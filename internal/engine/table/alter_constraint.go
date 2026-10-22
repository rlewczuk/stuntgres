package table

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

type PreparedAddedChecks struct {
	Old, Next *catalog.Relation
	Raw       []*parser.CheckConstraint
	Scans     []catalog.CheckConstraint
	Ordinal   int
}

// Fresh PK preparation observes statement-start NN validity, before DROP/SET
// candidates can remove or validate it. New keys remain with the later binder.
func VerifyAlterPrimaryPrerequisites(parent *catalog.Relation, actions []parser.AlterTableAction) error {
	for _, action := range actions {
		if action.Action != parser.AddUnique || !action.Unique.Primary {
			continue
		}
		for _, column := range action.Unique.Columns {
			if slot, exists := parent.Index[column.Text]; exists {
				if err := VerifyNotNullPKCompatible(parent, parent.Attributes[slot:slot+1]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func VerifyNotNullPKCompatible(parent *catalog.Relation, attributes []int32) error {
	for _, n := range parent.Constraints.NotNull {
		if (!n.NotValid && !n.NoInherit) || !slices.Contains(attributes, n.Attribute) {
			continue
		}
		column := parent.Columns[slices.Index(parent.Attributes, n.Attribute)].Name
		d := sqlError("55000", fmt.Sprintf(`cannot create primary key on column "%s"`, column)).(*sqlstate.Error)
		modifier := "NOT VALID"
		d.Hint = "You might need to validate it using ALTER TABLE ... VALIDATE CONSTRAINT."
		if n.NoInherit {
			modifier = "NO INHERIT"
			d.Hint = "You might need to make the existing constraint inheritable using ALTER TABLE ... ALTER CONSTRAINT ... INHERIT."
		}
		d.Detail = fmt.Sprintf(`The constraint "%s" on column "%s" of table "%s", marked %s, is incompatible with a primary key.`, n.Name, column, parent.Name(), modifier)
		return d
	}
	return nil
}

// Definition cooking is independent of stage ownership. Admission retains this
// exact result; the sole physical pipeline installs it and scans rows once.
// PrepareAddedChecks cooks the CHECKs of ADD CONSTRAINT on parent; plan is the
// recursing ALTER's record, nil when not recursing.
func PrepareAddedChecks(h AlterHost, plan *InheritanceAlterPlan, auth ddl.Authority, parent *catalog.Relation, raw []*parser.CheckConstraint) (PreparedAddedChecks, error) {
	prepared := PreparedAddedChecks{Old: parent, Next: parent, Raw: raw}
	if len(raw) == 0 {
		return prepared, nil
	}
	if plan != nil && plan.Partition && plan.Descendant {
		return preparePartitionChecks(h, plan, auth, parent, raw)
	}
	next := *parent
	next.Constraints.Check = slices.Clone(parent.Constraints.Check)
	// The ordinary binder writes locality into this graph. A candidate must not
	// mutate captured descriptors, including when a later descendant vetoes it.
	next.Inheritance = catalog.CloneInheritance(parent.Inheritance)
	ordinal := 0
	for _, c := range parent.Constraints.Check {
		if c.Ordinal >= ordinal {
			ordinal = c.Ordinal + 1
		}
	}
	var inherited map[string]catalog.CheckConstraint
	if plan != nil {
		for _, a := range raw {
			for _, c := range parent.Constraints.Check {
				if c.Name != a.Name.Text {
					continue
				}
				merge := plan.Descendant
				if c.PartitionOrigin != nil {
					merge = merge || c.PartitionOrigin.Count() != 0
				}
				if parent.Inheritance != nil {
					provenance := parent.Inheritance.Checks[c.Name]
					merge = merge || !provenance.Local && provenance.Count() != 0
				}
				if !merge {
					continue
				}
				if c.NoInherit {
					return prepared, sqlError("42P17", fmt.Sprintf(`constraint "%s" conflicts with non-inherited constraint on relation "%s"`, c.Name, parent.Name()))
				}
				if inherited == nil {
					inherited = make(map[string]catalog.CheckConstraint)
				}
				inherited[c.Name] = c
			}
		}
	}
	if err := BindChecks(h, auth, &next, raw, h.Input(), true, inherited); err != nil {
		return prepared, err
	}
	if err := plan.OnlyCreatedConstraint(parent, &next); err != nil {
		return prepared, err
	}
	if plan != nil && plan.Descendant && next.Inheritance != nil {
		for _, check := range next.Constraints.Check {
			if !slices.ContainsFunc(raw, func(declaration *parser.CheckConstraint) bool { return declaration.Name.Text == check.Name }) {
				continue
			}
			provenance := next.Inheritance.Checks[check.Name]
			before := parent.Inheritance.Checks[check.Name]
			retained := slices.ContainsFunc(parent.Constraints.Check, func(old catalog.CheckConstraint) bool { return old.Name == check.Name })
			provenance.Local = retained && before.Local
			next.Inheritance.Checks[check.Name] = provenance
		}
	}
	if plan != nil && !plan.Descendant && next.Inheritance != nil && len(inherited) != 0 {
		graph := catalog.CloneInheritance(next.Inheritance)
		for name := range inherited {
			provenance := graph.Checks[name]
			provenance.Local = true
			graph.Checks[name] = provenance
		}
		next.Inheritance = graph
	}
	for _, c := range OrderedChecks(&next) {
		if c.Ordinal >= ordinal && !c.NotValid {
			prepared.Scans = append(prepared.Scans, c)
		}
	}
	prepared.Next = &next
	prepared.Ordinal = ordinal
	return prepared, nil
}

// StageAddedUnique stages ADD UNIQUE or PRIMARY KEY on parent; a raw PRIMARY
// KEY first adds the NOT NULLs it implies.
func StageAddedUnique(h Host, op *session.Operation, parent *catalog.Relation, raw *parser.UniqueConstraint) (*catalog.Relation, error) {
	if raw.Primary {
		for _, c := range parent.Constraints.Unique {
			if c.Primary {
				return nil, sqlError("42P16", fmt.Sprintf(`multiple primary keys for table "%s" are not allowed`, parent.Name()))
			}
		}
	}
	definition, err := analyze.BindUniqueAttributes(raw, nil, parent.Index, parent.Attributes)
	if err != nil {
		return nil, AlterCookingError(err)
	}
	if raw.Primary {
		if err := VerifyNotNullPKCompatible(parent, definition.Attributes); err != nil {
			return nil, err
		}
		for _, attribute := range definition.Attributes {
			// Raw PRIMARY reuses an existing NN tuple without direct-ADD locality
			// changes. USING INDEX's separate implied ADD NN remains unconditional.
			if slices.ContainsFunc(parent.Constraints.NotNull, func(constraint catalog.NotNullConstraint) bool { return constraint.Attribute == attribute }) {
				continue
			}
			column := parent.Columns[slices.Index(parent.Attributes, attribute)].Name
			parent, err = NotNullCandidate(op, h.StageRead(), parent, parser.AlterTableAction{Action: parser.AddNotNull, Column: parser.Identifier{Text: column}}, NotNullDirect)
			if err != nil {
				return nil, err
			}
		}
	}
	next := *parent
	err = index.StageUniqueDefinitions(h, &next, []analyze.UniqueConstraint{{UniqueConstraint: definition}}, true)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

// StageAddedForeignKey stages ADD FOREIGN KEY on parent against the captured
// target and supporting index.
func StageAddedForeignKey(h Host, auth ddl.Authority, parent *catalog.Relation, raw *parser.ForeignKeyConstraint, target, supporting ids.ObjectAddress) (*catalog.Relation, error) {
	next := *parent
	next.Constraints.ForeignKeys = slices.Clone(parent.Constraints.ForeignKeys)
	if err := h.Stage().Replace(&next); err != nil {
		return nil, err
	}
	err := StageForeignKey(h, auth, &next, *raw, target, supporting)
	if err != nil {
		return nil, AlterCookingError(err)
	}
	return &next, nil
}
