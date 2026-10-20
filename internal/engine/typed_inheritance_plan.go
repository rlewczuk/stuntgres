package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func TypedShapeActions(old, next, table *catalog.Relation, forced map[int32]bool) ([]TypedColumnAction, map[int32]string) {
	var actions []TypedColumnAction
	renames := make(map[int32]string)
	for i, attribute := range old.Attributes {
		target := table.Attributes[i]
		name := table.Columns[i].Name
		slot := next.IndexForAttribute(attribute)
		if slot < 0 {
			actions = append(actions, TypedColumnAction{Action: parser.AlterTableAction{Action: parser.DropColumn, Column: parser.Identifier{Text: name}, Mode: parser.DropCascade}})
			continue
		}
		if next.Columns[slot].Name != name {
			renames[target] = next.Columns[slot].Name
		}
		if forced[attribute] || next.Columns[slot].Usage != table.Columns[i].Usage {
			usage := next.Columns[slot].Usage
			actions = append(actions, TypedColumnAction{Action: parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: name}}, Usage: &usage, DeclaredDimensions: next.Columns[slot].DeclaredDimensions})
		}
	}
	for i, attribute := range next.Attributes {
		if old.HasAttribute(attribute) {
			continue
		}
		usage := next.Columns[i].Usage
		actions = append(actions, TypedColumnAction{Action: parser.AlterTableAction{Action: parser.AddColumn, Definition: &parser.Column{Name: next.Columns[i].Name}}, Usage: &usage, DeclaredDimensions: next.Columns[i].DeclaredDimensions})
	}
	return actions, renames
}

func FinishTypedInheritance(view catalog.Read, next *catalog.Relation, graph *catalog.TableInheritance) {
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
		if !slices.ContainsFunc(next.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == name }) {
			delete(graph.Checks, name)
			continue
		}
		var origins []catalog.InheritanceOrigin
		for _, origin := range provenance.Origins {
			parent := view.MustRelation(origin.Parent)
			for _, constraint := range parent.Constraints.Check {
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
			for _, constraint := range parent.Constraints.NotNull {
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

func TypedSortedAddresses(tables []*catalog.Relation) []ids.ObjectAddress {
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
