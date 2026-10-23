package proc

import (
	"reflect"
	"testing"

	"stuntgres/internal/parser"
)

func TestPLpgSQLCursorArgumentSourceLedger(t *testing.T) {
	d, p, _ := cursorProgramUnit(t)
	body := p.Root.Statements[0].Block
	open := body.Statements[0].Cursor
	if open.ArgumentQuery == nil || len(open.ArgumentQuery.Targets) != 2 || len(open.ArgumentQuery.Labels) != 2 {
		t.Fatal("missing whole ordinary projection")
	}
	for i, want := range []struct{ expression, name string }{{"7", "a"}, {"'x'", "b"}} {
		actual := open.Arguments[i]
		if actual.Query != nil || parser.PLExpressionSource(p.Source, actual.Start, actual.End) != want.expression || open.ArgumentQuery.Labels[i].Text != want.name || !reflect.DeepEqual(open.ArgumentQuery.Targets[i], actual.Expression) {
			t.Fatal("declaration-order source/target association lost", i)
		}
	}
	before, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, len(d.OutputFormals), false))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, d.PLSyntax) {
		t.Fatal("validation mutated immutable syntax")
	}
	for _, kind := range []string{"missing_projection", "extra_filter", "target_order", "alias", "start", "end", "delimiter", "individual_query"} {
		t.Run(kind, func(t *testing.T) {
			d, p, _ := cursorProgramUnit(t)
			in := p.Root.Statements[0].Block.Statements[0].Cursor
			syntax, err := parser.ParsePLpgSQL(d.Source, PLReturnPolicy(d.Result, len(d.OutputFormals), false))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing_projection":
				in.ArgumentQuery = nil
			case "extra_filter":
				in.ArgumentQuery.Where = in.Arguments[0].Expression
			case "target_order":
				in.ArgumentQuery.Targets[0], in.ArgumentQuery.Targets[1] = in.ArgumentQuery.Targets[1], in.ArgumentQuery.Targets[0]
			case "alias":
				in.ArgumentQuery.Labels[0].Text = "other"
			case "start":
				in.Arguments[0].Start.Character++
			case "end":
				in.Arguments[0].End.Offset++
			case "delimiter":
				in.Arguments[0].Delimiter = parser.Position{}
			case "individual_query":
				in.Arguments[0].Query = plExpressionQuery(in.Arguments[0].Expression)
			}
			if err := p.ValidateDefinition(d); err == nil {
				t.Fatal("forged projection/source metadata accepted")
			}
			if !reflect.DeepEqual(syntax, d.PLSyntax) {
				t.Fatal("forgery validation mutated source syntax")
			}
		})
	}
}
