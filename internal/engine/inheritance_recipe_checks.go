package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Declarations use compatible typed NULL recipes only inside the dump/import
// dependency stage. Every actual physical recipe is restored before CHECK/data.
// This permits nonlocal children of parents whose captured recipes now differ.
func prepareGenerationPlaceholders(view catalog.Read, tables []dumpRelation) {
	hasSeed := false
	for i := range tables {
		if tables[i].needsGenerationPlaceholders() {
			hasSeed = true
			break
		}
	}
	if !hasSeed {
		return
	}
	byAddress := make(map[ids.ObjectAddress]*dumpRelation, len(tables))
	for i := range tables {
		byAddress[tables[i].descriptor.Address()] = &tables[i]
	}
	var include func(*dumpRelation)
	var component []*dumpRelation
	include = func(table *dumpRelation) {
		if table.recipePlaceholders {
			return
		}
		table.recipePlaceholders = true
		component = append(component, table)
		if inheritance := table.descriptor.Inheritance; inheritance != nil {
			for _, address := range inheritance.Parents {
				if parent := byAddress[address]; parent != nil {
					include(parent)
				}
			}
		}
		for _, child := range inheritanceChildren(view, table.descriptor.Address()) {
			if target := byAddress[child.Address()]; target != nil {
				include(target)
			}
		}
	}
	for i := range tables {
		if tables[i].recipePlaceholders || !tables[i].needsGenerationPlaceholders() {
			continue
		}
		component = nil
		include(&tables[i])
		leader := component[0].descriptor.Address()
		for _, table := range component {
			if table.descriptor.Address().ObjectID < leader.ObjectID {
				leader = table.descriptor.Address()
			}
		}
		for _, table := range component {
			table.recipeComponent = leader
		}
	}
	for i := range tables {
		if tables[i].recipePlaceholders {
			for _, check := range tables[i].descriptor.CheckConstraints {
				if !check.NotValid {
					if tables[i].deferredChecks == nil {
						tables[i].deferredChecks = make(map[string]bool)
					}
					tables[i].deferredChecks[check.Name] = true
				}
			}
		}
	}
}

// The actual dependency graph, not rows-first reconstruction, orders physical
// recipes and local CHECK definers. Recursive ADD rebuilds real diamond merges.
func inheritanceRecipeEvents(ctx context.Context, view catalog.Read, graph map[dumpEventKey][]dumpEventKey, declarations []dumpEventKey, tables []dumpRelation, nameReady func(ids.ObjectAddress) (dumpEventKey, bool, error)) error {
	selected := false
	for i := range tables {
		if tables[i].recipePlaceholders {
			selected = true
			break
		}
	}
	if !selected {
		return ctx.Err()
	}
	byOID := make(map[ids.OID]*dumpRelation, len(tables))
	for i := range tables {
		byOID[tables[i].descriptor.Address().ObjectID] = &tables[i]
	}
	components := make(map[ids.ObjectAddress][]dumpEventKey)
	var order []ids.ObjectAddress
	for _, declaration := range declarations {
		if table := byOID[declaration.oid]; table != nil && table.recipePlaceholders {
			key := table.recipeComponent
			if components[key] == nil {
				order = append(order, key)
			}
			components[key] = append(components[key], declaration)
		}
	}
	addProvider := func(event dumpEventKey) func(ids.ObjectAddress) error {
		return func(address ids.ObjectAddress) error {
			predecessor, exists, err := nameReady(address)
			if err == nil && exists {
				addDumpDependency(graph, event, predecessor)
			}
			return err
		}
	}
	for _, component := range order {
		var tail dumpEventKey
		prerequisites := components[component]
		for _, declaration := range prerequisites {
			table := byOID[declaration.oid]
			if len(table.descriptor.Generations) == 0 {
				continue
			}
			event := dumpEventKey{oid: declaration.oid, phase: dumpInheritanceRecipes}
			graph[event] = append([]dumpEventKey(nil), prerequisites...)
			add := addProvider(event)
			for _, definition := range table.descriptor.Generations {
				if err := regclassReplayReferences(view, definition.Expression, add); err != nil {
					return err
				}
				for _, dependency := range definition.Dependencies {
					if err := add(dependency.Address); err != nil {
						return err
					}
				}
			}
			if tail != (dumpEventKey{}) {
				addDumpDependency(graph, event, tail)
			}
			tail = event
		}
		for _, declaration := range prerequisites {
			table := byOID[declaration.oid]
			local := false
			for _, check := range table.descriptor.CheckConstraints {
				if table.deferredChecks[check.Name] && dumpCheckDeclaration(view, table.descriptor, check) {
					local = true
					break
				}
			}
			if !local {
				continue
			}
			event := dumpEventKey{oid: declaration.oid, phase: dumpInheritanceChecks}
			graph[event] = append([]dumpEventKey(nil), prerequisites...)
			add := addProvider(event)
			for _, check := range table.descriptor.CheckConstraints {
				if table.deferredChecks[check.Name] && dumpCheckDeclaration(view, table.descriptor, check) {
					if err := regclassReplayReferences(view, check.Expression, add); err != nil {
						return err
					}
					for _, dependency := range check.Dependencies {
						if err := add(dependency.Ref.Address); err != nil {
							return err
						}
					}
				}
			}
			if tail != (dumpEventKey{}) {
				addDumpDependency(graph, event, tail)
			}
			tail = event
		}
		if tail != (dumpEventKey{}) {
			for _, declaration := range prerequisites {
				addDumpDependency(graph, dumpEventKey{oid: declaration.oid, phase: dumpRows}, tail)
			}
		}
	}
	return ctx.Err()
}

func (t *dumpRelation) emitInheritedGenerations(ctx context.Context, view catalog.Read, emit func(string) error) error {
	prefix := "ALTER TABLE ONLY " + quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name) + " ALTER COLUMN "
	for i, column := range t.columns {
		if definition, exists := t.descriptor.Generations[t.attributes[i]]; exists {
			text, err := (scalarRenderer{owner: t.descriptor}).sql(ctx, view, definition.Expression)
			if err != nil {
				return err
			}
			if err := emit(prefix + quoteIdentifier(column.Name) + " SET EXPRESSION AS (" + text + ");\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *dumpRelation) emitInheritedChecks(ctx context.Context, view catalog.Read, emit func(string) error) error {
	for _, check := range orderedChecks(t.descriptor) {
		if !t.deferredChecks[check.Name] || !dumpCheckDeclaration(view, t.descriptor, check) {
			continue
		}
		text, err := (scalarRenderer{owner: t.descriptor, checkReplayPrecision: true}).sql(ctx, view, check.Expression)
		if err != nil {
			return err
		}
		suffix := ""
		if check.NoInherit {
			suffix = " NO INHERIT"
		}
		if err := emit("ALTER TABLE " + quoteIdentifier(t.schema) + "." + quoteIdentifier(t.name) + " ADD CONSTRAINT " + quoteIdentifier(check.Name) + " CHECK (" + text + ")" + suffix + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

type restoredGenerationRecipe struct {
	ref        ids.ObjectRef
	attribute  int32
	generation ids.OID
	usage      sqltype.Usage
	kind       catalog.GenerationKind
}
type restoredGenerationKey struct {
	relation  ids.ObjectAddress
	attribute int32
}

func (e *Engine) captureRestoredPlaceholders(created ids.ObjectRef, raw *parser.CreateTable, origins map[restoredGenerationKey]restoredGenerationRecipe) {
	view := e.CatalogRead()
	table, ok := view.Relation(created.Address)
	if !ok {
		return
	}
	for attribute, definition := range table.Generations {
		column := table.Columns[table.IndexForAttribute(attribute)].Name
		declared := false
		for _, element := range raw.Elements {
			if element.Column != nil && element.Column.Name == column {
				for _, constraint := range element.Column.Constraints {
					if constraint.Generation != nil {
						literal, ok := constraint.Generation.Expression.(parser.LiteralExpr)
						declared = ok && literal.Value.Kind == parser.NullValue
					}
				}
			}
		}
		if !declared && table.Inheritance != nil {
			inherited := false
			compatible := true
			for _, origin := range table.Inheritance.Columns[attribute].Origins {
				parent, ok := view.Relation(origin.Parent)
				if !ok {
					compatible = false
					break
				}
				generation, exists := parent.Generations[origin.Attribute]
				if !exists {
					continue
				}
				inherited = true
				record, exists := origins[restoredGenerationKey{origin.Parent, origin.Attribute}]
				if !exists || record.ref.Domain() != created.Domain() || record.ref.Epoch != created.Epoch || record.generation != generation.OID || record.kind != generation.Kind || record.usage != parent.Columns[parent.IndexForAttribute(origin.Attribute)].Usage {
					compatible = false
					break
				}
			}
			declared = inherited && compatible && !table.Inheritance.Columns[attribute].ExplicitGeneration
		}
		if declared {
			origins[restoredGenerationKey{created.Address, attribute}] = restoredGenerationRecipe{ref: created, attribute: attribute, generation: definition.OID, usage: table.Columns[table.IndexForAttribute(attribute)].Usage, kind: definition.Kind}
		}
	}
}

func (e *Engine) pairRestoredGeneration(op *Operation, raw *parser.AlterTable, origins map[restoredGenerationKey]restoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error {
		return sqlError("0A000", "restore accepts only paired generated-placeholder recipe definitions")
	}
	if !raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 || raw.Actions[0].Action != parser.SetExpression {
		return reject()
	}
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	e.mu.Lock()
	trusted := partitionConstraintReplayImport(e, op, auth)
	e.mu.Unlock()
	if !trusted {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	table, ok := auth.catalog.Relation(ref.Address)
	if !ok {
		return reject()
	}
	namespace, namespaceOK := auth.catalog.Namespace(table.Namespace())
	if !namespaceOK || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	slot, exists := table.Index[raw.Actions[0].Column.Text]
	if !exists {
		return reject()
	}
	attribute := table.Attributes[slot]
	record, exists := origins[restoredGenerationKey{ref.Address, attribute}]
	definition, generated := table.Generations[attribute]
	if !exists || !generated || record.ref != ref || record.attribute != attribute || record.generation != definition.OID || record.usage != table.Columns[slot].Usage || record.kind != definition.Kind {
		return reject()
	}
	if (relationProvider{}).Validate(auth.catalog, table) != nil {
		return reject()
	}
	return nil
}

func (e *Engine) captureRestoredGeneration(op *Operation, raw *parser.AlterTable) (restoredGenerationRecipe, error) {
	auth, err := op.Authorization()
	if err != nil {
		return restoredGenerationRecipe{}, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return restoredGenerationRecipe{}, err
	}
	table, ok := auth.catalog.Relation(ref.Address)
	if !ok {
		return restoredGenerationRecipe{}, ids.ErrCatalogReplaced
	}
	slot, ok := table.Index[raw.Actions[0].Column.Text]
	if !ok {
		return restoredGenerationRecipe{}, ids.ErrCatalogReplaced
	}
	attribute := table.Attributes[slot]
	generation := table.Generations[attribute]
	return restoredGenerationRecipe{ref: ref, attribute: attribute, generation: generation.OID, usage: table.Columns[slot].Usage, kind: generation.Kind}, nil
}

func (e *Engine) restoreInheritedCheck(op *Operation, raw *parser.AlterTable, repairs []restoredGenerationRecipe) error {
	if err := op.Context().Err(); err != nil {
		return err
	}
	reject := func() error {
		return sqlError("0A000", "restore accepts only paired inherited-recipe CHECK definitions")
	}
	if raw.Only || raw.IfExists || len(raw.Name.Parts) != 2 || len(raw.Actions) != 1 {
		return reject()
	}
	action := raw.Actions[0]
	if action.Action != parser.AddCheck || action.Check == nil || action.Check.NotValid || action.Check.Name.Text == "" || action.IfExists || action.IfNotExists {
		return reject()
	}
	if err := restoreScalar(action.Check.Expression, true); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.statementAuthorizationLocked(op, raw)
	if err != nil {
		return err
	}
	if !partitionConstraintReplayImport(e, op, auth) {
		return reject()
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return reject()
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return err
	}
	target, ok := auth.catalog.Relation(ref.Address)
	if !ok || target.RelationKind != catalog.TableRelation || target.Partition != nil || target.Membership != nil {
		return reject()
	}
	namespace, namespaceOK := auth.catalog.Namespace(target.Namespace())
	if !namespaceOK || namespace.Temporary != (ids.SessionToken{}) {
		return reject()
	}
	if validateInheritance(auth.catalog, target) != nil {
		return reject()
	}
	for _, repair := range repairs {
		if repair.ref.Domain() != ref.Domain() || repair.ref.Epoch != ref.Epoch {
			continue
		}
		table, ok := auth.catalog.Relation(repair.ref.Address)
		if !ok {
			continue
		}
		generation, ok := table.Generations[repair.attribute]
		slot := table.IndexForAttribute(repair.attribute)
		if !ok || slot < 0 || generation.OID != repair.generation || generation.Kind != repair.kind || table.Columns[slot].Usage != repair.usage {
			continue
		}
		if (relationProvider{}).Validate(auth.catalog, table) != nil {
			continue
		}
		if inheritedRecipeConnected(auth.catalog, target.Address(), table.Address()) {
			return nil
		}
	}
	return reject()
}

func inheritedRecipeConnected(view catalog.Read, target, start ids.ObjectAddress) bool {
	if target == start {
		return true
	}
	pending := []ids.ObjectAddress{start}
	seen := map[ids.ObjectAddress]bool{start: true}
	for len(pending) != 0 {
		address := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		table, ok := view.Relation(address)
		if !ok {
			continue
		}
		appendTarget := func(next ids.ObjectAddress) bool {
			if next == target {
				return true
			}
			if !seen[next] {
				seen[next] = true
				pending = append(pending, next)
			}
			return false
		}
		if table.Inheritance != nil {
			for _, parent := range table.Inheritance.Parents {
				if appendTarget(parent) {
					return true
				}
			}
		}
		for _, child := range inheritanceChildren(view, address) {
			if appendTarget(child.Address()) {
				return true
			}
		}
	}
	return false
}

func capturedGenerationReplayPlan(view catalog.Read) (map[ids.ObjectAddress]*dumpRelation, error) {
	hasGeneratedInheritance := false
	for _, descriptor := range view.Descriptors() {
		if table, ok := descriptor.(*catalog.Relation); ok && table.Inheritance != nil && len(table.Generations) != 0 {
			if namespace, ok := view.Namespace(table.Namespace()); ok && namespace.Temporary == (ids.SessionToken{}) {
				hasGeneratedInheritance = true
				break
			}
		}
	}
	if !hasGeneratedInheritance {
		return nil, nil
	}
	var tables []dumpRelation
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || table.RelationKind != catalog.TableRelation || table.Partition != nil || table.Membership != nil {
			continue
		}
		if namespace, ok := view.Namespace(table.Namespace()); !ok || namespace.Temporary != (ids.SessionToken{}) {
			continue
		}
		if !inheritanceParticipant(view, table) {
			continue
		}
		captured := dumpRelation{descriptor: table, columns: table.Columns, attributes: table.Attributes, defaults: table.Defaults}
		if err := captured.prepareInheritedRecipes(view); err != nil {
			return nil, err
		}
		tables = append(tables, captured)
	}
	prepareGenerationPlaceholders(view, tables)
	var selected map[ids.ObjectAddress]*dumpRelation
	for i := range tables {
		if tables[i].recipePlaceholders {
			if selected == nil {
				selected = make(map[ids.ObjectAddress]*dumpRelation)
			}
			selected[tables[i].descriptor.Address()] = &tables[i]
		}
	}
	return selected, nil
}

func (e *Engine) closeRestoredRecipesForData(op *Operation, raw *parser.Insert, repairs []restoredGenerationRecipe) ([]restoredGenerationRecipe, error) {
	auth, err := op.Authorization()
	if err != nil {
		return nil, err
	}
	ref, err := e.resolveRelationReference(auth, raw.Name, false)
	if err != nil {
		return nil, err
	}
	retained := repairs[:0]
	for _, repair := range repairs {
		if !inheritedRecipeConnected(auth.catalog, ref.Address, repair.ref.Address) {
			retained = append(retained, repair)
		}
	}
	return retained, nil
}

func (t *dumpRelation) needsGenerationPlaceholders() bool {
	if t.descriptor.Inheritance == nil {
		return false
	}
	for attribute, recipe := range t.recipes {
		if _, generated := t.descriptor.Generations[attribute]; generated && recipe.ExplicitGeneration && (!recipe.Local || !t.descriptor.Inheritance.Columns[attribute].ExplicitGeneration) {
			return true
		}
	}
	return false
}
