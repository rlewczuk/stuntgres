package engine

import (
	"fmt"
	"sort"
	"strings"
	"stuntgres/internal/engine/authorization"
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
func schemaNotice(op *Operation, code, message string) {
	op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: code, Message: message})
}
func schemaOwner(auth AuthorizationView, name string, identity bool) (authorization.PrincipalRef, error) {
	if name == "" {
		return auth.state.CurrentUser, nil
	}
	if identity {
		switch name {
		case "current_user", "current_role":
			return auth.state.CurrentUser, nil
		case "session_user":
			return auth.state.SessionUser, nil
		}
	}
	role, ok := auth.roles.Lookup(name)
	if !ok {
		return authorization.PrincipalRef{}, sqlError("42704", fmt.Sprintf(`role "%s" does not exist`, name))
	}
	return role.Ref, nil
}

// Non-table creation and namespace moves retain their TEMP capability boundary.
func checkPersistentCreationNamespace(catalog CatalogRead, namespace ObjectAddress) error {
	if catalog.state.descriptors[namespace].(*namespaceDescriptor).temporary != (SessionToken{}) {
		return sqlError("0A000", "temporary schema creation is unsupported")
	}
	return nil
}

func (e *Engine) schemaTableLocked(op *Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateTable, ns ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ObjectRef) error {
	auth.catalog = stage.base
	auth.recaptureCatalog(auth.catalog)
	if namespace, ok := auth.catalog.state.descriptors[ns].(*namespaceDescriptor); ok && auth.session != nil && namespace.temporary == auth.session.token {
		auth.stagedTemporary = ns
	}
	if len(stage.descriptors) != 0 || len(stage.removals) != 0 || len(stage.oldNames) != 0 {
		auth.catalog = e.stageRead(stage)
		auth.recaptureCatalog(auth.catalog)
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := NameKey{ClassID: RelationClassID, Namespace: ns, Name: name}
	_, baseExists := stage.base.state.names[key]
	_, stagedExists := stage.names[key]
	if baseExists || stagedExists {
		if s.IfNotExists {
			op.existingRelationNotice(name)
			return nil
		}
		return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
	}
	available, err := e.displaceAssociatedArrayLocked(stage, ns, name)
	if err != nil {
		return err
	}
	if !available {
		diagnostic := sqlError("42710", fmt.Sprintf(`type "%s" already exists`, name)).(*sqlstate.Error)
		diagnostic.Hint = "A relation has an associated type of the same name, so you must use a name that doesn't conflict with any existing type."
		return diagnostic
	}
	if len(stage.descriptors) != 0 || len(stage.removals) != 0 || len(stage.oldNames) != 0 {
		auth.catalog = e.stageRead(stage)
		auth.recaptureCatalog(auth.catalog)
	}
	b, err := e.describe(op.Context(), s, auth, ObjectRef{}, input, op, nil, nil)
	if err != nil {
		return err
	}
	input.EnumMemberSafe = b.enumMemberSafe
	for _, column := range b.createColumns {
		if typ := column.Usage.Type; typ.IsUserDefined() || typ.IsArray() && typ.Element().IsDomain() {
			ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: TypeClassID, ObjectID: OID(column.Usage.Type.ID())}}
			if b.createOfType == nil {
				if err := auth.CanUse(ref); err != nil {
					return err
				}
			}
			stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[ref.Address])
		}
	}
	d, err := e.newTableDescriptorLocked(ns, name, owner, b.createColumns, b.createColumnIndex)
	if err != nil {
		return err
	}
	if b.createOfType != nil {
		d.ofType = b.createOfType.Address()
		stage.domainBases = append(stage.domainBases, b.createOfType)
	}
	d.acl = stage.creationACL(owner, ns, authorization.DefaultTables)
	if err := e.stageRowTypeLocked(stage, d); err != nil {
		return err
	}
	d.onCommit = s.OnCommit
	d.notNullConstraints = b.createNotNull
	if err := e.stageInheritance(op, auth, stage, d, b); err != nil {
		return err
	}
	if err := nameCreateNotNull(d, auth.catalog); err != nil {
		return err
	}
	if err := e.stageCreateIdentities(auth, stage, d, s); err != nil {
		return err
	}
	if err := e.stageCreateSerials(stage, d, b.createDeclarations); err != nil {
		return err
	}
	if err := e.stageLikeColumns(op, auth, stage, d, b.createLikes); err != nil {
		return err
	}
	// All owned sequences and their defaults share the private CREATE candidate.
	auth.catalog = e.stageRead(stage)
	auth.recaptureCatalog(auth.catalog)
	// Preserve operation safety, but resolve diagnostic names lazily against
	// the staged catalog that owns these defaults, generations and CHECKs.
	input.TypeDiagnosticName = nil
	binder := newExpressionBinder(op.Context(), e, input, op, nil)
	binder.queryAuth = &auth
	binder.defaultTemporary = auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor).temporary
	for slot, declaration := range b.createDeclarations {
		if declaration.defaultExpr != nil {
			delete(d.defaults, d.attributes[slot])
		}
	}
	defaults, err := bindCreateDefaults(binder, d.columns, b.createDeclarations)
	if err != nil {
		return err
	}
	if d.defaults == nil {
		d.defaults = defaults
	} else {
		for attribute, definition := range defaults {
			d.defaults[attribute] = definition
		}
	}
	if err := e.bindCreateGenerations(op, auth, d, b.createDeclarations, input); err != nil {
		return err
	}
	if err := e.bindCreateChecks(op, auth, s, d, input, nil); err != nil {
		return err
	}
	if err := e.stageLikeChecks(op, auth, d, b.createLikes); err != nil {
		return err
	}
	if err := refreshGenerationChecks(d, auth.catalog); err != nil {
		return err
	}
	if err := e.assignDefinitionOIDsLocked(d); err != nil {
		return err
	}
	// PostgreSQL resolves raw column modifiers again while building the
	// relation descriptor, without a parser state/source position.
	for _, element := range s.Elements {
		if column := element.Column; column != nil && len(column.TypeName.Modifiers) > 0 {
			i := d.index[column.Name]
			// Temporal modifiers emit a second descriptor-build warning.
			if id := d.columns[i].Usage.Type.ID(); id != sqltype.Interval && id != sqltype.Timestamp && id != sqltype.Time && id != sqltype.Timestamptz && id != sqltype.Timetz {
				continue
			}
			modifiers := make([]int32, len(column.TypeName.Modifiers))
			valid := true
			for j, modifier := range column.TypeName.Modifiers {
				if modifier.Integer == nil {
					valid = false
					break
				}
				modifiers[j] = *modifier.Integer
			}
			if !valid {
				continue
			}
			usage, warning, err := d.columns[i].Usage.Type.ResolveModifiers(modifiers...)
			if err != nil {
				return err
			}
			d.columns[i].Usage = usage
			if warning != nil {
				op.emitNotice(levelWarning, *warning)
			}
		}
	}
	if err := e.stageCreateUniques(op, stage, d, b.createUniques); err != nil {
		return err
	}
	if err := e.stageCreateExclusions(op, input, auth, stage, d, s); err != nil {
		return err
	}
	if err := e.stageLikeIndexes(op, auth, stage, d, b.createLikes); err != nil {
		return err
	}
	return e.stageCreateForeignKeys(auth, stage, d, s, foreignTargets)
}

// The table identity owner is shared by declared and query-derived schemas.
// The caller transfers a private column slice; source definitions are not mutated.
func (e *Engine) newTableDescriptorLocked(ns ObjectAddress, name string, owner authorization.PrincipalRef, columns []ColumnDescriptor, index map[string]int) (*relationDescriptor, error) {
	oid, err := e.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	revision, err := e.revisionLocked()
	if err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]int, len(columns))
		for i, column := range columns {
			index[column.Name] = i
		}
	}
	d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: oid}, namespace: ns, name: name, revision: revision, owner: owner, columns: columns, index: index, attributes: make([]int32, len(columns))}
	d.attributeHighWater = int32(len(columns))
	for i := range columns {
		d.attributes[i] = int32(i + 1)
		d.columns[i].OriginTable = uint32(oid)
		d.columns[i].OriginAttribute = int16(i + 1)
	}
	return d, nil
}
func (e *Engine) executeSchema(scope *StatementScope, op *Operation, stmt parser.Statement, count int, input sqltype.InputEnvironment) (Result, error) {
	if count != 0 {
		return Result{}, fmt.Errorf("stuntgres: got %d arguments, want 0", count)
	}
	if schema, ok := stmt.(*parser.CreateSchema); ok {
		for _, table := range schema.Tables {
			if table.PartitionOf != nil {
				return Result{}, expressionError("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionOf.Pos)
			}
			if table.PartitionBy != nil {
				return Result{}, expressionError("0A000", "partition definitions in CREATE SCHEMA are unsupported", table.PartitionBy.Pos)
			}
		}
	}
	e.mu.Lock()
	stage, err := e.stageSchemaLocked(op, stmt, input)
	e.mu.Unlock()
	if err != nil || stage == nil {
		return Result{}, err
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
	case *parser.DropSchema:
		cascade = s.Cascade
	}
	var removedRelations []ObjectAddress
	for address, removal := range stage.removals {
		if _, ok := removal.descriptor.(*relationDescriptor); ok {
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
	for a := range stage.removals {
		if r, ok := stage.base.state.descriptors[a].(*relationDescriptor); ok && r.relationKind == sequenceRelation {
			if err := scope.acquireSequence(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: a}, accessExclusive, true); err != nil {
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
	_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: stage.base.Epoch()})
	if err == nil {
		for _, descriptor := range stage.descriptors {
			if ns, ok := descriptor.(*namespaceDescriptor); ok && ns.temporary == op.session.token {
				op.session.temporaryNamespace = ns.address
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
				op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
			}
		}
	}
	return Result{}, err
}
func (e *Engine) stageSchemaLocked(op *Operation, stmt parser.Statement, input sqltype.InputEnvironment) (*CatalogStage, error) {
	auth, err := e.authorizationLocked(op)
	if err != nil {
		return nil, err
	}
	if err = op.Context().Err(); err != nil {
		return nil, err
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return nil, err
	}
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authorizeLocked(auth, op.session)
	databaseGate := func() error {
		if !actor.Superuser {
			return sqlError("42501", "permission denied for database stuntgres")
		}
		return nil
	}
	lookup := func(name string) (*namespaceDescriptor, error) {
		a, ok := auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name}]
		if !ok {
			return nil, sqlError("3F000", fmt.Sprintf(`schema "%s" does not exist`, name))
		}
		d, ok := auth.catalog.state.descriptors[a].(*namespaceDescriptor)
		if !ok {
			return nil, fmt.Errorf("stuntgres: invalid namespace descriptor")
		}
		return d, nil
	}
	own := func(d *namespaceDescriptor) error {
		ref := ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: d.address}
		return auth.Check(AccessRequirement{Object: ref, OwnerOnly: true})
	}
	switch s := stmt.(type) {
	case *parser.CreateComposite:
		if err = e.stageCreateCompositeLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.CreateCollation:
		if err = e.stageCreateCollationLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.AlterCollation:
		if err = e.stageAlterCollationLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.DropCollation:
		if err = e.stageDropCollationLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.CreateDomain:
		if err = e.stageCreateDomainLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.DropDomain:
		if err = e.stageDropDomainLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.DropComposite:
		if err = e.stageDropCompositeLocked(op, auth, stage, s); err != nil {
			return nil, err
		}
	case *parser.CreateSchema:
		owner, err := schemaOwner(auth, s.Authorization, s.AuthorizationIdentity)
		if err != nil {
			return nil, err
		}
		name := s.Name
		if name == "" {
			d, err := auth.roles.Descriptor(owner)
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
		if _, ok := auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name}]; ok {
			if s.IfNotExists {
				schemaNotice(op, "42P06", fmt.Sprintf(`schema "%s" already exists, skipping`, name))
				return nil, nil
			}
			return nil, sqlError("42P06", fmt.Sprintf(`schema "%s" already exists`, name))
		}
		oid, err := e.allocateOIDLocked()
		if err != nil {
			return nil, err
		}
		rev, err := e.revisionLocked()
		if err != nil {
			return nil, err
		}
		ns := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: oid}, name: name, revision: rev, owner: owner}
		ns.acl = stage.creationACL(owner, ObjectAddress{}, authorization.DefaultSchemas)
		if err = stage.Create(ns); err != nil {
			return nil, err
		}
		// PostgreSQL analyzes elements as the schema owner with the new namespace
		// first in the explicit path. Keep both changes local to this stage.
		auth.state.CurrentUser = owner
		auth.pathPrefix = ns.address
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
			if err = e.schemaTableLocked(op, auth, stage, table, ns.address, owner, input, nil); err != nil {
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
			if _, ok := auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: s.NewName}]; ok {
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
			next.name = s.NewName
		} else {
			target, err := schemaOwner(auth, s.Owner, s.OwnerIdentity)
			if err != nil {
				return nil, err
			}
			if target == old.owner {
				return nil, nil
			}
			if err = own(old); err != nil {
				return nil, err
			}
			if err = ownershipSuccessor(auth, target); err != nil {
				return nil, err
			}
			if err = ownershipDatabaseCreate(auth); err != nil {
				return nil, err
			}
			next.owner = target
			next.acl = authorization.SchemaACLOwner(old.acl, old.owner, target)
		}
		next.revision, err = e.revisionLocked()
		if err != nil {
			return nil, err
		}
		if err = stage.Replace(&next); err != nil {
			return nil, err
		}
	case *parser.DropSchema:
		seen := make(map[ObjectAddress]bool)
		var schemas []*namespaceDescriptor
		for _, name := range s.Names {
			d, err := lookup(name)
			if err != nil {
				if s.IfExists {
					schemaNotice(op, "00000", fmt.Sprintf(`schema "%s" does not exist, skipping`, name))
					continue
				}
				return nil, err
			}
			if seen[d.address] {
				continue
			}
			seen[d.address] = true
			if err = own(d); err != nil {
				return nil, err
			}
			schemas = append(schemas, d)
		}
		var dependent []*relationDescriptor
		var domains []Descriptor
		children, err := namespaceChildren(auth.catalog, seen)
		if err != nil {
			return nil, err
		}
		for _, address := range children {
			d := auth.catalog.state.descriptors[address]
			if extensionMembership(d) != (ObjectAddress{}) {
				continue // the extension owns these members as one namespace dependent
			}
			if r, ok := d.(*relationDescriptor); ok {
				if r.relationKind == compositeRelation {
					continue
				}
				if r.sqlIndex != nil {
					continue
				}
				dependent = append(dependent, r)
			} else if t, ok := d.(*typeDescriptor); ok && t.typ.IsUserDefined() && (t.owningRelation == (ObjectAddress{}) || t.typ.IsComposite() && auth.catalog.state.descriptors[t.owningRelation].(*relationDescriptor).relationKind == compositeRelation) {
				domains = append(domains, t)
			} else if c, ok := d.(*collationDescriptor); ok {
				domains = append(domains, c)
			} else if extension, ok := d.(*extensionDescriptor); ok {
				domains = append(domains, extension)
			} else if t, ok := d.(*typeDescriptor); ok && (t.owningRelation != (ObjectAddress{}) || t.typ.IsArray() && t.typ.Element().IsUserDefined()) {
				continue // removed automatically with the owning relation or element type
			} else {
				return nil, sqlError("0A000", "dropping schemas with unsupported object dependencies is unsupported")
			}
		}
		if len(domains) != 0 {
			if err = e.stageSchemaDomainsLocked(op, auth, stage, schemas, dependent, domains, s.Cascade); err != nil {
				return nil, err
			}
			break
		}
		sort.Slice(dependent, func(i, j int) bool { return dependent[i].address.ObjectID < dependent[j].address.ObjectID })
		if len(dependent) > 0 && !s.Cascade {
			message := "cannot drop desired object(s) because other objects depend on them"
			if len(schemas) == 1 {
				message = "cannot drop schema " + dependencyIdentifier(schemas[0].name) + " because other objects depend on it"
			}
			details := make([]string, len(dependent))
			for i, r := range dependent {
				ns := auth.catalog.state.descriptors[r.namespace].Name()
				details[i] = relationDiagnosticKind(r) + " " + relationDiagnosticName(auth, r) + " depends on schema " + dependencyIdentifier(ns)
			}
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: message, Detail: strings.Join(details, "\n"), Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
		}
		if s.Cascade {
			for _, r := range dependent {
				stage.schemaCascadeNames = append(stage.schemaCascadeNames, "drop cascades to "+relationDiagnosticKind(r)+" "+relationDiagnosticName(auth, r))
			}
		}
		for _, r := range dependent {
			if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: r.address}, false); err != nil {
				return nil, err
			}
		}
		for _, d := range schemas {
			if err = stage.removeObjectLocked(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: d.address}, true); err != nil {
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
