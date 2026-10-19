package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func typedClosure(view catalog.Read, source ids.ObjectAddress) map[ids.ObjectAddress]catalog.Descriptor {
	result := make(map[ids.ObjectAddress]catalog.Descriptor)
	var visit func(*catalog.Relation)
	visit = func(table *catalog.Relation) {
		if result[table.Address()] != nil {
			return
		}
		result[table.Address()] = table
		for _, child := range inheritanceChildren(view, table.Address()) {
			visit(child)
		}
	}
	for _, descriptor := range view.Descriptors() {
		if table, ok := descriptor.(*catalog.Relation); ok && table.OfType == source {
			visit(table)
		}
	}
	// Propagated own-row providers are distinct from the standalone source.
	// Capture their consumers even when a requested TYPE keeps the same Usage
	// and codec pointer. Consumers are observations, never propagation targets.
	rowTypes := make([]sqltype.ID, 1, len(result)+1)
	rowTypes[0] = sqltype.ID(source.ObjectID)
	for _, descriptor := range result {
		rowTypes = append(rowTypes, descriptor.(*catalog.Relation).RowType)
	}
	for _, descriptor := range view.Descriptors() {
		table, ok := descriptor.(*catalog.Relation)
		if !ok || !table.RelationKind.Stored() || result[table.Address()] != nil {
			continue
		}
		for _, column := range table.Columns {
			typ := column.Usage.Type
			if !typ.IsDomain() && !typ.IsArray() && !typ.IsComposite() {
				continue
			}
			for _, rowType := range rowTypes {
				if containsRowType(typ, rowType) {
					result[table.Address()] = table
					break
				}
			}
			if result[table.Address()] != nil {
				break
			}
		}
	}
	return result
}

func typedObservationValid(stage *CatalogStage, current catalog.Read) bool {
	if stage.typedSource == (ids.ObjectAddress{}) {
		return true
	}
	observed := typedClosure(current, stage.typedSource)
	if len(observed) != len(stage.typedObserved) {
		return false
	}
	for address, descriptor := range stage.typedObserved {
		if observed[address] != descriptor {
			return false
		}
	}
	return true
}

// Parent-before-child order processes a diamond only after both incoming edges.
func typedPropagationOrder(view catalog.Read, source ids.ObjectAddress) []*catalog.Relation {
	selected := make(map[ids.ObjectAddress]*catalog.Relation)
	var selectTable func(*catalog.Relation)
	selectTable = func(table *catalog.Relation) {
		if selected[table.Address()] != nil {
			return
		}
		selected[table.Address()] = table
		for _, child := range inheritanceChildren(view, table.Address()) {
			selectTable(child)
		}
	}
	for _, descriptor := range view.Descriptors() {
		if table, ok := descriptor.(*catalog.Relation); ok && table.OfType == source {
			selectTable(table)
		}
	}
	var ordered []*catalog.Relation
	seen := make(map[ids.ObjectAddress]bool, len(selected))
	var visit func(*catalog.Relation)
	visit = func(table *catalog.Relation) {
		if seen[table.Address()] {
			return
		}
		seen[table.Address()] = true
		if table.Inheritance != nil {
			for _, parent := range table.Inheritance.Parents {
				if d := selected[parent]; d != nil {
					visit(d)
				}
			}
		}
		ordered = append(ordered, table)
	}
	addresses := make([]ids.ObjectAddress, 0, len(selected))
	for address := range selected {
		addresses = append(addresses, address)
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
	for _, address := range addresses {
		visit(selected[address])
	}
	return ordered
}

func typedShapeActions(old, next, table *catalog.Relation, forced map[int32]bool) ([]typedColumnAction, map[int32]string) {
	var actions []typedColumnAction
	renames := make(map[int32]string)
	for i, attribute := range old.Attributes {
		target := table.Attributes[i]
		name := table.Columns[i].Name
		slot := next.IndexForAttribute(attribute)
		if slot < 0 {
			actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: name}, Mode: parser.DropCascade}})
			continue
		}
		if next.Columns[slot].Name != name {
			renames[target] = next.Columns[slot].Name
		}
		if forced[attribute] || next.Columns[slot].Usage != table.Columns[i].Usage {
			usage := next.Columns[slot].Usage
			actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: name}}, usage: &usage, declaredDimensions: next.Columns[slot].DeclaredDimensions})
		}
	}
	for i, attribute := range next.Attributes {
		if old.HasAttribute(attribute) {
			continue
		}
		usage := next.Columns[i].Usage
		actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: next.Columns[i].Name}}, usage: &usage, declaredDimensions: next.Columns[i].DeclaredDimensions})
	}
	return actions, renames
}

func typedInheritedActions(view catalog.Read, old *catalog.Relation, forced map[ids.ObjectAddress]map[int32]bool) ([]typedColumnAction, map[int32]string, *catalog.TableInheritance, error) {
	graph := *old.Inheritance
	graph.Columns = maps.Clone(graph.Columns)
	graph.Checks = maps.Clone(graph.Checks)
	graph.NotNull = maps.Clone(graph.NotNull)
	var actions []typedColumnAction
	renames := make(map[int32]string)
	for i, attribute := range old.Attributes {
		provenance := graph.Columns[attribute]
		var origins []catalog.InheritanceOrigin
		var desired *catalog.Column
		forceType := false
		requestedDimensions := int16(0)
		for _, origin := range provenance.Origins {
			parent := view.MustRelation(origin.Parent)
			slot := parent.IndexForAttribute(origin.Attribute)
			if slot < 0 {
				continue
			}
			forceType = forceType || forced[origin.Parent][origin.Attribute]
			if forced[origin.Parent][origin.Attribute] {
				requestedDimensions = parent.Columns[slot].DeclaredDimensions
			}
			member := &parent.Columns[slot]
			if desired != nil && (desired.Name != member.Name || desired.Usage != member.Usage) {
				return nil, nil, nil, sqlError("42P16", fmt.Sprintf(`cannot alter inherited column "%s"`, old.Columns[i].Name))
			}
			desired = member
			origins = append(origins, origin)
		}
		provenance.Origins = origins
		graph.Columns[attribute] = provenance
		if desired == nil {
			if !provenance.Local {
				actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: old.Columns[i].Name}, Mode: parser.DropCascade}})
				delete(graph.Columns, attribute)
			}
			continue
		}
		if desired.Name != old.Columns[i].Name {
			renames[attribute] = desired.Name
		}
		if forceType || desired.Usage != old.Columns[i].Usage {
			usage := desired.Usage
			dimensions := desired.DeclaredDimensions
			if forceType {
				dimensions = requestedDimensions
			}
			actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: old.Columns[i].Name}}, usage: &usage, declaredDimensions: dimensions})
		}
	}
	// New attributes merge with an existing local/sibling attribute by live name,
	// while old attributes retain their explicit origin identity across rename.
	pending := make(map[string]int)
	for _, address := range graph.Parents {
		parent := view.MustRelation(address)
		for i, member := range parent.Columns {
			origin := catalog.InheritanceOrigin{Parent: address, Attribute: parent.Attributes[i]}
			found := false
			for _, provenance := range graph.Columns {
				if slices.Contains(provenance.Origins, origin) {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if slot, exists := old.Index[member.Name]; exists {
				attribute := old.Attributes[slot]
				if _, survives := graph.Columns[attribute]; !survives {
					continue
				}
				if err := inheritanceColumnConflict(view, old.Columns[slot], member, graph.Columns[attribute].Local); err != nil {
					return nil, nil, nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, old.Name(), member.Name))
				}
				provenance := graph.Columns[attribute]
				provenance.Origins = append(slices.Clone(provenance.Origins), origin)
				graph.Columns[attribute] = provenance
				continue
			}
			if index, exists := pending[member.Name]; exists {
				if *actions[index].usage != member.Usage {
					return nil, nil, nil, sqlError("42804", fmt.Sprintf(`inherited column "%s" has a type conflict`, member.Name))
				}
			} else {
				usage := member.Usage
				pending[member.Name] = len(actions)
				actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: member.Name}}, usage: &usage, declaredDimensions: member.DeclaredDimensions})
			}
		}
	}
	return actions, renames, &graph, nil
}

func finishTypedInheritance(view catalog.Read, next *catalog.Relation, graph *catalog.TableInheritance) {
	for i, attribute := range next.Attributes {
		provenance, exists := graph.Columns[attribute]
		if !exists {
			for _, address := range graph.Parents {
				parent := view.MustRelation(address)
				if slot, found := parent.Index[next.Columns[i].Name]; found {
					provenance.Origins = append(provenance.Origins, catalog.InheritanceOrigin{Parent: address, Attribute: parent.Attributes[slot]})
				}
			}
		}
		if _, retained := next.ColumnMeta.Defaults[attribute]; retained && !provenance.ExplicitDefault {
			supplied := false
			for _, origin := range provenance.Origins {
				parent := view.MustRelation(origin.Parent)
				_, supplied = parent.ColumnMeta.Defaults[origin.Attribute]
				if supplied {
					break
				}
			}
			if !supplied {
				provenance.Local, provenance.ExplicitDefault = true, true
			}
		}
		graph.Columns[attribute] = provenance
	}
	for name, provenance := range graph.Checks {
		if !slices.ContainsFunc(next.CheckConstraints, func(c catalog.CheckConstraint) bool { return c.Name == name }) {
			delete(graph.Checks, name)
			continue
		}
		var origins []catalog.InheritanceOrigin
		for _, origin := range provenance.Origins {
			parent := view.MustRelation(origin.Parent)
			for _, constraint := range parent.CheckConstraints {
				if constraint.Name == name && !constraint.NoInherit {
					origin.Constraint = constraint.OID
					origins = append(origins, origin)
					break
				}
			}
		}
		provenance.Origins = origins
		// PG18 keeps the enforced child constraint after its last inherited
		// provider disappears. Native authority becomes independently replayable;
		// this does not claim PG's historical conislocal/coninhcount flags.
		if len(origins) == 0 {
			provenance.Local = true
		}
		graph.Checks[name] = provenance
	}
	for attribute, provenance := range graph.NotNull {
		if !next.HasAttribute(attribute) {
			delete(graph.NotNull, attribute)
			continue
		}
		var origins []catalog.InheritanceOrigin
		for _, origin := range provenance.Origins {
			parent := view.MustRelation(origin.Parent)
			for _, constraint := range parent.NotNullConstraints {
				if constraint.Attribute == origin.Attribute {
					origin.Constraint = constraint.OID
					origins = append(origins, origin)
					break
				}
			}
		}
		provenance.Origins = origins
		if len(origins) == 0 {
			provenance.Local = true
		}
		graph.NotNull[attribute] = provenance
	}
	next.Inheritance = graph
}

func typedSortedAddresses(tables []*catalog.Relation) []ids.ObjectAddress {
	addresses := make([]ids.ObjectAddress, len(tables))
	for i, table := range tables {
		addresses[i] = table.Address()
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
	return addresses
}

func typedTransformAttributes(transforms []columnTransformation) map[int32]bool {
	if len(transforms) == 0 {
		return nil
	}
	attributes := make(map[int32]bool, len(transforms))
	for _, transform := range transforms {
		attributes[transform.attribute] = true
	}
	return attributes
}
