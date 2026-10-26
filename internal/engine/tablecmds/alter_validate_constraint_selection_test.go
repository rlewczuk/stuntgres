package tablecmds_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestAlterValidateConstraintSelection(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(a INT PRIMARY KEY)")
	sessionSQL(t, s, "CREATE TABLE t(a INT,z INT,b INT)")
	sessionSQL(t, s, "ALTER TABLE t DROP z")
	sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT ck CHECK(a>0) NOT VALID, ADD CONSTRAINT nn NOT NULL b NOT VALID, ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID")
	old := alterConstraintDescriptor(t, e, "t")
	before := *old
	for _, name := range []string{"ck", "nn", "fk"} {
		next, selected, err := table.ValidateConstraintCandidate(old, name)
		if err != nil || next == old || !selected.Invalid() || selected.OID == 0 {
			t.Fatal(name, selected, err)
		}
		if !reflect.DeepEqual(old, &before) || next.Revision() != old.Revision() || !slices.Equal(next.Attributes, old.Attributes) || next.Inheritance != old.Inheritance {
			t.Fatal("selection changed original or identity", name)
		}
		current, err := table.SelectAlterConstraintValidation(next, name)
		if err != nil || current.Invalid() || current.OID != selected.OID {
			t.Fatal("promotion lost identity", current, err)
		}
		again, _, err := table.ValidateConstraintCandidate(next, name)
		if err != nil || again != next {
			t.Fatal("valid selection is not pointer no-op", name, err)
		}
		for _, other := range []string{"ck", "nn", "fk"} {
			v, err := table.SelectAlterConstraintValidation(next, other)
			if err != nil || v.Invalid() != (other != name) {
				t.Fatal("unselected flag changed", other, v, err)
			}
		}
		if name == "ck" && (&next.Constraints.Check[0] == &old.Constraints.Check[0] || &next.Constraints.NotNull[0] != &old.Constraints.NotNull[0]) {
			t.Fatal("incorrect touched slice ownership")
		}
		if name == "nn" && next.Constraints.NotNull[0].Attribute != 3 {
			t.Fatal("lost sparse attribute")
		}
	}
	for _, tc := range []struct{ name, code, message, detail string }{
		{"missing", "42704", `constraint "missing" of relation "t" does not exist`, ""},
	} {
		_, _, err := table.ValidateConstraintCandidate(old, tc.name)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != tc.code || got.Message != tc.message || got.Detail != tc.detail || got.Schema != "" || got.Table != "" || got.Constraint != "" {
			t.Fatal(got, err)
		}
	}
	p := alterConstraintDescriptor(t, e, "p")
	_, _, err := table.ValidateConstraintCandidate(p, p.Constraints.Unique[0].Name)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42809" || diagnostic.Detail != "This operation is not supported for this type of constraint." {
		t.Fatal(err)
	}
	promoted, selected, err := table.ValidateConstraintCandidate(old, "ck")
	if err != nil {
		t.Fatal(err)
	}
	validator, scan := table.PlanAlterScan(old, promoted, "public", false, true, nil, nil, []catalog.CheckConstraint{*selected.Check}, nil, nil, nil)
	if !scan || validator.Checks == nil || len(validator.Checks.Constraints.Check) != 1 || len(validator.NotNull) != 0 {
		t.Fatal("selection scanned unrelated NN", validator)
	}
	if _, err := table.ResolveAlterChecks(promoted, []catalog.CheckConstraint{{Name: "ck", OID: selected.OID + 1, Ordinal: selected.Check.Ordinal}}); err == nil {
		t.Fatal("accepted stale selected OID")
	}

	// These states are intentionally structural: released public inheritance
	// constructors reject a valid parent merged with an invalid enforced child.
	sessionSQL(t, s, "CREATE TABLE root(a INT)")
	sessionSQL(t, s, "CREATE TABLE middle() INHERITS(root)")
	sessionSQL(t, s, "CREATE TABLE leaf() INHERITS(middle)")
	sessionSQL(t, s, "ALTER TABLE root ADD CONSTRAINT inherited CHECK(a>0) NOT VALID")
	root := alterConstraintDescriptor(t, e, "root")
	middle := alterConstraintDescriptor(t, e, "middle")
	leaf := alterConstraintDescriptor(t, e, "leaf")
	a := parser.AlterTableAction{Action: parser.ValidateConstraint, Constraint: parser.Identifier{Text: "inherited"}}
	validRoot, _, err := table.ValidateConstraintCandidate(root, "inherited")
	if err != nil {
		t.Fatal(err)
	}
	if table.InheritanceRecursiveAction(validRoot, a, false) {
		t.Fatal("valid root discovered invalid descendants")
	}
	noop, _, err := table.ValidateConstraintCandidate(validRoot, "inherited")
	if err != nil || noop != validRoot || !middle.Constraints.Check[0].NotValid {
		t.Fatal("structural root no-op mutated descendant")
	}
	plan := table.InheritanceAlterPlan{Root: root, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{}}
	plan.RecordAction(root, validRoot, a)
	incoming := table.InheritanceParentActions(&plan, middle)
	if len(incoming) != 1 {
		t.Fatal("lost initial recursive intent", incoming)
	}
	validMiddle, _, err := table.ValidateConstraintCandidate(middle, "inherited")
	if err != nil {
		t.Fatal(err)
	}
	plan.Descendant = true
	plan.RecordAction(validMiddle, validMiddle, incoming[0])
	if relayed := table.InheritanceParentActions(&plan, leaf); len(relayed) != 1 || !leaf.Constraints.Check[0].NotValid {
		t.Fatal("valid intermediate stopped descendant intent", relayed)
	}
	noopPlan, preparatory, err := table.PlanInheritanceAlter(e.CatalogRead(), validRoot, &parser.AlterTable{Actions: []parser.AlterTableAction{a}})
	if err != nil || preparatory || len(noopPlan.Targets) != 1 || noopPlan.Targets[0] != validRoot {
		t.Fatal("valid root discovered topology", noopPlan.Targets, err)
	}
	sessionSQL(t, s, "ALTER TABLE root ADD CONSTRAINT root_nn NOT NULL a NOT VALID")
	// Clone authentic inherited NN facts into structurally differing child names
	// before constructing the publicly forbidden valid-parent/invalid-child state.
	nnRoot, nnMiddle, nnLeaf := *alterConstraintDescriptor(t, e, "root"), *alterConstraintDescriptor(t, e, "middle"), *alterConstraintDescriptor(t, e, "leaf")
	nnRoot.Constraints.NotNull = slices.Clone(nnRoot.Constraints.NotNull)
	nnMiddle.Constraints.NotNull = slices.Clone(nnMiddle.Constraints.NotNull)
	nnLeaf.Constraints.NotNull = slices.Clone(nnLeaf.Constraints.NotNull)
	nnMiddle.Constraints.NotNull[0].Name = "middle_nn"
	nnLeaf.Constraints.NotNull[0].Name = "leaf_nn"
	nnRoot.Constraints.NotNull[0].NoInherit = false
	nnMiddle.Constraints.NotNull[0].NoInherit = false
	nnLeaf.Constraints.NotNull[0].NoInherit = false
	nnAction := parser.AlterTableAction{Action: parser.ValidateConstraint, Constraint: parser.Identifier{Text: "root_nn"}}
	validNNRoot, _, err := table.ValidateConstraintCandidate(&nnRoot, "root_nn")
	if err != nil {
		t.Fatal(err)
	}
	nnNoop, _, err := table.ValidateConstraintCandidate(validNNRoot, "root_nn")
	if err != nil || nnNoop != validNNRoot || table.InheritanceRecursiveAction(validNNRoot, nnAction, false) || !nnMiddle.Constraints.NotNull[0].NotValid {
		t.Fatal("NN structural root no-op", err)
	}
	nnPlan := table.InheritanceAlterPlan{Root: &nnRoot, Events: map[ids.ObjectAddress][]table.InheritanceAlterEvent{}}
	nnPlan.RecordAction(&nnRoot, validNNRoot, nnAction)
	nnIncoming := table.InheritanceParentActions(&nnPlan, &nnMiddle)
	if len(nnIncoming) != 1 {
		t.Fatal(nnIncoming)
	}
	nnMapped, err := table.ValidationChildAction(&nnMiddle, nnIncoming[0])
	if err != nil || nnMapped.Constraint.Text != "middle_nn" {
		t.Fatal(nnMapped, err)
	}
	validNNMiddle, _, err := table.ValidateConstraintCandidate(&nnMiddle, nnMapped.Constraint.Text)
	if err != nil {
		t.Fatal(err)
	}
	nnPlan.Descendant = true
	nnPlan.RecordAction(validNNMiddle, validNNMiddle, nnMapped)
	if relayed := table.InheritanceParentActions(&nnPlan, &nnLeaf); len(relayed) != 1 || !nnLeaf.Constraints.NotNull[0].NotValid {
		t.Fatal("valid NN intermediate stopped relay", relayed)
	}
	// NN mapping uses the child's own constraint name and stable logical column.
	nn := parser.AlterTableAction{Action: parser.ValidateConstraint, Constraint: parser.Identifier{Text: "parent_name"}, Column: parser.Identifier{Text: "b"}}
	mapped, err := table.ValidationChildAction(old, nn)
	if err != nil || mapped.Constraint.Text != "nn" {
		t.Fatal(mapped, err)
	}
	if _, err := table.ValidationChildAction(old, parser.AlterTableAction{Column: parser.Identifier{Text: "absent"}}); err == nil {
		t.Fatal("missing NN corruption accepted")
	}
}
