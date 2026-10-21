package table

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
func prepareGenerationPlaceholders(view catalog.Read, tables []DumpRelation) {
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
	byAddress := make(map[ids.ObjectAddress]*DumpRelation, len(tables))
	for i := range tables {
		byAddress[tables[i].Descriptor.Address()] = &tables[i]
	}
	var include func(*DumpRelation)
	var component []*DumpRelation
	include = func(table *DumpRelation) {
		if table.RecipePlaceholders {
			return
		}
		table.RecipePlaceholders = true
		component = append(component, table)
		if inheritance := table.Descriptor.Inheritance; inheritance != nil {
			for _, edge := range inheritance.Parents {
				address := edge.Relation
				if parent := byAddress[address]; parent != nil {
					include(parent)
				}
			}
		}
		for _, child := range catalog.InheritanceChildren(view, table.Descriptor.Address()) {
			if target := byAddress[child.Address()]; target != nil {
				include(target)
			}
		}
	}
	for i := range tables {
		if tables[i].RecipePlaceholders || !tables[i].needsGenerationPlaceholders() {
			continue
		}
		component = nil
		include(&tables[i])
		leader := component[0].Descriptor.Address()
		for _, table := range component {
			if table.Descriptor.Address().ObjectID < leader.ObjectID {
				leader = table.Descriptor.Address()
			}
		}
		for _, table := range component {
			table.RecipeComponent = leader
		}
	}
	for i := range tables {
		if tables[i].RecipePlaceholders {
			for _, check := range tables[i].Descriptor.Constraints.Check {
				if !check.NotValid {
					if tables[i].DeferredChecks == nil {
						tables[i].DeferredChecks = make(map[string]bool)
					}
					tables[i].DeferredChecks[check.Name] = true
				}
			}
		}
	}
}

// The actual dependency graph, not rows-first reconstruction, orders physical
// recipes and local CHECK definers. Recursive ADD rebuilds real diamond merges.
func InheritanceRecipeEvents(ctx context.Context, view catalog.Read, graph dump.Graph, declarations []dump.Event, tables []DumpRelation, nameReady func(ids.ObjectAddress) (dump.Event, bool, error)) error {
	selected := false
	for i := range tables {
		if tables[i].RecipePlaceholders {
			selected = true
			break
		}
	}
	if !selected {
		return ctx.Err()
	}
	byOID := make(map[ids.OID]*DumpRelation, len(tables))
	for i := range tables {
		byOID[tables[i].Descriptor.Address().ObjectID] = &tables[i]
	}
	components := make(map[ids.ObjectAddress][]dump.Event)
	var order []ids.ObjectAddress
	for _, declaration := range declarations {
		if table := byOID[declaration.OID]; table != nil && table.RecipePlaceholders {
			key := table.RecipeComponent
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
			if len(table.Descriptor.ColumnMeta.Generations) == 0 {
				continue
			}
			event := dump.Event{OID: declaration.OID, Phase: dump.InheritanceRecipes}
			graph[event] = append([]dump.Event(nil), prerequisites...)
			add := addProvider(event)
			for _, definition := range table.Descriptor.ColumnMeta.Generations {
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
			for _, check := range table.Descriptor.Constraints.Check {
				if table.DeferredChecks[check.Name] && DumpCheckDeclaration(view, table.Descriptor, check) {
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
			for _, check := range table.Descriptor.Constraints.Check {
				if table.DeferredChecks[check.Name] && DumpCheckDeclaration(view, table.Descriptor, check) {
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

func (t *DumpRelation) EmitInheritedGenerations(ctx context.Context, view catalog.Read, emit func(string) error) error {
	prefix := "ALTER TABLE ONLY " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " ALTER COLUMN "
	for i, column := range t.Columns {
		if definition, exists := t.Descriptor.ColumnMeta.Generations[t.Attributes[i]]; exists {
			text, err := (stored.ScalarRenderer{Owner: t.Descriptor}).SQL(ctx, view, definition.Expression)
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

func (t *DumpRelation) EmitInheritedChecks(ctx context.Context, view catalog.Read, emit func(string) error) error {
	for _, check := range OrderedChecks(t.Descriptor) {
		if !t.DeferredChecks[check.Name] || !DumpCheckDeclaration(view, t.Descriptor, check) {
			continue
		}
		text, err := (stored.ScalarRenderer{Owner: t.Descriptor, CheckReplayPrecision: true}).SQL(ctx, view, check.Expression)
		if err != nil {
			return err
		}
		suffix := ""
		if check.NoInherit {
			suffix = " NO INHERIT"
		}
		if err := emit("ALTER TABLE " + catalog.QuoteIdentifier(t.Schema) + "." + catalog.QuoteIdentifier(t.Name) + " ADD CONSTRAINT " + catalog.QuoteIdentifier(check.Name) + " CHECK (" + text + ")" + suffix + ";\n"); err != nil {
			return err
		}
	}
	return nil
}

type RestoredGenerationRecipe struct {
	Ref        ids.ObjectRef
	Attribute  int32
	Generation ids.OID
	Usage      sqltype.Usage
	Kind       catalog.GenerationKind
}

type RestoredGenerationKey struct {
	Relation  ids.ObjectAddress
	Attribute int32
}

func InheritedRecipeConnected(view catalog.Read, target, start ids.ObjectAddress) bool {
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
			for _, edge := range table.Inheritance.Parents {
				parent := edge.Relation
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

func CapturedGenerationReplayPlan(view catalog.Read) (map[ids.ObjectAddress]*DumpRelation, error) {
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
	var tables []DumpRelation
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
		captured := DumpRelation{Descriptor: table, Columns: table.Columns, Attributes: table.Attributes, Defaults: table.ColumnMeta.Defaults}
		if err := captured.PrepareInheritedRecipes(view); err != nil {
			return nil, err
		}
		tables = append(tables, captured)
	}
	prepareGenerationPlaceholders(view, tables)
	var selected map[ids.ObjectAddress]*DumpRelation
	for i := range tables {
		if tables[i].RecipePlaceholders {
			if selected == nil {
				selected = make(map[ids.ObjectAddress]*DumpRelation)
			}
			selected[tables[i].Descriptor.Address()] = &tables[i]
		}
	}
	return selected, nil
}

func (t *DumpRelation) needsGenerationPlaceholders() bool {
	if t.Descriptor.Inheritance == nil {
		return false
	}
	for attribute, recipe := range t.Recipes {
		if _, generated := t.Descriptor.ColumnMeta.Generations[attribute]; generated && recipe.ExplicitGeneration && (!recipe.Local || !t.Descriptor.Inheritance.Columns[attribute].ExplicitGeneration) {
			return true
		}
	}
	return false
}
