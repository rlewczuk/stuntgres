package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// OF names are catalog object names, not SQL declaration aliases.
func (e *Engine) namedType(auth AuthorizationView, name parser.ObjectName) (*typeDescriptor, error) {
	path, err := e.lookupNamespaces(auth, name, typeLookup)
	if err != nil {
		return nil, err
	}
	for i := range path.count() {
		found := auth.catalog.Lookup(NameKey{ClassID: TypeClassID, Namespace: path.at(i), Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 1 {
			return auth.catalog.state.descriptors[found[0]].(*typeDescriptor), nil
		}
	}
	return nil, expressionError("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(name)), name.Parts[0].Pos)
}

func standaloneComposite(catalog CatalogRead, typ *typeDescriptor, path []ObjectAddress) (*relationDescriptor, error) {
	name := typ.typ.DiagnosticName()
	if typ.typ.IsUserDefined() {
		name = typeDiagnosticNameAt(catalog, typ, path)
	}
	if !typ.typ.IsComposite() {
		return nil, sqlError("42809", "type "+name+" is not a composite type")
	}
	source, ok := catalog.state.descriptors[typ.owningRelation].(*relationDescriptor)
	if !ok || source.rowType != typ.typ.ID() {
		return nil, fmt.Errorf("stuntgres: missing composite type authority")
	}
	if source.relationKind != compositeRelation {
		d := sqlError("42809", "type "+name+" is the row type of another table").(*sqlstate.Error)
		d.Detail = "A typed table must use a stand-alone composite type created with CREATE TYPE."
		return nil, d
	}
	return source, nil
}

func (e *Engine) bindTypedColumns(auth AuthorizationView, s *parser.CreateTable) ([]ColumnDescriptor, []createColumnDeclaration, *typeDescriptor, error) {
	typ, err := e.namedType(auth, *s.OfType)
	if err != nil {
		return nil, nil, nil, err
	}
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, nil, nil, err
	}
	source, err := standaloneComposite(auth.catalog, typ, path)
	if err == nil {
		err = auth.CanUse(ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: typ.Address()})
	}
	if err != nil {
		return nil, nil, nil, err
	}
	columns := make([]ColumnDescriptor, len(source.columns))
	declarations := make([]createColumnDeclaration, len(columns))
	for i, member := range source.columns {
		if isSystemColumn(member.Name) {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, member.Name))
		}
		columns[i] = ColumnDescriptor{Name: member.Name, Usage: member.Usage, Nullable: true, NullabilityKnown: true}
	}
	seen := make(map[string]bool, len(s.Elements))
	for _, element := range s.Elements {
		column := element.Column
		if column == nil {
			continue
		}
		slot, exists := source.index[column.Name]
		if !exists {
			return nil, nil, nil, sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, column.Name))
		}
		if seen[column.Name] {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, column.Name))
		}
		seen[column.Name] = true
		declaration, err := bindCreateColumnDeclaration(column, s.Name.Parts[len(s.Name.Parts)-1].Text)
		if err != nil {
			return nil, nil, nil, err
		}
		if declaration.identity != nil {
			return nil, nil, nil, sqlError("0A000", "identity columns are not supported on typed tables")
		}
		if declaration.generation != nil {
			return nil, nil, nil, sqlError("0A000", "generated columns are not supported on typed tables")
		}
		declarations[slot] = declaration
	}
	return columns, declarations, typ, nil
}

func typedShape(table, source *relationDescriptor) error {
	for i, member := range source.columns {
		if i >= len(table.columns) {
			return sqlError("42804", fmt.Sprintf(`table is missing column "%s"`, member.Name))
		}
		actual := table.columns[i]
		if actual.Name != member.Name {
			return sqlError("42804", fmt.Sprintf(`table has column "%s" where type requires "%s"`, actual.Name, member.Name))
		}
		if actual.Usage != member.Usage {
			return sqlError("42804", fmt.Sprintf(`table "%s" has different type for column "%s"`, table.name, member.Name))
		}
	}
	if len(table.columns) > len(source.columns) {
		return sqlError("42804", fmt.Sprintf(`table has extra column "%s"`, table.columns[len(source.columns)].Name))
	}
	return nil
}

func validateTypedAttachment(catalog CatalogRead, table *relationDescriptor) error {
	if table.ofType == (ObjectAddress{}) {
		return nil
	}
	if table.ofType.ClassID != TypeClassID || table.ofType.SubID != 0 || table.relationKind != tableRelation {
		return fmt.Errorf("stuntgres: invalid typed attachment")
	}
	typ, ok := catalog.state.descriptors[table.ofType].(*typeDescriptor)
	if !ok {
		return fmt.Errorf("stuntgres: missing typed source")
	}
	source, err := standaloneComposite(catalog, typ, nil)
	if err != nil {
		return err
	}
	if table.inheritance != nil && len(table.inheritance.parents) != 0 {
		return sqlError("42809", "typed tables cannot inherit")
	}
	return typedShape(table, source)
}

func (e *Engine) alterTableOfLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTableOf, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	old, ok := scope.lockedDescriptor.(*relationDescriptor)
	if !ok {
		return fail(ErrCatalogReplaced)
	}
	if err := tableKind(old, "OF"); err != nil {
		return fail(err)
	}
	next := *old
	if s.Type == nil {
		if old.ofType == (ObjectAddress{}) {
			return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a typed table`, old.name)))
		}
		next.ofType = ObjectAddress{}
	} else {
		typ, err := e.namedType(auth, *s.Type)
		if err != nil {
			return fail(err)
		}
		path, err := e.referencePath(auth)
		if err != nil {
			return fail(err)
		}
		source, err := standaloneComposite(auth.catalog, typ, path)
		if err != nil {
			return fail(err)
		}
		// PostgreSQL18 attachment is owner-authorized without source USAGE;
		// CREATE OF intentionally has a separate source-USAGE gate.
		if old.inheritance != nil && len(old.inheritance.parents) != 0 {
			return fail(sqlError("42809", "typed tables cannot inherit"))
		}
		if err := typedShape(old, source); err != nil {
			return fail(err)
		}
		next.ofType = typ.Address()
	}
	if old.revision == DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	next.revision++
	stage := e.BeginCatalogStage(auth.catalog)
	stage.authSession, stage.authRevision, stage.roleRevision = op.session, auth.revision, auth.roles.state.revision
	if next.ofType != (ObjectAddress{}) {
		stage.domainBases = append(stage.domainBases, auth.catalog.state.descriptors[next.ofType])
	}
	e.mu.Unlock()
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		return Result{}, err
	}
	if s.Type != nil {
		// Execution can select a source created while target admission waited.
		// Grant the actual staged attachment, not just the pre-wait source.
		replacementRead := scope.replacementRead
		if err := scope.acquireTypedSources(stage, false); err != nil {
			return Result{}, err
		}
		// Release only this helper's incidental reservation before provider
		// callbacks; preserve any replacement admission held on entry.
		if !replacementRead && scope.replacementRead {
			e.replacementMu.RUnlock()
			scope.replacementRead = false
		}
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), stage)
	if err == nil {
		_, err = e.publishCatalogOperation(op, prepared, PublicationToken{Epoch: stage.base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func typedDirectActionsGuard(table *relationDescriptor, actions []parser.AlterTableAction) error {
	if table.ofType == (ObjectAddress{}) {
		return nil
	}
	for _, action := range actions {
		switch action.Action {
		case parser.AddColumn:
			return sqlError("42809", "cannot add column to typed table")
		case parser.DropColumn:
			return sqlError("42809", "cannot drop column from typed table")
		case parser.AlterColumnType:
			if _, exists := table.index[action.Column.Text]; !exists {
				return sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, action.Column.Text, table.name))
			}
			return expressionError("42809", "cannot alter column type of typed table", action.Column.Pos)
		}
	}
	return nil
}

func (scope *StatementScope) acquireTypedSources(stage *CatalogStage, drop bool) error {
	var sources map[ObjectAddress]bool
	if drop {
		if len(stage.domainTargets) != 0 {
			sources = make(map[ObjectAddress]bool, len(stage.domainTargets))
		}
		for _, source := range stage.domainTargets {
			sources[source] = true
		}
	} else {
		for _, descriptor := range stage.descriptors {
			if table, ok := descriptor.(*relationDescriptor); ok && table.ofType != (ObjectAddress{}) {
				if sources == nil {
					sources = make(map[ObjectAddress]bool)
				}
				sources[table.ofType] = true
			}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	addresses := make([]ObjectAddress, 0, len(sources))
	for source := range sources {
		addresses = append(addresses, source)
	}
	slices.SortFunc(addresses, func(a, b ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	mode := accessShare
	if drop {
		mode = accessExclusive
	}
	for _, source := range addresses {
		typ, ok := stage.base.state.descriptors[source].(*typeDescriptor)
		if !ok {
			continue
		}
		if err := scope.acquireSequence(ObjectRef{owner: stage.owner.owner, Epoch: stage.base.Epoch(), Address: typ.owningRelation}, mode, true); err != nil {
			return err
		}
		// A wait may admit a different definition at the same address. Keep
		// the copied shape tied to the exact captured reciprocal providers;
		// neither a new generation nor a same-name source can be adopted.
		e := stage.owner
		e.mu.Lock()
		valid := e.state.descriptors[source] == typ && e.state.descriptors[typ.owningRelation] == stage.base.state.descriptors[typ.owningRelation]
		e.mu.Unlock()
		if !valid {
			return CatalogConflict{}
		}
	}
	return nil
}
