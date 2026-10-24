package proc

import (
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/parser"
)

func TestPLpgSQLQueryLoopValidation(t *testing.T) {
	const source = `<<s>> DECLARE r record; a ALIAS FOR r; BEGIN <<row_scan>> FOR s.a IN SELECT r.a+a.a AS n LOOP r.b:=1; CONTINUE row_scan WHEN false; EXIT row_scan; END LOOP row_scan; CASE 1 WHEN 1 THEN RETURN 7; ELSE RETURN 8; END CASE; END s`
	d := conditionalUnitDefinition(t, source)
	p := d.PL
	body := p.Root.Statements[0].Block
	loop := body.Statements[0].QueryLoop
	if len(p.Variables) != 2 || loop.Target.Slot != body.Bindings["r"] || len(loop.Target.Steps) != 0 || len(loop.Body.Bindings) != 0 || loop.Body.Statements[1].Transfer != loop.Body.Control || loop.Body.Statements[2].Transfer != loop.Body.Control {
		t.Fatal("query loop created a shadow or lost enclosing target/control")
	}
	if got := body.Statements[1].Case.SearchName; got != "__Case__Variable_4__" {
		t.Fatal("query/body original-field ledger", got)
	}
	variables := slices.Clone(p.Variables)
	syntax, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(syntax, d.PLSyntax) {
		t.Fatal("compilation mutated syntax", err)
	}
	for _, name := range []string{"slot", "steps", "source", "body", "control", "payload", "span", "missing", "ledger"} {
		t.Run(name, func(t *testing.T) {
			candidate := conditionalUnitDefinition(t, source)
			b := candidate.PL.Root.Statements[0].Block
			in := &b.Statements[0]
			q := in.QueryLoop
			switch name {
			case "slot":
				q.Target.Slot = candidate.PL.FoundSlot
			case "steps":
				q.Target.Steps = []parser.TargetStep{{Field: &parser.Identifier{Text: "a"}}}
			case "source":
				q.Query = &parser.Select{}
			case "body":
				q.Body = b
			case "control":
				q.Body.Statements[2].Transfer = candidate.PL.Root.Control
			case "payload":
				in.Query = &parser.Select{}
			case "span":
				in.Syntax.QueryLoop.End.Character++
			case "missing":
				in.QueryLoop = nil
			case "ledger":
				b.Statements[1].Case.SearchName = "__Case__Variable_3__"
			}
			if err := candidate.PL.ValidateDefinition(candidate); err == nil {
				t.Fatal("accepted forged", name)
			}
			if !reflect.DeepEqual(p.Variables, variables) || !reflect.DeepEqual(d.PLSyntax, syntax) {
				t.Fatal("candidate mutation reached original program")
			}
			if err := p.ValidateDefinition(d); err != nil {
				t.Fatal("original program damaged", err)
			}
		})
	}
	copyProgram := *p
	copyProgram.Variables = slices.Clone(p.Variables)
	if !p.SameLayout(&copyProgram) {
		t.Fatal("immutable usage copy changed layout")
	}
	for _, query := range []string{`SELECT 1 AS i`, `INSERT INTO q VALUES (1) RETURNING i`, `UPDATE q SET i=2 RETURNING i`, `DELETE FROM q RETURNING i`, `WITH c AS (SELECT 1 AS i) SELECT i FROM c`} {
		conditionalUnitDefinition(t, `DECLARE r record; BEGIN FOR r IN `+query+` LOOP END LOOP; RETURN 7; END`)
	}
}
