package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
)

// OF names are catalog object names, not SQL declaration aliases.
func (e *Engine) namedType(auth AuthorizationView, name parser.ObjectName) (*catalog.Type, error) {
	path, err := e.lookupNamespaces(auth, name, analyze.TypeLookup)
	if err != nil {
		return nil, err
	}
	for i := range path.Count() {
		found := auth.Catalog.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: path.At(i), Name: name.Parts[len(name.Parts)-1].Text})
		if len(found) == 1 {
			return auth.Catalog.MustType(found[0]), nil
		}
	}
	return nil, expr.ErrorAt("42704", fmt.Sprintf(`type "%s" does not exist`, objectNameText(name)), name.Parts[0].Pos)
}

func bindTypedColumns(host analyzeHost, auth AuthorizationView, s *parser.CreateTable) ([]catalog.Column, []createColumnDeclaration, *catalog.Type, error) {
	typ, err := host.NamedType(auth, *s.OfType)
	if err != nil {
		return nil, nil, nil, err
	}
	path, err := host.ReferencePath(auth)
	if err != nil {
		return nil, nil, nil, err
	}
	source, err := typedef.StandaloneComposite(auth.Catalog, typ, path)
	if err == nil {
		err = auth.CanUse(host.CatalogOwner().Domain.Ref(auth.Catalog.Epoch(), typ.Address()))
	}
	if err != nil {
		return nil, nil, nil, err
	}
	columns := make([]catalog.Column, len(source.Columns))
	declarations := make([]createColumnDeclaration, len(columns))
	for i, member := range source.Columns {
		if isSystemColumn(member.Name) {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column name "%s" conflicts with a system column name`, member.Name))
		}
		columns[i] = catalog.Column{Name: member.Name, Usage: member.Usage, Nullable: true, NullabilityKnown: true}
	}
	seen := make(map[string]bool, len(s.Elements))
	for _, element := range s.Elements {
		column := element.Column
		if column == nil {
			continue
		}
		slot, exists := source.Index[column.Name]
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
		if declaration.Identity != nil {
			return nil, nil, nil, sqlError("0A000", "identity columns are not supported on typed tables")
		}
		if declaration.Generation != nil {
			return nil, nil, nil, sqlError("0A000", "generated columns are not supported on typed tables")
		}
		declarations[slot] = declaration
	}
	return columns, declarations, typ, nil
}

func (e *Engine) alterTableOfLocked(scope *StatementScope, op *Operation, auth AuthorizationView, s *parser.AlterTableOf, count int) (Result, error) {
	fail := func(err error) (Result, error) { e.mu.Unlock(); return Result{}, err }
	if count != 0 {
		return fail(fmt.Errorf("stuntgres: got %d arguments, want 0", count))
	}
	old, ok := scope.lockedDescriptor.(*catalog.Relation)
	if !ok {
		return fail(ids.ErrCatalogReplaced)
	}
	if err := tableKind(old, "OF"); err != nil {
		return fail(err)
	}
	next := *old
	if s.Type == nil {
		if old.OfType == (ids.ObjectAddress{}) {
			return fail(sqlError("42809", fmt.Sprintf(`"%s" is not a typed table`, old.Name())))
		}
		next.OfType = ids.ObjectAddress{}
	} else {
		typ, err := e.namedType(auth, *s.Type)
		if err != nil {
			return fail(err)
		}
		path, err := e.referencePath(auth)
		if err != nil {
			return fail(err)
		}
		source, err := typedef.StandaloneComposite(auth.Catalog, typ, path)
		if err == nil {
			// Like CREATE OF, attachment needs source USAGE (PostgreSQL 18.6,
			// CVE-2026-6470), checked before inheritance and shape.
			err = auth.CanUse(e.catalog.Domain.Ref(auth.Catalog.Epoch(), typ.Address()))
		}
		if err != nil {
			return fail(err)
		}
		if old.Inheritance != nil && len(old.Inheritance.Parents) != 0 {
			return fail(sqlError("42809", "typed tables cannot inherit"))
		}
		if err := typedef.TypedShape(old, source); err != nil {
			return fail(err)
		}
		next.OfType = typ.Address()
	}
	if old.Revision() == ids.DefinitionRevision(^uint64(0)) {
		return fail(fmt.Errorf("stuntgres: definition revision exhausted"))
	}
	next.ObjectRevision++
	stage := e.BeginCatalogStage(auth.Catalog)
	stage.authorizeLocked(auth, op.session)
	if next.OfType != (ids.ObjectAddress{}) {
		stage.domainBases = append(stage.domainBases, auth.Catalog.Get(next.OfType))
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
		_, err = e.publishCatalogOperation(op, scope, prepared, PublicationToken{Epoch: stage.Base.Epoch()})
	}
	if err == nil {
		scope.completed = true
	}
	return Result{}, err
}

func (scope *StatementScope) acquireTypedSources(stage *CatalogStage, drop bool) error {
	var sources map[ids.ObjectAddress]bool
	if drop {
		if len(stage.domainTargets) != 0 {
			sources = make(map[ids.ObjectAddress]bool, len(stage.domainTargets))
		}
		for _, source := range stage.domainTargets {
			sources[source] = true
		}
	} else {
		for _, descriptor := range stage.Descriptors() {
			if table, ok := descriptor.(*catalog.Relation); ok && table.OfType != (ids.ObjectAddress{}) {
				if sources == nil {
					sources = make(map[ids.ObjectAddress]bool)
				}
				sources[table.OfType] = true
			}
		}
	}
	if len(sources) == 0 {
		return nil
	}
	addresses := make([]ids.ObjectAddress, 0, len(sources))
	for source := range sources {
		addresses = append(addresses, source)
	}
	slices.SortFunc(addresses, func(a, b ids.ObjectAddress) int {
		if a.ObjectID < b.ObjectID {
			return -1
		}
		if a.ObjectID > b.ObjectID {
			return 1
		}
		return 0
	})
	mode := lock.AccessShare
	if drop {
		mode = lock.AccessExclusive
	}
	for _, source := range addresses {
		typ, ok := stage.Base.Type(source)
		if !ok {
			continue
		}
		if err := scope.acquireSequence(stage.owner.catalog.Domain.Ref(stage.Base.Epoch(), typ.OwningRelation), mode, true); err != nil {
			return err
		}
		// A wait may admit a different definition at the same address. Keep
		// the copied shape tied to the exact captured reciprocal providers;
		// neither a new generation nor a same-name source can be adopted.
		e := stage.owner
		e.mu.Lock()
		valid := e.catalog.state.Get(source) == typ && e.catalog.state.Get(typ.OwningRelation) == stage.Base.Get(typ.OwningRelation)
		e.mu.Unlock()
		if !valid {
			return catalog.Conflict{}
		}
	}
	return nil
}
