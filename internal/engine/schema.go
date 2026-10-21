package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func schemaReserved(name string) error {
	if strings.HasPrefix(name, "pg_") {
		return roleDiagnostic("42939", fmt.Sprintf(`unacceptable schema name "%s"`, name), `The prefix "pg_" is reserved for system schemas.`)
	}
	return nil
}
func schemaNotice(op *session.Operation, code, message string) {
	op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: code, Message: message})
}

func (e *Engine) executeSchema(scope *StatementScope, op *session.Operation, stmt parser.Statement, count int, input sqltype.InputEnvironment) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if schema, ok := stmt.(*parser.CreateSchema); ok {
		for _, table := range schema.Tables {
			if table.PartitionOf != nil {
				return Result{}, expr.ErrorAt("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionOf.Pos)
			}
			if table.PartitionBy != nil {
				return Result{}, expr.ErrorAt("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionBy.Pos)
			}
		}
	}
	e.mu.Lock()
	stage, err := e.stageSchemaLocked(op, stmt, input, scope.output)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
	}
	// Retain the constructor's actual fresh domain identity for bounded Restore
	// ownership pairing; never resolve an arbitrary later object by name.
	var created ids.ObjectRef
	if _, domain := stmt.(*parser.CreateDomain); domain {
		for address, descriptor := range stage.Descriptors() {
			if typ, ok := descriptor.(*catalog.Type); ok && typ.SQLType.IsDomain() && stage.Base.Get(address) == nil {
				created = e.catalog.Domain.Ref(stage.Base.Epoch(), address)
				break
			}
		}
	}
	defer e.DiscardCatalogStage(stage)
	_, droppingTypedSource := stmt.(*parser.DropComposite)
	if err := scope.acquireTypedSources(stage, droppingTypedSource); err != nil {
		return Result{}, err
	}
	domainMode := parser.DropCascade
	cascade := false
	switch s := stmt.(type) {
	case *parser.DropComposite:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropDomain:
		domainMode = s.Mode
		cascade = s.Mode == parser.DropCascade
	case *parser.DropCollation:
		domainMode = s.Mode
		cascade = s.Mode == parser.DropCascade
	case *parser.DropConversion:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropOperator:
		domainMode, cascade = s.Mode, s.Mode == parser.DropCascade
	case *parser.DropSchema:
		cascade = s.Cascade
		if !s.Cascade {
			domainMode = parser.DropRestrict
		}
	}
	var removedRelations []ids.ObjectAddress
	for address, removal := range stage.Removals() {
		if _, ok := removal.Descriptor.(*catalog.Relation); ok {
			removedRelations = append(removedRelations, address)
		}
	}
	if err := scope.stageExpressionDependents(stage, removedRelations, parser.DropCascade, nil); err != nil {
		return Result{}, err
	}
	if err = scope.stageDomainDependents(stage, domainMode); err != nil {
		return Result{}, err
	}
	if err := scope.stageOwnedSequences(stage, removedRelations, parser.DropCascade, nil); err != nil {
		return Result{}, err
	}
	stage.schemaCascadeNames = append(stage.schemaCascadeNames, stage.viewCascadeNames...)
	for a := range stage.Removals() {
		if r, ok := stage.Base.Relation(a); ok && r.RelationKind == catalog.SequenceRelation {
			if err := scope.acquireSequence(e.catalog.Domain.Ref(stage.Base.Epoch(), a), lock.AccessExclusive, true); err != nil {
				return Result{}, err
			}
		}
	}
	if _, ok := stmt.(*parser.CreateSchema); ok {
		if err := scope.acquireDefinitionProviders(stage); err != nil {
			return Result{}, err
		}
	}
	if scope.replacementRead {
		e.replacementMu.RUnlock()
		scope.replacementRead = false
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err != nil {
		return Result{}, err
	}
	_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	if err == nil {
		for _, descriptor := range stage.Descriptors() {
			if ns, ok := descriptor.(*catalog.Namespace); ok && !ns.TemporaryToast && ns.Temporary == op.Session.SessionToken {
				op.Session.TemporaryNamespace = ns.Address()
			}
		}
		if cascade {
			details := stage.schemaCascadeNames
			if len(details) > 0 {
				message := details[0]
				detail := ""
				if len(details) > 1 {
					message = fmt.Sprintf("drop cascades to %d other objects", len(details))
					detail = strings.Join(details, "\n")
				}
				op.EmitNotice(session.LevelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
			}
		}
	}
	if err != nil {
		return Result{}, err
	}
	return Result{created: created}, nil
}
func (e *Engine) stageSchemaLocked(op *session.Operation, stmt parser.Statement, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (*CatalogStage, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return nil, err
	}
	if err = op.Context().Err(); err != nil {
		return nil, err
	}
	actor, err := auth.Roles.Descriptor(auth.State.CurrentUser)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.Session)
	host := ddlHost{e: e, op: op, stage: stage}
	databaseGate := func() error {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), catalog.CurrentDatabaseAddress)
		rights, err := auth.Effective(ref, false)
		if err != nil {
			return err
		}
		if rights&authorization.CreatePrivilege == 0 {
			database := auth.Catalog.MustDatabase(catalog.CurrentDatabaseAddress)
			name := database.Name()
			if name == "" {
				name = "stuntgres" // The unnamed native Engine's existing database display.
			}
			return sqlError("42501", "permission denied for database "+name)
		}
		return nil
	}
	lookup := func(name string) (*catalog.Namespace, error) {
		a, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
		if !ok {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name))
		}
		d, ok := auth.Catalog.Namespace(a)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid namespace descriptor")
		}
		return d, nil
	}
	own := func(d *catalog.Namespace) error {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
		return auth.Check(access.Requirement{Object: ref, OwnerOnly: true})
	}
	switch s := stmt.(type) {
	case *parser.CreateComposite:
		if err = typedef.StageCreateCompositeLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateCollation:
		if err = typedef.StageCreateCollationLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterCollation:
		if err = typedef.StageAlterCollationLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropCollation:
		if err = typedef.StageDropCollationLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateConversion:
		if err = typedef.StageCreateConversionLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterConversion:
		if err = typedef.StageAlterConversionLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropConversion:
		if err = typedef.StageDropConversionLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateFunction:
		if err = routine.StageCreateFunction(host, auth, s, input); err != nil {
			return nil, err
		}
	case *parser.CreateOperator:
		if err = routine.StageCreateOperator(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.AlterOperator:
		if err = routine.StageAlterOperator(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropOperator:
		if err = routine.StageDropOperator(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateDomain:
		if err = typedef.StageCreateDomainLocked(host, auth, s, input, output); err != nil {
			return nil, err
		}
	case *parser.DropDomain:
		if err = typedef.StageDropDomainLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.DropComposite:
		if err = typedef.StageDropCompositeLocked(host, auth, s); err != nil {
			return nil, err
		}
	case *parser.CreateSchema:
		owner, err := access.SchemaOwner(auth.View, s.Authorization, s.AuthorizationIdentity)
		if err != nil {
			return nil, err
		}
		name := s.Name
		if name == "" {
			d, err := auth.Roles.Descriptor(owner)
			if err != nil {
				return nil, err
			}
			name = d.Name
		}
		if err = databaseGate(); err != nil {
			return nil, err
		}
		if !actor.Superuser && owner != actor.Ref {
			return nil, sqlError("42501", fmt.Sprintf(`must be able to SET ROLE "%s"`, s.Authorization))
		}
		if err = schemaReserved(name); err != nil {
			return nil, err
		}
		if _, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}); ok {
			if s.IfNotExists {
				schemaNotice(op, "42P06", fmt.Sprintf(`schema "%s" already exists, skipping`, name))
				return nil, nil
			}
			return nil, sqlError("42P06", fmt.Sprintf(`schema "%s" already exists`, name))
		}
		oid, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		rev, err := e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: oid}, ObjectName: name, ObjectRevision: rev, Owner: owner}
		ns.ACL = stage.creationACL(owner, ids.ObjectAddress{}, authorization.DefaultSchemas)
		if err = stage.Create(ns); err != nil {
			return nil, err
		}
		// PostgreSQL analyzes elements as the schema owner with the new namespace
		// first in the explicit path. Keep both changes local to this stage.
		auth.State.CurrentUser = owner
		auth.pathPrefix = ns.Address()
		for _, table := range s.Tables {
			if table.Persistence == "temporary" {
				return nil, sqlError("0A000", "temporary tables in CREATE SCHEMA are unsupported")
			}
			if table.OnCommit != parser.OnCommitUnspecified {
				return nil, sqlError("42P16", "ON COMMIT can only be used on temporary tables")
			}
			if len(table.Name.Parts) == 2 && table.Name.Parts[0].Text != name {
				return nil, sqlError("42P15", fmt.Sprintf("CREATE specifies a schema (%s) different from the one being created (%s)", table.Name.Parts[0].Text, name))
			}
			if err = e.schemaTableLocked(op, auth, stage, table, ns.Address(), owner, input, nil); err != nil {
				return nil, err
			}
		}
	case *parser.AlterSchema:
		old, err := lookup(s.Name)
		if err != nil {
			return nil, err
		}
		next := *old
		if s.NewName != "" {
			if _, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: s.NewName}); ok {
				return nil, sqlError("42P06", fmt.Sprintf(`schema "%s" already exists`, s.NewName))
			}
			if err = own(old); err != nil {
				return nil, err
			}
			if err = databaseGate(); err != nil {
				return nil, err
			}
			if err = schemaReserved(s.NewName); err != nil {
				return nil, err
			}
			next.ObjectName = s.NewName
		} else {
			target, err := access.SchemaOwner(auth.View, s.Owner, s.OwnerIdentity)
			if err != nil {
				return nil, err
			}
			if target == old.Owner {
				return nil, nil
			}
			if err = own(old); err != nil {
				return nil, err
			}
			if err = access.OwnershipSuccessor(auth.View, target); err != nil {
				return nil, err
			}
			if err = access.OwnershipDatabaseCreate(auth.View, auth); err != nil {
				return nil, err
			}
			next.Owner = target
			next.ACL = authorization.SchemaACLOwner(old.ACL, old.Owner, target)
		}
		next.ObjectRevision, err = e.catalog.revisionLocked()
		if err != nil {
			return nil, err
		}
		if err = stage.Replace(&next); err != nil {
			return nil, err
		}
	case *parser.DropSchema:
		seen := make(map[ids.ObjectAddress]bool)
		var schemas []*catalog.Namespace
		for _, name := range s.Names {
			d, err := lookup(name)
			if err != nil {
				if s.IfExists {
					schemaNotice(op, "00000", fmt.Sprintf(`schema "%s" does not exist, skipping`, name))
					continue
				}
				return nil, err
			}
			if seen[d.Address()] {
				continue
			}
			seen[d.Address()] = true
			if err = own(d); err != nil {
				return nil, err
			}
			schemas = append(schemas, d)
		}
		var dependent []*catalog.Relation
		var domains []catalog.Descriptor
		children, err := catalog.NamespaceChildren(auth.Catalog, seen)
		if err != nil {
			return nil, err
		}
		for _, address := range children {
			d := auth.Catalog.Get(address)
			if catalog.ExtensionMembership(d) != (ids.ObjectAddress{}) {
				continue // the extension owns these members as one namespace dependent
			}
			if r, ok := d.(*catalog.Relation); ok {
				if r.RelationKind == catalog.CompositeRelation {
					continue
				}
				if r.SQLIndex != nil {
					continue
				}
				dependent = append(dependent, r)
			} else if t, ok := d.(*catalog.Type); ok && t.SQLType.IsUserDefined() && (t.OwningRelation == (ids.ObjectAddress{}) || t.SQLType.IsComposite() && auth.Catalog.MustRelation(t.OwningRelation).RelationKind == catalog.CompositeRelation) {
				domains = append(domains, t)
			} else if c, ok := d.(*catalog.Collation); ok {
				domains = append(domains, c)
			} else if c, ok := d.(*catalog.Conversion); ok {
				domains = append(domains, c)
			} else if operator, ok := d.(*catalog.Operator); ok {
				domains = append(domains, operator)
			} else if extension, ok := d.(*catalog.Extension); ok {
				domains = append(domains, extension)
			} else if procedure, ok := d.(*catalog.Procedure); ok && procedure.Foreign != nil {
				domains = append(domains, procedure)
			} else if t, ok := d.(*catalog.Type); ok && (t.OwningRelation != (ids.ObjectAddress{}) || t.SQLType.IsArray() && t.SQLType.Element().IsUserDefined()) {
				continue // removed automatically with the owning relation or element type
			} else {
				return nil, sqlError("0A000", "dropping schemas with unsupported object dependencies is unsupported")
			}
		}
		// A schema is also a referent of scalar REGNAMESPACE constants in
		// definitions outside it. Feed its identity to the existing dependency
		// closure before publication; contained-object removal alone is not enough.
		for _, schema := range schemas {
			stage.domainTargets = append(stage.domainTargets, schema.Address())
		}
		stage.dependencyPath, err = e.referencePath(auth)
		if err != nil {
			return nil, err
		}
		if len(domains) != 0 {
			if err = e.stageSchemaDomainsLocked(op, auth, stage, schemas, dependent, domains, s.Cascade); err != nil {
				return nil, err
			}
			break
		}
		sort.Slice(dependent, func(i, j int) bool { return dependent[i].Address().ObjectID < dependent[j].Address().ObjectID })
		if len(dependent) > 0 && !s.Cascade {
			message := "cannot drop desired object(s) because other objects depend on them"
			if len(schemas) == 1 {
				message = "cannot drop schema " + catalog.DependencyIdentifier(schemas[0].Name()) + " because other objects depend on it"
			}
			details := make([]string, len(dependent))
			for i, r := range dependent {
				ns := auth.Catalog.Get(r.Namespace()).Name()
				details[i] = relationDiagnosticKind(r) + " " + relationDiagnosticName(auth, r) + " depends on schema " + catalog.DependencyIdentifier(ns)
			}
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		}
		if s.Cascade {
			for _, r := range dependent {
				stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+relationDiagnosticKind(r)+" "+relationDiagnosticName(auth, r))
			}
		}
		for _, r := range dependent {
			if err = stage.removeObjectLocked(e.catalog.Domain.Ref(auth.Catalog.Epoch(), r.Address()), false); err != nil {
				return nil, err
			}
		}
		for _, d := range schemas {
			if err = stage.removeObjectLocked(e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address()), true); err != nil {
				return nil, err
			}
		}
		if len(schemas) == 0 {
			return nil, nil
		}
	default:
		return nil, fmt.Errorf("stuntgres: invalid schema statement")
	}
	return stage, nil
}
