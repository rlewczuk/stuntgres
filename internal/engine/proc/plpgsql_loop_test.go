package proc

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLLoopCompiler(t *testing.T) {
	const source = "DECLARE n integer:=0; BEGIN <<outer_loop>> LOOP <<b>> BEGIN <<inner_loop>> LOOP CONTINUE WHEN false; EXIT outer_loop WHEN n=3; EXIT b; END LOOP; END b; EXIT; END LOOP outer_loop; RETURN n; END"
	d := conditionalUnitDefinition(t, source)
	body := d.PL.Root.Statements[0].Block
	outer := body.Statements[0].Loop
	block := outer.Statements[0].Block
	inner := block.Statements[0].Loop
	if d.PL.Root.Control.ID != 1 || body.Control.ID != 2 || outer.Control.ID != 3 || block.Control.ID != 4 || inner.Control.ID != 5 {
		t.Fatal("control IDs are not deterministic preorder")
	}
	if inner.Statements[0].Transfer != inner.Control || inner.Statements[1].Transfer != outer.Control || inner.Statements[2].Transfer != block.Control || outer.Statements[1].Transfer != outer.Control {
		t.Fatal("default or named ancestry changed")
	}
	if len(d.PL.Variables) != 2 || len(outer.Declarations) != 0 || len(inner.Declarations) != 0 {
		t.Fatal("loop introduced fresh variable slots")
	}
	before, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(before, d.PLSyntax) {
		t.Fatal("compilation mutated syntax", err)
	}
	hidden := conditionalUnitDefinition(t, "BEGIN EXIT conditional_unit; RETURN 7; END")
	if hidden.PL.Root.Statements[0].Block.Statements[0].Transfer != hidden.PL.Root.Control {
		t.Fatal("hidden function namespace not recognized")
	}
	nearest := conditionalUnitDefinition(t, "BEGIN <<x>> LOOP <<x>> LOOP CONTINUE x WHEN false; EXIT x; END LOOP; EXIT x; END LOOP; RETURN 7; END")
	outer = nearest.PL.Root.Statements[0].Block.Statements[0].Loop
	inner = outer.Statements[0].Loop
	if inner.Statements[0].Transfer != inner.Control || inner.Statements[1].Transfer != inner.Control {
		t.Fatal("nearest duplicate label not selected")
	}
	for _, expression := range []string{"true x", `true AS "Étage"`} {
		d := conditionalUnitDefinition(t, "BEGIN LOOP EXIT WHEN "+expression+"; END LOOP; RETURN 7; END")
		in := d.PL.Root.Statements[0].Block.Statements[0].Loop.Statements[0]
		if len(in.Query.Labels) != 1 || in.Query.Labels[0] != in.Syntax.Transfer.Alias {
			t.Fatal("checked transfer dropped positioned projection alias")
		}
	}
	for _, tc := range []struct{ source, message string }{
		{"BEGIN EXIT; RETURN 7; END", "EXIT cannot be used outside a loop, unless it has a label"},
		{"BEGIN CONTINUE; RETURN 7; END", "CONTINUE cannot be used outside a loop"},
		{"BEGIN LOOP EXIT absent WHEN false; END LOOP; RETURN 7; END", `there is no label "absent" attached to any block or loop enclosing this statement`},
		{"BEGIN RETURN 7; LOOP CONTINUE absent WHEN NULL; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`},
		{"BEGIN <<x>> LOOP <<x>> BEGIN CONTINUE x; END; END LOOP; RETURN 7; END", `block label "x" cannot be used in CONTINUE`},
		{"BEGIN CONTINUE conditional_unit; RETURN 7; END", `block label "conditional_unit" cannot be used in CONTINUE`},
		{"BEGIN <<old>> LOOP EXIT; END LOOP; LOOP EXIT old; END LOOP; RETURN 7; END", `there is no label "old" attached to any block or loop enclosing this statement`},
	} {
		raw, err := parser.ParsePLpgSQL(tc.source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal("static witness is not syntax-valid", err)
		}
		d := &Definition{Name: "conditional_unit", Source: tc.source, Result: plUnitInteger(t).Usage()}
		_, err = CompilePL(d, raw, false, plUnitCollation(t), plUnitResolver(t, d.Result))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != tc.message {
			t.Fatalf("static target diagnostic got=%v want=%q", err, tc.message)
		}
	}
}

func TestPLpgSQLLoopValidation(t *testing.T) {
	const source = "BEGIN <<outer_loop>> LOOP <<inner_loop>> LOOP EXIT outer_loop WHEN true; END LOOP; EXIT; END LOOP; LOOP EXIT; END LOOP; RETURN 7; END"
	for _, name := range []string{"missing_loop", "missing_owner", "redirected_target", "foreign_target", "sibling_target", "owner_id", "owner_kind", "duplicate_owner", "label", "missing_condition", "condition_clause", "condition_span", "extra_variant"} {
		t.Run(name, func(t *testing.T) {
			d := conditionalUnitDefinition(t, source)
			body := d.PL.Root.Statements[0].Block
			outer := body.Statements[0].Loop
			inner := outer.Statements[0].Loop
			transfer := &inner.Statements[0]
			switch name {
			case "missing_loop":
				body.Statements[0].Loop = nil
			case "missing_owner":
				outer.Control = nil
			case "redirected_target":
				transfer.Transfer = inner.Control
			case "foreign_target":
				foreign := conditionalUnitDefinition(t, source)
				transfer.Transfer = foreign.PL.Root.Statements[0].Block.Statements[0].Loop.Control
			case "sibling_target":
				transfer.Transfer = body.Statements[1].Loop.Control
			case "owner_id":
				outer.Control.ID++
			case "owner_kind":
				outer.Control.Loop = false
			case "duplicate_owner":
				inner.Control = outer.Control
			case "label":
				outer.Label = "redirected"
			case "missing_condition":
				transfer.Query = nil
			case "condition_clause":
				transfer.Query.Where = transfer.Query.Targets[0]
			case "condition_span":
				transfer.Syntax.ExpressionStart.Character++
			case "extra_variant":
				transfer.If = &PLConditional{}
			}
			if err := d.PL.ValidateDefinition(d); err == nil {
				t.Fatal("forged loop program accepted", name)
			}
		})
	}
	original := conditionalUnitDefinition(t, source)
	equivalent := conditionalUnitDefinition(t, source)
	if !original.PL.SameLayout(equivalent.PL) {
		t.Fatal("equivalent token graphs have different semantic layout")
	}
	equivalent.PL.Root.Statements[0].Block.Statements[0].Loop.Statements[0].Loop.Statements[0].Transfer = equivalent.PL.Root.Control
	if original.PL.SameLayout(equivalent.PL) {
		t.Fatal("retargeted flow disappeared from semantic layout")
	}
	if err := original.PL.ValidateDefinition(original); err != nil {
		t.Fatal("comparison mutated source", err)
	}
	for _, name := range []string{"missing_alias", "changed_alias", "extra_alias"} {
		t.Run(name, func(t *testing.T) {
			d := conditionalUnitDefinition(t, "BEGIN LOOP EXIT WHEN true x; END LOOP; RETURN 7; END")
			query := d.PL.Root.Statements[0].Block.Statements[0].Loop.Statements[0].Query
			switch name {
			case "missing_alias":
				query.Labels = nil
			case "changed_alias":
				query.Labels[0].Text = "redirected"
			case "extra_alias":
				query.Labels = append(query.Labels, query.Labels[0])
			}
			if err := d.PL.ValidateDefinition(d); err == nil {
				t.Fatal("forged projection alias accepted")
			}
		})
	}
}

func TestPLpgSQLWhileCompiler(t *testing.T) {
	const source = "DECLARE n integer:=0; BEGIN <<o>> WHILE n<3 admitted LOOP <<b>> BEGIN LOOP CONTINUE o; EXIT b; END LOOP; END b; EXIT; END LOOP o; RETURN n; END"
	d := conditionalUnitDefinition(t, source)
	body := d.PL.Root.Statements[0].Block
	loop := body.Statements[0].While
	block := loop.Body.Statements[0].Block
	inner := block.Statements[0].Loop
	if d.PL.Root.Control.ID != 1 || body.Control.ID != 2 || loop.Body.Control.ID != 3 || block.Control.ID != 4 || inner.Control.ID != 5 || !loop.Body.Control.Loop {
		t.Fatal("WHILE owner preorder changed")
	}
	if inner.Statements[0].Transfer != loop.Body.Control || inner.Statements[1].Transfer != block.Control || loop.Body.Statements[1].Transfer != loop.Body.Control {
		t.Fatal("WHILE mixed-loop ancestry changed")
	}
	if len(d.PL.Variables) != 2 || d.PL.SearchCount != 0 || len(loop.Body.Declarations) != 0 || len(loop.Body.Bindings) != 0 {
		t.Fatal("WHILE introduced executable slots")
	}
	raw := body.Statements[0].Syntax.While.Condition
	if !reflect.DeepEqual(loop.Condition.Targets[0], raw.Expression) || len(loop.Condition.Labels) != 1 || loop.Condition.Labels[0] != raw.Alias || loop.Condition.Pos != raw.Expression.SourcePosition() {
		t.Fatal("WHILE condition/alias recipe changed")
	}
	before, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(before, d.PLSyntax) {
		t.Fatal("compilation mutated WHILE syntax", err)
	}
	ledger := walkUnitDefinition(t, "DECLARE r record; BEGIN WHILE r.head LOOP r.body:=1; EXIT; END LOOP; CASE 1 WHEN 1 THEN RETURN 7; END CASE; END")
	if got := ledger.PL.Root.Statements[0].Block.Statements[1].Case.SearchName; got != "__Case__Variable_4__" {
		t.Fatal("WHILE condition/body diagnostic ledger order", got)
	}
}

func TestPLpgSQLWhileValidation(t *testing.T) {
	const source = "BEGIN <<o>> WHILE true admitted LOOP WHILE false LOOP EXIT o; END LOOP; EXIT; END LOOP o; WHILE false LOOP END LOOP; RETURN 7; END"
	for _, name := range []string{"missing_variant", "missing_condition", "redirected_query", "duplicate_query", "extra_clause", "alias", "extra_alias", "start", "end", "delimiter", "body_position", "missing_body", "body_kind", "missing_owner", "foreign_owner", "sibling_target", "foreign_target", "mixed_checked", "mixed_raw", "raw_condition"} {
		t.Run(name, func(t *testing.T) {
			d := conditionalUnitDefinition(t, source)
			body := d.PL.Root.Statements[0].Block
			in := &body.Statements[0]
			loop, raw := in.While, in.Syntax.While
			inner := loop.Body.Statements[0].While
			switch name {
			case "missing_variant":
				in.While = nil
			case "missing_condition":
				loop.Condition = nil
			case "redirected_query":
				loop.Condition.Targets[0] = inner.Condition.Targets[0]
			case "duplicate_query":
				inner.Condition = loop.Condition
			case "extra_clause":
				loop.Condition.Where = loop.Condition.Targets[0]
			case "alias":
				loop.Condition.Labels = nil
			case "extra_alias":
				loop.Condition.Labels = append(loop.Condition.Labels, loop.Condition.Labels[0])
			case "start":
				raw.Condition.Start.Character++
			case "end":
				raw.Condition.End.Offset++
			case "delimiter":
				raw.Condition.Delimiter = raw.Condition.End
			case "body_position":
				raw.Body.Pos = raw.Condition.Start
			case "missing_body":
				loop.Body = nil
			case "body_kind":
				raw.Body.IsLoop = false
			case "missing_owner":
				loop.Body.Control = nil
			case "foreign_owner":
				loop.Body.Control = &PLControl{ID: 99, Loop: true}
			case "sibling_target":
				inner.Body.Statements[0].Transfer = body.Statements[1].While.Body.Control
			case "foreign_target":
				inner.Body.Statements[0].Transfer = &PLControl{ID: 3, Loop: true}
			case "mixed_checked":
				in.Loop = loop.Body
			case "mixed_raw":
				in.Syntax.Loop = raw.Body
			case "raw_condition":
				raw.Condition.Expression = nil
			}
			if err := d.PL.ValidateDefinition(d); err == nil {
				t.Fatal("forged WHILE accepted", name)
			}
		})
	}
	for _, change := range []string{"condition", "body", "owner"} {
		a, b := conditionalUnitDefinition(t, source), conditionalUnitDefinition(t, source)
		if !a.PL.SameLayout(b.PL) {
			t.Fatal("equivalent WHILE layouts differ")
		}
		loop := b.PL.Root.Statements[0].Block.Statements[0].While
		switch change {
		case "condition":
			loop.Condition.Targets[0] = loop.Body.Statements[0].While.Condition.Targets[0]
		case "body":
			loop.Body.Statements = nil
		case "owner":
			loop.Body.Control.ID++
		}
		if a.PL.SameLayout(b.PL) {
			t.Fatal("WHILE layout discarded", change)
		}
		if err := a.PL.ValidateDefinition(a); err != nil {
			t.Fatal("layout comparison mutated original", err)
		}
	}
}
