package proc

import (
	"errors"
	"slices"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPLExceptionMatcher(t *testing.T) {
	for _, tc := range []struct {
		conditions []PLCondition
		code       string
		match      bool
	}{
		{[]PLCondition{{Codes: []string{"22012"}}}, "22012", true},
		{[]PLCondition{{Codes: []string{"22000"}}}, "22P02", true},
		{[]PLCondition{{Codes: []string{"22012"}}}, "22P02", false},
		{[]PLCondition{{Codes: []string{"23505"}}, {Codes: []string{"22000"}}}, "22012", true},
		{[]PLCondition{{Others: true}}, "57014", false},
		{[]PLCondition{{Others: true}}, "P0004", false},
		{[]PLCondition{{Others: true}}, "23503", true},
		{[]PLCondition{{Codes: []string{"57014"}}}, "57014", true},
		{[]PLCondition{{Codes: []string{"P0004"}}}, "P0004", true},
	} {
		block := &PLBlock{Handlers: []PLHandler{{Conditions: tc.conditions}}}
		if got := block.MatchException(&sqlstate.Error{Code: tc.code}); (got != nil) != tc.match {
			t.Fatalf("%+v state%s match=%v", tc.conditions, tc.code, got)
		}
	}
	block := &PLBlock{Handlers: []PLHandler{{Conditions: []PLCondition{{Others: true}}}, {Conditions: []PLCondition{{Codes: []string{"22012"}}}}}}
	if block.MatchException(&sqlstate.Error{Code: "22012"}) != &block.Handlers[0] || block.MatchException(nil) != nil {
		t.Fatal("source order/nil diagnostic")
	}
	if len(plExceptionConditions) != 251 {
		t.Fatalf("released error label count: %d", len(plExceptionConditions))
	}
	for _, entry := range plExceptionConditions {
		condition, err := compilePLCondition(parser.PLCondition{Name: parser.Identifier{Text: entry.name}})
		if err != nil || !slices.Contains(condition.Codes, entry.code) {
			t.Fatalf("released condition %s/%s: %+v %v", entry.name, entry.code, condition, err)
		}
		b := PLBlock{Handlers: []PLHandler{{Conditions: []PLCondition{condition}}}}
		if b.MatchException(&sqlstate.Error{Code: entry.code}) == nil {
			t.Fatal("unmatchable released condition", entry)
		}
	}
	for _, tc := range []struct {
		name  string
		codes []string
	}{
		{"null_value_not_allowed", []string{"22004", "39004"}},
		{"modifying_sql_data_not_permitted", []string{"2F002", "38002"}},
		{"prohibited_sql_statement_attempted", []string{"2F003", "38003"}},
		{"reading_sql_data_not_permitted", []string{"2F004", "38004"}},
		{"string_data_right_truncation", []string{"22001"}},
	} {
		c, err := compilePLCondition(parser.PLCondition{Name: parser.Identifier{Text: tc.name}})
		if err != nil || !slices.Equal(c.Codes, tc.codes) {
			t.Fatalf("alias/filter %s: %+v %v", tc.name, c, err)
		}
	}
	for _, name := range []string{"warning", "successful_completion", "no_data", "unknown_condition"} {
		if _, err := compilePLCondition(parser.PLCondition{Name: parser.Identifier{Text: name}}); err == nil {
			t.Fatal("unknown/nonerror condition accepted", name)
		}
	}
	for _, code := range []string{"1234", "22p02", "AB_CD"} {
		if _, err := compilePLCondition(parser.PLCondition{Code: code}); err == nil {
			t.Fatal("invalid code accepted", code)
		}
	}
	if _, err := compilePLCondition(parser.PLCondition{Code: "ZX987"}); err != nil {
		t.Fatal(err)
	}
}

func plExceptionUnitProgram(t *testing.T) (*PLProgram, *Definition, *sqltype.Descriptor) {
	t.Helper()
	integer := plUnitInteger(t)
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
		}
	}
	if text == nil {
		t.Fatal("missing text")
	}
	d := &Definition{Kind: PLpgSQLBody, Name: "exceptions", Result: text.Usage(), Source: "DECLARE x integer; BEGIN x := 1/0; EXCEPTION WHEN division_by_zero OR SQLSTATE '22000' THEN BEGIN RAISE; EXCEPTION WHEN OTHERS THEN RETURN SQLSTATE; END; END"}
	raw, err := parser.ParsePLpgSQL(d.Source)
	if err != nil {
		t.Fatal(err)
	}
	p, err := CompilePL(d, raw, false, func(raw parser.PLType) (sqltype.Usage, bool, error) {
		if raw.Type.Name == "text" {
			return text.Usage(), false, nil
		}
		return integer.Usage(), false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	d.PLSyntax, d.PL = raw, p
	if err := p.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	return p, d, text
}

func TestPLExceptionProgramValidation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		breakProgram func(*PLProgram)
	}{
		{"handler_syntax", func(p *PLProgram) { p.Root.Statements[0].Block.Handlers[0].Syntax = nil }},
		{"condition", func(p *PLProgram) { p.Root.Statements[0].Block.Handlers[0].Conditions[0].Codes = []string{"23505"} }},
		{"missing_handler", func(p *PLProgram) { p.Root.Statements[0].Block.Handlers = nil }},
		{"scope", func(p *PLProgram) { p.Root.Statements[0].Block.HandlerScope = nil }},
		{"intrinsic_slot", func(p *PLProgram) { p.Root.Statements[0].Block.HandlerScope.Bindings["sqlstate"] = 0 }},
		{"constant", func(p *PLProgram) { p.Variables[1].Constant = false }},
		{"type", func(p *PLProgram) { p.Variables[1].Usage = plUnitInteger(t).Usage() }},
		{"nested_rethrow", func(p *PLProgram) {
			p.Root.Statements[0].Block.Handlers[0].Statements[0].Block.Statements[0].Query = &parser.Select{}
		}},
		{"nested_intrinsic", func(p *PLProgram) {
			p.Root.Statements[0].Block.Handlers[0].Statements[0].Block.HandlerScope.Bindings["sqlerrm"] = 0
		}},
		{"handler_target_same_position", func(p *PLProgram) {
			instruction := &p.Root.Statements[0].Block.Handlers[0].Statements[0].Block.Handlers[0].Statements[0]
			query := *instruction.Query
			target := instruction.Syntax.Expression.(parser.ColumnExpr)
			target.Name.Parts = append([]parser.Identifier(nil), target.Name.Parts...)
			target.Name.Parts[0].Text = "sqlerrm"
			query.Targets = []parser.Expr{target}
			instruction.Query = &query
		}},
		{"handler_extra_query_clause", func(p *PLProgram) {
			instruction := &p.Root.Statements[0].Block.Handlers[0].Statements[0].Block.Handlers[0].Statements[0]
			query := *instruction.Query
			clause, err := parser.ParsePLpgSQL("BEGIN RETURN true; END")
			if err != nil {
				t.Fatal(err)
			}
			query.Where = clause.Statements[0].Expression
			instruction.Query = &query
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, d, _ := plExceptionUnitProgram(t)
			tc.breakProgram(p)
			if p.ValidateDefinition(d) == nil {
				t.Fatal("forged handler program accepted")
			}
		})
	}
	p, d, text := plExceptionUnitProgram(t)
	input := NewFrame(nil)
	defer input.Close()
	first := NewPLFrame(p, d, input, sqltype.CollationRef{})
	second := NewPLFrame(p, d, input, sqltype.CollationRef{})
	defer second.Close()
	outer := p.Root.Statements[0].Block
	inner := outer.Handlers[0].Statements[0].Block
	original := &sqlstate.Error{Code: "22012", Message: "division by zero", Detail: "owned detail", Context: "original context"}
	nested := &sqlstate.Error{Code: "22P02", Message: "bad integer"}
	first.Enter(p.Root)
	first.Enter(outer)
	previous := first.EnterHandler(outer.HandlerScope, original)
	if previous != nil || second.HandledError() != nil || !sqltype.SameDatum(first.Values[outer.HandlerScope.Bindings["sqlstate"]], sqltype.String(text, "22012")) {
		t.Fatal("invocation state shared/missing")
	}
	first.Enter(inner)
	prior := first.EnterHandler(inner.HandlerScope, nested)
	if prior != original || first.HandledError() != nested {
		t.Fatal("nested diagnostic not installed")
	}
	first.LeaveHandler(inner.HandlerScope, prior)
	first.Leave(inner)
	if first.HandledError() != original || !sqltype.SameDatum(first.Values[outer.HandlerScope.Bindings["sqlerrm"]], sqltype.String(text, "division by zero")) {
		t.Fatal("outer diagnostic/intrinsics not restored")
	}
	first.LeaveHandler(outer.HandlerScope, previous)
	first.Leave(outer)
	first.Leave(p.Root)
	first.Close()
	if first.HandledError() != nil || first.Values != nil || first.Scopes != nil {
		t.Fatal("closed frame retains exception values")
	}
	// Compiler rejects unreachable unknown conditions, not just reached handlers.
	raw, err := parser.ParsePLpgSQL("BEGIN RETURN 'ok'; EXCEPTION WHEN unknown_condition THEN RETURN 'bad'; END")
	if err != nil {
		t.Fatal(err)
	}
	_, err = CompilePL(d, raw, false, func(parser.PLType) (sqltype.Usage, bool, error) { return text.Usage(), false, nil })
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" {
		t.Fatal("unknown condition not rejected at compilation", err)
	}
}
