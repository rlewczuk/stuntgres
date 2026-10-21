package engine

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (e *Engine) schemaTableLocked(op *session.Operation, auth AuthorizationView, stage *CatalogStage, s *parser.CreateTable, ns ids.ObjectAddress, owner authorization.PrincipalRef, input sqltype.InputEnvironment, foreignTargets []ids.ObjectRef) error {
	auth.Catalog = stage.Base
	auth.Recapture(auth.Catalog)
	if namespace, ok := auth.Catalog.Namespace(ns); ok && !namespace.TemporaryToast && auth.session != nil && namespace.Temporary == auth.session.SessionToken {
		auth.stagedTemporary = ns
	}
	if stage.DescriptorCount() != 0 || stage.RemovalCount() != 0 || stage.OldNameCount() != 0 {
		auth.Catalog = e.stageRead(stage)
		auth.Recapture(auth.Catalog)
	}
	name := s.Name.Parts[len(s.Name.Parts)-1].Text
	key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name}
	_, baseExists := stage.Base.LookupName(key)
	_, stagedExists := stage.NameBinding(key)
	if baseExists || stagedExists {
		if s.IfNotExists {
			op.ExistingRelationNotice(name)
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
	if stage.DescriptorCount() != 0 || stage.RemovalCount() != 0 || stage.OldNameCount() != 0 {
		auth.Catalog = e.stageRead(stage)
		auth.Recapture(auth.Catalog)
	}
	b, err := describe(op.Context(), e.analysisHost(op), s, auth, ids.ObjectRef{}, input, nil, nil)
	if err != nil {
		return err
	}
	input.EnumMemberSafe = b.Resolvers.EnumMemberSafe
	for _, column := range b.Create.Columns {
		ref := e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(column.Usage.Type.ID())})
		if b.Create.OfType == nil {
			if err := auth.CanUse(ref); err != nil {
				return err
			}
		}
		stage.domainBases = append(stage.domainBases, auth.Catalog.Get(ref.Address))
	}
	d, err := e.newTableDescriptorLocked(ns, name, owner, b.Create.Columns, b.Create.ColumnIndex)
	if err != nil {
		return err
	}
	if b.Create.OfType != nil {
		d.OfType = b.Create.OfType.Address()
		stage.domainBases = append(stage.domainBases, b.Create.OfType)
	}
	d.Security.ACL = stage.creationACL(owner, ns, authorization.DefaultTables)
	if err := e.stageRowTypeLocked(stage, d); err != nil {
		return err
	}
	d.OnCommit = s.OnCommit
	d.Constraints.NotNull = b.Create.NotNull
	if err := e.stageInheritance(op, auth, stage, d, b); err != nil {
		return err
	}
	if err := table.NameCreateNotNull(d, auth.Catalog); err != nil {
		return err
	}
	if err := e.stageCreateIdentities(auth, stage, d, s); err != nil {
		return err
	}
	if err := e.stageCreateSerials(stage, d, b.Create.Declarations); err != nil {
		return err
	}
	if err := e.stageLikeColumns(op, auth, stage, d, b.Create.Likes); err != nil {
		return err
	}
	// All owned sequences and their defaults share the private CREATE candidate.
	auth.Catalog = e.stageRead(stage)
	auth.Recapture(auth.Catalog)
	// Preserve operation safety, but resolve diagnostic names lazily against
	// the staged catalog that owns these defaults, generations and CHECKs.
	input.TypeDiagnosticName = nil
	binder := newExpressionBinder(op.Context(), e, input, op, nil)
	binder.Query.Auth = &auth
	binder.Consumer.DefaultTemporary = auth.Catalog.MustNamespace(d.Namespace()).Temporary
	for slot, declaration := range b.Create.Declarations {
		if declaration.DefaultExpr != nil {
			delete(d.ColumnMeta.Defaults, d.Attributes[slot])
		}
	}
	defaults, err := bindCreateDefaults(binder, d.Columns, b.Create.Declarations)
	if err != nil {
		return err
	}
	if d.ColumnMeta.Defaults == nil {
		d.ColumnMeta.Defaults = defaults
	} else {
		for attribute, definition := range defaults {
			d.ColumnMeta.Defaults[attribute] = definition
		}
	}
	if err := e.bindCreateGenerations(op, auth, d, b.Create.Declarations, input); err != nil {
		return err
	}
	if err := e.bindCreateChecks(op, auth, s, d, input, nil); err != nil {
		return err
	}
	if err := e.stageLikeChecks(op, auth, d, b.Create.Likes); err != nil {
		return err
	}
	if err := stored.RefreshGenerationChecks(d, auth.Catalog); err != nil {
		return err
	}
	if err := e.assignDefinitionOIDsLocked(d); err != nil {
		return err
	}
	// PostgreSQL resolves raw column modifiers again while building the
	// relation descriptor, without a parser state/source position.
	for _, element := range s.Elements {
		if column := element.Column; column != nil && len(column.TypeName.Modifiers) > 0 {
			i := d.Index[column.Name]
			// Temporal modifiers emit a second descriptor-build warning.
			if id := d.Columns[i].Usage.Type.ID(); id != sqltype.Interval && id != sqltype.Timestamp && id != sqltype.Time && id != sqltype.Timestamptz && id != sqltype.Timetz {
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
			usage, warning, err := d.Columns[i].Usage.Type.ResolveModifiers(modifiers...)
			if err != nil {
				return err
			}
			d.Columns[i].Usage = usage
			if warning != nil {
				op.EmitNotice(session.LevelWarning, *warning)
			}
		}
	}
	if err := e.stageCreateUniques(op, stage, d, b.Create.Uniques); err != nil {
		return err
	}
	if err := e.stageCreateExclusions(op, input, auth, stage, d, s); err != nil {
		return err
	}
	if err := e.stageLikeIndexes(op, auth, stage, d, b.Create.Likes); err != nil {
		return err
	}
	return e.stageCreateForeignKeys(auth, stage, d, s, foreignTargets)
}

// The table identity owner is shared by declared and query-derived schemas.
// The caller transfers a private column slice; source definitions are not mutated.
func (e *Engine) newTableDescriptorLocked(ns ids.ObjectAddress, name string, owner authorization.PrincipalRef, columns []catalog.Column, index map[string]int) (*catalog.Relation, error) {
	oid, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return nil, err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return nil, err
	}
	if index == nil {
		index = make(map[string]int, len(columns))
		for i, column := range columns {
			index[column.Name] = i
		}
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectNamespace: ns, ObjectName: name, ObjectRevision: revision, Owner: owner, Columns: columns, Index: index, Attributes: make([]int32, len(columns))}
	d.ColumnMeta.HighWater = int32(len(columns))
	for i := range columns {
		d.Attributes[i] = int32(i + 1)
		d.Columns[i].OriginTable = uint32(oid)
		d.Columns[i].OriginAttribute = int16(i + 1)
	}
	return d, nil
}
