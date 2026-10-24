package proc

import (
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLRaiseCompiler(t *testing.T) {
	const source = "DECLARE x integer:=7; BEGIN RAISE NOTICE '% %',x,missing_raise_argument() USING DETAIL=x,HINT=missing_raise_hint(),DETAIL=NULL; RETURN x; END"
	d := conditionalUnitDefinition(t, source)
	in := d.PL.Root.Statements[0].Block.Statements[0]
	if in.Query != nil || in.Raise.Level != parser.PLRaiseNotice || in.Raise.Form != parser.PLRaiseFormat || len(in.Raise.Arguments) != 2 || len(in.Raise.Options) != 3 { t.Fatal("wrong checked form") }
	for i, value := range in.Syntax.Raise.Arguments {
		if !reflect.DeepEqual(in.Raise.Arguments[i], plExpressionQuery(value.Expression)) { t.Fatal("argument query changed syntax") }
	}
	for i, option := range in.Syntax.Raise.Options {
		if in.Raise.Options[i].Kind != option.Kind || !reflect.DeepEqual(in.Raise.Options[i].Query, plExpressionQuery(option.Value.Expression)) { t.Fatal("option query/order changed syntax") }
	}
	for _, tc := range []struct{ body, code, message string }{
		{"RAISE '%';", "42601", "too few parameters specified for RAISE"},
		{"RAISE '%%',1;", "42601", "too many parameters specified for RAISE"},
		{"IF false THEN RAISE '%'; END IF;", "42601", "too few parameters specified for RAISE"},
		{"RAISE no_such_condition;", "42704", "unrecognized exception condition \"no_such_condition\""},
		{"RAISE others;", "42704", "unrecognized exception condition \"others\""},
	} {
		rawSource := "BEGIN "+tc.body+" RETURN 7; END"
		raw, err := parser.ParsePLpgSQL(rawSource, parser.PLReturnPolicy{})
		if err != nil { t.Fatal(err) }
		bad := &Definition{Name:"bad", Source:rawSource, Result:plUnitInteger(t).Usage()}
		_, err = CompilePL(bad, raw, false, plUnitCollation(t), plUnitResolver(t, bad.Result))
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || diagnostic.Message != tc.message || diagnostic.Context != "compilation of PL/pgSQL function \"bad\" near line 1" { t.Fatalf("compile diagnostic: %#v", err) }
	}
	for _, tc := range []struct{ condition, code string }{{"modifying_sql_data_not_permitted", "2F002"}, {"division_by_zero", "22012"}, {"00000", "00000"}, {"ZX987", "ZX987"}} {
		if got, err := RaiseSQLState(tc.condition); err != nil || got != tc.code { t.Fatal(tc.condition, got, err) }
	}
	handler, err := compilePLCondition(parser.PLCondition{Name:parser.Identifier{Text:"modifying_sql_data_not_permitted"}})
	if err != nil || !slices.Contains(handler.Codes, "2F002") || !slices.Contains(handler.Codes, "38002") { t.Fatal("construction changed alias handler matching", handler, err) }
}

func TestPLpgSQLAssertCompiler(t *testing.T) {
	const source = "BEGIN ASSERT missing_condition(),missing_message(); ASSERT true; ASSERT NULL,NULL; RETURN 7; END"
	d := conditionalUnitDefinition(t, source)
	body := d.PL.Root.Statements[0].Block
	for _, in := range body.Statements[:3] {
		if in.Assert == nil || in.Assert.Condition == nil || in.Query != nil || !reflect.DeepEqual(in.Assert.Condition, plExpressionQuery(in.Syntax.Assert.Condition.Expression)) || !reflect.DeepEqual(in.Assert.Message, plExpressionQuery(in.Syntax.Assert.Message.Expression)) { t.Fatal("assert query association") }
	}
	if body.Statements[1].Assert.Message != nil || body.Statements[2].Assert.Message == nil || len(d.PL.Variables) != 1 { t.Fatal("message presence or extra assertion state") }
	fresh, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(fresh, d.PLSyntax) { t.Fatal("compiler mutated source", err) }
}

func TestPLpgSQLRaiseAssertValidation(t *testing.T) {
	const source = "DECLARE r record; BEGIN RAISE NOTICE '% %',r.a,r.b USING DETAIL=r.c,HINT=r.d; ASSERT r.e,r.f; CASE 1 WHEN 1 THEN RETURN 7; END CASE; RAISE; RETURN 8; END"
	original := conditionalUnitDefinition(t, source)
	if got := original.PL.Root.Statements[0].Block.Statements[2].Case.SearchName; got != "__Case__Variable_8__" {
		t.Fatal("message operands lost ordered field diagnostic datums", got)
	}
	variables := slices.Clone(original.PL.Variables)
	for _, name := range []string{"missing_raise", "argument_query", "argument_extra_clause", "argument_order", "option_kind", "option_order", "extra_option", "level", "form", "format", "code", "assert_condition", "assert_message", "mixed", "rethrow_payload", "return_payload", "span", "ledger"} {
		t.Run(name, func(t *testing.T) {
			candidate := conditionalUnitDefinition(t, source)
			body := candidate.PL.Root.Statements[0].Block
			raise, assertion := body.Statements[0].Raise, body.Statements[1].Assert
			switch name {
			case "missing_raise": body.Statements[0].Raise = nil
			case "argument_query": raise.Arguments[0] = raise.Options[0].Query
			case "argument_extra_clause": q := *raise.Arguments[0]; q.Where=q.Targets[0]; raise.Arguments[0]=&q
			case "argument_order": raise.Arguments[0],raise.Arguments[1]=raise.Arguments[1],raise.Arguments[0]
			case "option_kind": raise.Options[0].Kind=parser.PLRaiseSchema
			case "option_order": raise.Options[0],raise.Options[1]=raise.Options[1],raise.Options[0]
			case "extra_option": raise.Options=append(raise.Options,raise.Options[0])
			case "level": raise.Level=parser.PLRaiseException
			case "form": raise.Form=parser.PLRaiseUsing
			case "format": raise.Format="changed"
			case "code": raise.Code="ZX987"
			case "assert_condition": assertion.Condition=nil
			case "assert_message": assertion.Message=raise.Arguments[0]
			case "mixed": body.Statements[0].Assert=assertion
			case "rethrow_payload": body.Statements[3].Raise=raise
			case "return_payload": body.Statements[4].Assert=assertion
			case "span": body.Statements[0].Syntax.Raise.Arguments[0].End.Character++
			case "ledger": body.Statements[2].Case.SearchName="__Case__Variable_2__"
			}
			if err := candidate.PL.ValidateDefinition(candidate); err == nil { t.Fatal("accepted forged", name) }
			if !reflect.DeepEqual(original.PL.Variables, variables) { t.Fatal("candidate mutated source usages") }
			if err := original.PL.ValidateDefinition(original); err != nil { t.Fatal("original damaged", err) }
		})
	}
	copyProgram := *original.PL
	copyProgram.Variables=slices.Clone(original.PL.Variables)
	if !original.PL.SameLayout(&copyProgram) { t.Fatal("usage copy changed semantic layout") }
	changed := conditionalUnitDefinition(t, "BEGIN RAISE NOTICE 'changed'; ASSERT true; RETURN 7; END")
	if original.PL.SameLayout(changed.PL) { t.Fatal("different message program equivalent") }
}
