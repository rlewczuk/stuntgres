package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
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
		for _, child := range catalog.InheritanceChildren(view, table.descriptor.Address()) {
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
			for _, check := range tables[i].descriptor.Constraints.Check {
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
func inheritanceRecipeEvents(ctx context.Context, view catalog.Read, graph dump.Graph, declarations []dump.Event, tables []dumpRelation, nameReady func(ids.ObjectAddress) (dump.Event, bool, error)) error {
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
	components := make(map[ids.ObjectAddress][]dump.Event)
	var order []ids.ObjectAddress
	for _, declaration := range declarations {
		if table := byOID[declaration.OID]; table != nil && table.recipePlaceholders {
			key := table.recipeComponent
			if components[key] == nil {
				order = append(order, key)
			}
			components[key] = append(components[key], declaration)
		}
	}
	addProvider := func(event dump.Event) func(ids.ObjectAddress) error {
		return func(address ids.ObjectAddress) error {
			predecessor, exists, err := nameReady(address)
			if err == nil && exists {
				graph.Add(event, predecessor)
			}
			return err
		}
	}
	for _, component := range order {
		var tail dump.Event
		prerequisites := components[component]
		for _, declaration := range prerequisites {
			table := byOID[declaration.OID]
			if len(table.descriptor.ColumnMeta.Generations) == 0 {
				continue
			}
			event := dump.Event{OID: declaration.OID, Phase: dump.InheritanceRecipes}
			graph[event] = append([]dump.Event(nil), prerequisites...)
			add := addProvider(event)
			for _, definition := range table.descriptor.ColumnMeta.Generations {
				if err := regobj.ClassReplayReferences(view, definition.Expression, add); err != nil {
					return err
				}
				for _, dependency := range definition.Dependencies {
					if err := add(dependency.Address); err != nil {
						return err
					}
				}
			}
			if tail != (dump.Event{}) {
				graph.Add(event, tail)
			}
			tail = event
		}
		for _, declaration := range prerequisites {
			table := byOID[declaration.OID]
			local := false
			for _, check := range table.descriptor.Constraints.Check {
				if table.deferredChecks[check.Name] && dumpCheckDeclaration(view, table.descriptor, check) {
					local = true
					break
				}
			}
			if !local {
				continue
			}
			event := dump.Event{OID: declaration.OID, Phase: dump.InheritanceChecks}
			graph[event] = append([]dump.Event(nil), prerequisites...)
			add := addProvider(event)
			for _, check := range table.descriptor.Constraints.Check {
				if table.deferredChecks[check.Name] && dumpCheckDeclaration(view, table.descriptor, check) {
					if err := regobj.ClassReplayReferences(view, check.Expression, add); err != nil {
						return err
					}
					for _, dependency := range check.Dependencies {
						if err := add(dependency.Ref.Address); err != nil {
							return err
						}
					}
				}
			}
			if tail != (dump.Event{}) {
				graph.Add(event, tail)
			}
			tail = event
		}
		if tail != (dump.Event{}) {
			for _, declaration := range prerequisites {
				graph.Add(dump.Event{OID: declaration.OID, Phase: dump.Rows}, tail)
			}
		}
	}
	return ctx.Err()
}

func (t *dumpRelation) emitInheritedGenerations(ctx context.Context, view catalog.Read, emit func(string) error) error {
	prefix := "ALTER TABLE ONLY " + catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name) + " ALTER COLUMN "
	for i, column := range t.columns {
		if definition, exists := t.descriptor.ColumnMeta.Generations[t.attributes[i]]; exists {
			text, err := (stored.ScalarRenderer{Owner: t.descriptor}).SQL(ctx, view, definition.Expression)
			if err != nil {
				return err
			}
			if err := emit(prefix + catalog.QuoteIdentifier(column.Name) + " SET EXPRESSION AS (" + text + ");\n"); err != nil {
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
		text, err := (stored.ScalarRenderer{Owner: t.descriptor, CheckReplayPrecision: true}).SQL(ctx, view, check.Expression)
		if err != nil {
			return err
		}
		suffix := ""
		if check.NoInherit {
			suffix = " NO INHERIT"
		}
		if err := emit("ALTER TABLE " + catalog.QuoteIdentifier(t.schema) + "." + catalog.QuoteIdentifier(t.name) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(check.Name) + " CHECK (" + text + ")" + suffix + ";\n"); err != nil {
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
		for _, child := range catalog.InheritanceChildren(view, address) {
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
		if table, ok := descriptor.(*catalog.Relation); ok && table.Inheritance != nil && len(table.ColumnMeta.Generations) != 0 {
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
		if !catalog.InheritanceParticipant(view, table) {
			continue
		}
		captured := dumpRelation{descriptor: table, columns: table.Columns, attributes: table.Attributes, defaults: table.ColumnMeta.Defaults}
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

func (t *dumpRelation) needsGenerationPlaceholders() bool {
	if t.descriptor.Inheritance == nil {
		return false
	}
	for attribute, recipe := range t.recipes {
		if _, generated := t.descriptor.ColumnMeta.Generations[attribute]; generated && recipe.ExplicitGeneration && (!recipe.Local || !t.descriptor.Inheritance.Columns[attribute].ExplicitGeneration) {
			return true
		}
	}
	return false
}
