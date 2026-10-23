package proc

import (
	"slices"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPLpgSQLAssignmentTargetValidation(t *testing.T) {
	integer := plUnitInteger(t)
	d := &Definition{Kind: PLpgSQLBody, Name: "targets", Result: integer.Usage(), Source: "<<scope>> DECLARE a integer; b integer; x ALIAS FOR a; BEGIN scope.a[1].n[2:3] := 7; x[2] = 8; b := 9; END"}
	raw, err := parser.ParsePLpgSQL(d.Source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePL(d, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return integer.Usage(), false, nil })
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax, d.PL = raw, p
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	body := p.Root.Statements[0].Block
	first := &body.Statements[0].Target
	if first.Slot != body.Bindings["a"] || len(first.Steps) != 3 || first.Steps[0].Subscript == nil || first.Steps[1].Field == nil || first.Steps[1].Field.Text != "n" || first.Steps[2].Subscript == nil || !first.Steps[2].Subscript.Slice || body.Statements[1].Target.Slot != first.Slot {
		t.Fatal("ordered path or longest lexical/alias root lost", first)
	}
	original := *first
	for _, corrupt := range []func(){
		func() { first.Slot = body.Bindings["b"] },
		func() { first.Steps = first.Steps[:2] },
		func() {
			first.Steps = slices.Clone(first.Steps)
			field := *first.Steps[1].Field
			field.Text = "redirected"
			first.Steps[1].Field = &field
		},
		func() {
			first.Steps = slices.Clone(first.Steps)
			subscript := *first.Steps[0].Subscript
			subscript.Upper = first.Steps[2].Subscript.Upper
			first.Steps[0].Subscript = &subscript
		},
	} {
		corrupt()
		if err := p.ValidateDefinition(d); err == nil {
			t.Fatal("forged target accepted", first)
		}
		*first = original
	}
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal("valid target no longer accepted", err)
	}
}
