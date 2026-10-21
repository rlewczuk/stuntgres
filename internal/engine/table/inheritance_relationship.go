package table

import (
	"context"
	"fmt"
	"math"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func RelationshipAction(action parser.AlterTableActionKind) bool {
	return action == parser.InheritTable || action == parser.NoInheritTable
}

func HasRelationshipActions(actions []parser.AlterTableAction) bool {
	return slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool { return RelationshipAction(a.Action) })
}

func RelationshipOnly(actions []parser.AlterTableAction) bool {
	return len(actions) != 0 && !slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool { return !RelationshipAction(a.Action) })
}

func RelationshipChildKind(child *catalog.Relation, action parser.AlterTableActionKind) error {
	if action == parser.InheritTable && child.OfType != (ids.ObjectAddress{}) {
		return sqlError("42809", "cannot change inheritance of typed table")
	}
	if child.Membership != nil {
		return sqlError("42809", "cannot change inheritance of a partition")
	}
	if action == parser.InheritTable && child.Partition != nil {
		return sqlError("42809", "cannot change inheritance of partitioned table")
	}
	label := "INHERIT"
	if action == parser.NoInheritTable {
		label = "NO INHERIT"
	}
	return analyze.TableKindError(child, label)
}

// LocalInheritance captures existing declarations; it does not cook or copy
// any physical fact, recipe, default, generation, key or row.
func LocalInheritance(child *catalog.Relation) *catalog.TableInheritance {
	g := &catalog.TableInheritance{Columns: make(map[int32]catalog.InheritedColumn, len(child.Columns)), NotNull: make(map[int32]catalog.InheritedConstraint, len(child.Constraints.NotNull)), Checks: make(map[string]catalog.InheritedConstraint, len(child.Constraints.Check))}
	for _, a := range child.Attributes {
		_, def := child.ColumnMeta.Defaults[a]
		_, gen := child.ColumnMeta.Generations[a]
		g.Columns[a] = catalog.InheritedColumn{Local: true, ExplicitDefault: def, ExplicitGeneration: gen}
	}
	for _, c := range child.Constraints.NotNull {
		g.NotNull[c.Attribute] = catalog.InheritedConstraint{Local: true}
	}
	for _, c := range child.Constraints.Check {
		g.Checks[c.Name] = catalog.InheritedConstraint{Local: true}
	}
	return g
}

func AttachInheritance(ctx context.Context, view catalog.Read, child, parent *catalog.Relation) (*catalog.Relation, error) {
	if err := RelationshipChildKind(child, parser.InheritTable); err != nil {
		return nil, err
	}
	if parent.Partition != nil {
		return nil, sqlError("42809", fmt.Sprintf(`cannot inherit from partitioned table "%s"`, parent.Name()))
	}
	if parent.Membership != nil {
		return nil, sqlError("42809", "cannot inherit from a partition")
	}
	if err := analyze.TableKindError(parent, "INHERIT"); err != nil {
		return nil, err
	}
	pns, cok := view.Namespace(parent.Namespace())
	cns, pok := view.Namespace(child.Namespace())
	if !cok || !pok {
		return nil, fmt.Errorf("stuntgres: missing inheritance namespace")
	}
	if pns.Temporary != (ids.SessionToken{}) && pns.Temporary != cns.Temporary {
		return nil, sqlError("42809", fmt.Sprintf(`cannot inherit from temporary relation "%s"`, parent.Name()))
	}
	if child.Inheritance.HasParent(parent.Address()) {
		return nil, sqlError("42P07", fmt.Sprintf(`relation "%s" would be inherited from more than once`, parent.Name()))
	}
	if parent.Address() == child.Address() || InheritedForeignKeyDescendant(view, parent, child.Address()) {
		return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: "circular inheritance not allowed", Detail: fmt.Sprintf(`"%s" is already a child of "%s".`, parent.Name(), child.Name())}
	}
	mapper := analyze.LikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
	for i, column := range parent.Columns {
		slot, ok := child.Index[column.Name]
		if !ok {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing column "%s"`, column.Name))
		}
		actual := child.Columns[slot]
		if column.Usage.Type != actual.Usage.Type || column.Usage.Typmod != actual.Usage.Typmod {
			return nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different type for column "%s"`, child.Name(), column.Name))
		}
		if column.Usage.Collation.ID != actual.Usage.Collation.ID {
			return nil, sqlError("42P21", fmt.Sprintf(`child table "%s" has different collation for column "%s"`, child.Name(), column.Name))
		}
		if !column.Nullable && actual.Nullable && slices.ContainsFunc(parent.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == parent.Attributes[i] && !c.NoInherit }) {
			return nil, sqlError("42804", fmt.Sprintf(`column "%s" in child table "%s" must be marked NOT NULL`, column.Name, child.Name()))
		}
		pg, parentGenerated := parent.ColumnMeta.Generations[parent.Attributes[i]]
		cg, childGenerated := child.ColumnMeta.Generations[child.Attributes[slot]]
		if parentGenerated != childGenerated {
			must := "must not"
			if parentGenerated {
				must = "must"
			}
			return nil, sqlError("42804", fmt.Sprintf(`column "%s" in child table %s be a generated column`, column.Name, must))
		}
		if parentGenerated && pg.Kind != cg.Kind {
			kind := func(k catalog.GenerationKind) string {
				if k == catalog.GenerationStored {
					return "STORED"
				}
				return "VIRTUAL"
			}
			return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: fmt.Sprintf(`column "%s" inherits from generated column of different kind`, column.Name), Detail: fmt.Sprintf("Parent column is %s, child column is %s.", kind(pg.Kind), kind(cg.Kind))}
		}
		mapper.TargetSlots[i] = slot
	}
	for _, required := range parent.Constraints.NotNull {
		if required.NoInherit {
			continue
		}
		attribute := child.Attributes[mapper.TargetSlots[parent.IndexForAttribute(required.Attribute)]]
		at := slices.IndexFunc(child.Constraints.NotNull, func(c catalog.NotNullConstraint) bool { return c.Attribute == attribute })
		if at < 0 {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing constraint "%s"`, required.Name))
		}
		actual := child.Constraints.NotNull[at]
		if actual.NoInherit {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on child table "%s"`, actual.Name, child.Name()))
		}
		if !required.NotValid && actual.NotValid {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on child table "%s"`, actual.Name, child.Name()))
		}
	}
	for _, required := range parent.Constraints.Check {
		if required.NoInherit {
			continue
		}
		at := slices.IndexFunc(child.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Name == required.Name })
		if at < 0 {
			return nil, sqlError("42804", fmt.Sprintf(`child table is missing constraint "%s"`, required.Name))
		}
		actual := child.Constraints.Check[at]
		mapped, err := stored.RemapLikeExpression(&mapper, required.Expression, child, view)
		if err != nil {
			return nil, err
		}
		same, err := sameInheritedCheck(ctx, mapped, actual.Expression, child, view)
		if err != nil {
			return nil, err
		}
		if !same {
			return nil, sqlError("42804", fmt.Sprintf(`child table "%s" has different definition for check constraint "%s"`, child.Name(), required.Name))
		}
		if actual.NoInherit {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on child table "%s"`, required.Name, child.Name()))
		}
		if !required.NotValid && actual.NotValid {
			return nil, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with NOT VALID constraint on child table "%s"`, required.Name, child.Name()))
		}
	}
	g := catalog.CloneInheritance(child.Inheritance)
	if g == nil {
		g = LocalInheritance(child)
	}
	var seqno int32
	if len(g.Parents) != 0 {
		seqno = g.Parents[len(g.Parents)-1].Seqno
	}
	if seqno == math.MaxInt32 {
		return nil, sqlError("54000", "too many inheritance parents")
	}
	g.Parents = append(g.Parents, catalog.InheritanceParent{Relation: parent.Address(), Seqno: seqno + 1})
	for i, attribute := range parent.Attributes {
		target := child.Attributes[mapper.TargetSlots[i]]
		p := g.Columns[target]
		if len(p.Origins) >= math.MaxInt16 {
			return nil, sqlError("54000", "too many inheritance parents")
		}
		p.Origins = append(slices.Clone(p.Origins), catalog.InheritanceOrigin{Parent: parent.Address(), Attribute: attribute})
		var err error
		p, err = RetainInheritedRecipes(view, child, target, p)
		if err != nil {
			return nil, err
		}
		g.Columns[target] = p
	}
	for _, c := range parent.Constraints.NotNull {
		if c.NoInherit {
			continue
		}
		target := child.Attributes[mapper.TargetSlots[parent.IndexForAttribute(c.Attribute)]]
		p := g.NotNull[target]
		if p.Count() >= math.MaxInt16 {
			return nil, sqlError("54000", "too many inheritance parents")
		}
		p.Origins = append(slices.Clone(p.Origins), catalog.InheritanceOrigin{Parent: parent.Address(), Attribute: c.Attribute, Constraint: c.OID})
		g.NotNull[target] = p
	}
	for _, c := range parent.Constraints.Check {
		if c.NoInherit {
			continue
		}
		p := g.Checks[c.Name]
		if p.Count() >= math.MaxInt16 {
			return nil, sqlError("54000", "too many inheritance parents")
		}
		p.Origins = append(slices.Clone(p.Origins), catalog.InheritanceOrigin{Parent: parent.Address(), Constraint: c.OID})
		g.Checks[c.Name] = p
	}
	next := *child
	next.Inheritance = g
	next.ObjectRevision++
	return &next, ctx.Err()
}

func DetachInheritance(view catalog.Read, child, parent *catalog.Relation, parentRef ids.ObjectRef) (*catalog.Relation, error) {
	if err := RelationshipChildKind(child, parser.NoInheritTable); err != nil {
		return nil, err
	}
	if parentRef.Domain() != view.Domain() || parentRef.Epoch != view.Epoch() || parentRef.Address != parent.Address() || view.Get(parent.Address()) != parent {
		return nil, ids.ErrCatalogReplaced
	}
	if !child.Inheritance.HasParent(parent.Address()) {
		return nil, sqlError("42P01", fmt.Sprintf(`relation "%s" is not a parent of relation "%s"`, parent.Name(), child.Name()))
	}
	g := catalog.CloneInheritance(child.Inheritance)
	g.Parents = slices.DeleteFunc(g.Parents, func(p catalog.InheritanceParent) bool { return p.Relation == parent.Address() })
	for attribute, p := range g.Columns {
		p.Origins = slices.DeleteFunc(slices.Clone(p.Origins), func(o catalog.InheritanceOrigin) bool { return o.Parent == parent.Address() })
		p.Local = p.Local || len(p.Origins) == 0
		g.Columns[attribute] = p
	}
	remove := func(p catalog.InheritedConstraint) catalog.InheritedConstraint {
		p.Origins = slices.DeleteFunc(slices.Clone(p.Origins), func(o catalog.InheritanceOrigin) bool { return o.Parent == parent.Address() })
		p.Retired = slices.Clone(p.Retired)
		for i := range p.Retired {
			if !p.Retired[i].Detached && p.Retired[i].Parent.Address == parent.Address() && stored.ReferenceEpochValid(view, child, p.Retired[i].Parent) {
				p.Retired[i].Detached = true
			}
		}
		p.Local = p.Local || p.Count() == 0
		return p
	}
	for a, p := range g.NotNull {
		g.NotNull[a] = remove(p)
	}
	for name, p := range g.Checks {
		g.Checks[name] = remove(p)
	}
	next := *child
	next.Inheritance = g
	if len(g.Parents) == 0 && !g.HasRetired() {
		next.Inheritance = nil
	}
	next.ObjectRevision++
	return &next, nil
}
