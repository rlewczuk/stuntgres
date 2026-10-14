package engine

import (
	"fmt"
	"maps"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func typedClosure(catalog CatalogRead, source ObjectAddress) map[ObjectAddress]Descriptor {
	result := make(map[ObjectAddress]Descriptor)
	var visit func(*relationDescriptor)
	visit = func(table *relationDescriptor) {
		if result[table.address] != nil {
			return
		}
		result[table.address] = table
		for _, child := range inheritanceChildren(catalog, table.address) {
			visit(child)
		}
	}
	for _, descriptor := range catalog.state.descriptors {
		if table, ok := descriptor.(*relationDescriptor); ok && table.ofType == source {
			visit(table)
		}
	}
	// Propagated own-row providers are distinct from the standalone source.
	// Capture their consumers even when a requested TYPE keeps the same Usage
	// and codec pointer. Consumers are observations, never propagation targets.
	rowTypes := make([]sqltype.ID, 1, len(result)+1)
	rowTypes[0] = sqltype.ID(source.ObjectID)
	for _, descriptor := range result {
		rowTypes = append(rowTypes, descriptor.(*relationDescriptor).rowType)
	}
	for _, descriptor := range catalog.state.descriptors {
		table, ok := descriptor.(*relationDescriptor)
		if !ok || !table.relationKind.stored() || result[table.address] != nil {
			continue
		}
		for _, column := range table.columns {
			typ := column.Usage.Type
			if !typ.IsDomain() && !typ.IsArray() && !typ.IsComposite() {
				continue
			}
			for _, rowType := range rowTypes {
				if containsRowType(typ, rowType) {
					result[table.address] = table
					break
				}
			}
			if result[table.address] != nil {
				break
			}
		}
	}
	return result
}

func typedObservationValid(stage *CatalogStage, current CatalogRead) bool {
	if stage.typedSource == (ObjectAddress{}) {
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
func typedPropagationOrder(catalog CatalogRead, source ObjectAddress) []*relationDescriptor {
	selected := make(map[ObjectAddress]*relationDescriptor)
	var selectTable func(*relationDescriptor)
	selectTable = func(table *relationDescriptor) {
		if selected[table.address] != nil {
			return
		}
		selected[table.address] = table
		for _, child := range inheritanceChildren(catalog, table.address) {
			selectTable(child)
		}
	}
	for _, descriptor := range catalog.state.descriptors {
		if table, ok := descriptor.(*relationDescriptor); ok && table.ofType == source {
			selectTable(table)
		}
	}
	var ordered []*relationDescriptor
	seen := make(map[ObjectAddress]bool, len(selected))
	var visit func(*relationDescriptor)
	visit = func(table *relationDescriptor) {
		if seen[table.address] {
			return
		}
		seen[table.address] = true
		if table.inheritance != nil {
			for _, parent := range table.inheritance.parents {
				if d := selected[parent]; d != nil {
					visit(d)
				}
			}
		}
		ordered = append(ordered, table)
	}
	addresses := make([]ObjectAddress, 0, len(selected))
	for address := range selected {
		addresses = append(addresses, address)
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
	for _, address := range addresses {
		visit(selected[address])
	}
	return ordered
}

func typedShapeActions(old, next, table *relationDescriptor, forced map[int32]bool) ([]typedColumnAction, map[int32]string) {
	var actions []typedColumnAction
	renames := make(map[int32]string)
	for i, attribute := range old.attributes {
		target := table.attributes[i]
		name := table.columns[i].Name
		slot := next.indexForAttribute(attribute)
		if slot < 0 {
			actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: name}, Mode: parser.DropCascade}})
			continue
		}
		if next.columns[slot].Name != name {
			renames[target] = next.columns[slot].Name
		}
		if forced[attribute] || next.columns[slot].Usage != table.columns[i].Usage {
			usage := next.columns[slot].Usage
			actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: name}}, usage: &usage, declaredDimensions: next.columns[slot].DeclaredDimensions})
		}
	}
	for i, attribute := range next.attributes {
		if old.hasAttribute(attribute) {
			continue
		}
		usage := next.columns[i].Usage
		actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: next.columns[i].Name}}, usage: &usage, declaredDimensions: next.columns[i].DeclaredDimensions})
	}
	return actions, renames
}

func typedInheritedActions(view CatalogRead, old *relationDescriptor, forced map[ObjectAddress]map[int32]bool) ([]typedColumnAction, map[int32]string, *tableInheritance, error) {
	graph := *old.inheritance
	graph.columns = maps.Clone(graph.columns)
	graph.checks = maps.Clone(graph.checks)
	graph.notNull = maps.Clone(graph.notNull)
	var actions []typedColumnAction
	renames := make(map[int32]string)
	for i, attribute := range old.attributes {
		provenance := graph.columns[attribute]
		var origins []inheritanceOrigin
		var desired *ColumnDescriptor
		forceType := false
		requestedDimensions := int16(0)
		for _, origin := range provenance.origins {
			parent := view.state.descriptors[origin.parent].(*relationDescriptor)
			slot := parent.indexForAttribute(origin.attribute)
			if slot < 0 {
				continue
			}
			forceType = forceType || forced[origin.parent][origin.attribute]
			if forced[origin.parent][origin.attribute] {
				requestedDimensions = parent.columns[slot].DeclaredDimensions
			}
			member := &parent.columns[slot]
			if desired != nil && (desired.Name != member.Name || desired.Usage != member.Usage) {
				return nil, nil, nil, sqlError("42P16", fmt.Sprintf(`cannot alter inherited column "%s"`, old.columns[i].Name))
			}
			desired = member
			origins = append(origins, origin)
		}
		provenance.origins = origins
		graph.columns[attribute] = provenance
		if desired == nil {
			if !provenance.local {
				actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: old.columns[i].Name}, Mode: parser.DropCascade}})
				delete(graph.columns, attribute)
			}
			continue
		}
		if desired.Name != old.columns[i].Name {
			renames[attribute] = desired.Name
		}
		if forceType || desired.Usage != old.columns[i].Usage {
			usage := desired.Usage
			dimensions := desired.DeclaredDimensions
			if forceType {
				dimensions = requestedDimensions
			}
			actions = append(actions, typedColumnAction{action: parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: old.columns[i].Name}}, usage: &usage, declaredDimensions: dimensions})
		}
	}
	// New attributes merge with an existing local/sibling attribute by live name,
	// while old attributes retain their explicit origin identity across rename.
	pending := make(map[string]int)
	for _, address := range graph.parents {
		parent := view.state.descriptors[address].(*relationDescriptor)
		for i, member := range parent.columns {
			origin := inheritanceOrigin{parent: address, attribute: parent.attributes[i]}
			found := false
			for _, provenance := range graph.columns {
				if slices.Contains(provenance.origins, origin) {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if slot, exists := old.index[member.Name]; exists {
				attribute := old.attributes[slot]
				if _, survives := graph.columns[attribute]; !survives {
					continue
				}
				if err := inheritanceColumnConflict(view, old.columns[slot], member, graph.columns[attribute].local); err != nil {
					return nil, nil, nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, old.name, member.Name))
				}
				provenance := graph.columns[attribute]
				provenance.origins = append(slices.Clone(provenance.origins), origin)
				graph.columns[attribute] = provenance
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

func finishTypedInheritance(view CatalogRead, next *relationDescriptor, graph *tableInheritance) {
	for i, attribute := range next.attributes {
		provenance, exists := graph.columns[attribute]
		if !exists {
			for _, address := range graph.parents {
				parent := view.state.descriptors[address].(*relationDescriptor)
				if slot, found := parent.index[next.columns[i].Name]; found {
					provenance.origins = append(provenance.origins, inheritanceOrigin{parent: address, attribute: parent.attributes[slot]})
				}
			}
		}
		if _, retained := next.defaults[attribute]; retained && !provenance.explicitDefault {
			supplied := false
			for _, origin := range provenance.origins {
				parent := view.state.descriptors[origin.parent].(*relationDescriptor)
				_, supplied = parent.defaults[origin.attribute]
				if supplied {
					break
				}
			}
			if !supplied {
				provenance.local, provenance.explicitDefault = true, true
			}
		}
		graph.columns[attribute] = provenance
	}
	for name, provenance := range graph.checks {
		if !slices.ContainsFunc(next.checkConstraints, func(c checkConstraint) bool { return c.Name == name }) {
			delete(graph.checks, name)
			continue
		}
		var origins []inheritanceOrigin
		for _, origin := range provenance.origins {
			parent := view.state.descriptors[origin.parent].(*relationDescriptor)
			for _, constraint := range parent.checkConstraints {
				if constraint.Name == name && !constraint.NoInherit {
					origin.constraint = constraint.OID
					origins = append(origins, origin)
					break
				}
			}
		}
		provenance.origins = origins
		// PG18 keeps the enforced child constraint after its last inherited
		// provider disappears. Native authority becomes independently replayable;
		// this does not claim PG's historical conislocal/coninhcount flags.
		if len(origins) == 0 {
			provenance.local = true
		}
		graph.checks[name] = provenance
	}
	for attribute, provenance := range graph.notNull {
		if !next.hasAttribute(attribute) {
			delete(graph.notNull, attribute)
			continue
		}
		var origins []inheritanceOrigin
		for _, origin := range provenance.origins {
			parent := view.state.descriptors[origin.parent].(*relationDescriptor)
			for _, constraint := range parent.notNullConstraints {
				if constraint.Attribute == origin.attribute {
					origin.constraint = constraint.OID
					origins = append(origins, origin)
					break
				}
			}
		}
		provenance.origins = origins
		if len(origins) == 0 {
			provenance.local = true
		}
		graph.notNull[attribute] = provenance
	}
	next.inheritance = graph
}

func typedSortedAddresses(tables []*relationDescriptor) []ObjectAddress {
	addresses := make([]ObjectAddress, len(tables))
	for i, table := range tables {
		addresses[i] = table.address
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
