package engine

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Cancel at a real executor state boundary, not after a sleep or an Err-call
// count. The operation observes an ordinary canceled context while CASE owns
// its search value, so its actual defer and lexical activation guards must unwind.
type conditionalFrameCancel struct {
	context.Context
	cancel context.CancelFunc
	frame  *proc.PLFrame
}

func (c conditionalFrameCancel) Err() error {
	if len(c.frame.Searches) != 0 && c.frame.Searches[0].Value.Type() != nil {
		c.cancel()
	}
	return c.Context.Err()
}

func TestPLpgSQLConditionalFrame(t *testing.T) {
	for _, tc := range []struct {
		name, source, code string
		cancel             bool
	}{
		{"return", "BEGIN CASE 1 WHEN 1 THEN IF true THEN RETURN 7; END IF; ELSE RETURN 8; END CASE; RETURN 1/0; END", "", false},
		{"when_error", "BEGIN CASE 1 WHEN 1/0 THEN RETURN 7; ELSE RETURN 8; END CASE; END", "22012", false},
		{"no_match", "BEGIN CASE NULL::integer WHEN NULL THEN RETURN 7; END CASE; END", "20000", false},
		{"cancel_active_search", "BEGIN CASE 1 WHEN 1 THEN RETURN 7; ELSE RETURN 8; END CASE; END", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, s := plUnitScope(t)
			read := e.CatalogRead()
			integer, _ := read.LookupType(sqltype.Int4)
			language, err := catalog.LookupLanguage(read, "plpgsql")
			if err != nil {
				t.Fatal(err)
			}
			raw, err := parser.ParsePLpgSQL(tc.source, parser.PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			d := &catalog.Procedure{ObjectName: "conditional_frame", Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "conditional_frame", Source: tc.source, Result: integer.Usage(), PLSyntax: raw}}
			e.mu.Lock()
			auth, err := e.authorizationLocked(s.op)
			if err == nil {
				err = e.compilePLRoutineLocked(s.op, auth, d)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			input := proc.NewFrame(nil)
			defer input.Close()
			f := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
			defer f.Close()
			if tc.cancel {
				ctx, cancel := context.WithCancel(s.op.Ctx)
				defer cancel()
				s.op.Ctx = conditionalFrameCancel{Context: ctx, cancel: cancel, frame: f}
			}
			value, returned, err := s.executePLBlock(d, f, d.SQL.PL.Root, read)
			if tc.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatal("active search cancellation lost", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary cancellation acquired SQLSTATE")
				}
			} else if tc.code != "" {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
					t.Fatal("conditional error lost", err)
				}
			} else {
				n, failure := value.Integer()
				if err != nil || failure != nil || !returned || n != 7 {
					t.Fatal("nested RETURN did not propagate", value, returned, err)
				}
			}
			if len(f.Scopes) != 0 || s.sqlRoutine.lexical != nil || s.activeSQLRoutineDepth != 0 {
				t.Fatal("conditional leaked lexical/query/root state")
			}
			for _, search := range f.Searches {
				if search.Value.Type() != nil || search.Usage != (sqltype.Usage{}) {
					t.Fatal("conditional retained search state on exit")
				}
			}
		})
	}
	t.Run("private_usage", func(t *testing.T) {
		e, s := plUnitScope(t)
		varchar, _ := e.CatalogRead().LookupType(sqltype.VarChar)
		usage := varchar.Usage()
		usage.Typmod = 9
		var err error
		usage.Collation, err = sqltype.RegisteredCollation(6411, 6411)
		if err != nil {
			t.Fatal(err)
		}
		frame := &proc.PLFrame{Program: &proc.PLProgram{SearchCount: 1}, Searches: []proc.PLSearch{{Value: sqltype.Null(varchar), Usage: usage}}}
		raw := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: proc.PLSearchName(0)}}}}
		e.mu.Lock()
		binder := newExpressionBinder(s.op.Context(), e, s.input, s.op, nil)
		binder.PL = frame
		node, err := binder.Column(raw)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		local, ok := node.(plan.PLLocalExpr)
		if !ok || local.ResultUsage() != usage || local.Value.Type() != varchar || !local.Value.IsNull() {
			t.Fatal("private search lost typed NULL/typmod/collation")
		}
		// Every plan node requires a live evaluation context, independently of
		// lexical-frame ownership. Keep that guard while testing detached values.
		if _, err := local.Eval(nil); err == nil {
			t.Fatal("private local bypassed the missing-context evaluation guard")
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.op.Context(), Host: hostOf(s), Catalog: e.CatalogRead(), Input: binder.Input, Output: s.output}}
		before, err := local.Eval(&evaluation)
		if err != nil || before.Type() != varchar || !before.IsNull() || local.ResultUsage() != usage {
			t.Fatalf("captured search before frame close: value=%#v usage=%#v err=%v", before, local.ResultUsage(), err)
		}
		evaluated, effective, present, err := s.evaluatePLExpression(frame, &parser.Select{Targets: []parser.Expr{raw}}, e.CatalogRead(), nil)
		if err != nil || !present || effective != usage || evaluated.Type() != varchar || !evaluated.IsNull() {
			t.Fatalf("expression result lost full Usage/presence: value=%#v usage=%#v present=%t err=%v", evaluated, effective, present, err)
		}
		frame.Close()
		value, err := local.Eval(&evaluation)
		if err != nil || value.Type() != varchar || !value.IsNull() || local.ResultUsage() != usage {
			t.Fatalf("captured search after frame close: value=%#v usage=%#v err=%v", value, local.ResultUsage(), err)
		}
		e.mu.Lock()
		_, err = binder.Column(raw)
		e.mu.Unlock()
		if err == nil {
			t.Fatal("inactive private binding admitted")
		}
		for _, parts := range [][]parser.Identifier{
			{{Text: "\x00case:00"}},
			{{Text: "\x00case:1"}},
			{{Text: "ordinary"}, {Text: proc.PLSearchName(0)}},
		} {
			e.mu.Lock()
			_, err := binder.Column(parser.ColumnExpr{Name: parser.ObjectName{Parts: parts}})
			e.mu.Unlock()
			if err == nil {
				t.Fatal("noncanonical or qualified private binding admitted", parts)
			}
		}
	})
	t.Run("diagnostic_coordinates", func(t *testing.T) {
		source := "/*é*/ BEGIN CASE 1 WHEN absent(1+missing()) THEN RETURN 7; END CASE; END"
		operator := proc.PLSearchOperatorPosition(source, 0)
		if operator.Character <= utf8.RuneCountInString(source)+1 || operator.Offset <= len(source) {
			t.Fatal("synthetic coordinate aliases body EOF")
		}
		start := parser.Position{Offset: 25, Character: 24}
		branch := proc.PLBranch{DiagnosticQuery: `"__Case__Variable_1__" IN (absent(1+missing()))`, OperatorPosition: operator, DiagnosticOperator: 24, DiagnosticItems: 28}
		for _, tc := range []struct {
			name           string
			position, want int
		}{
			{"operator", operator.Character, branch.DiagnosticOperator},
			{"first_item", start.Character, branch.DiagnosticItems},
			{"nested_item", start.Character + 9, branch.DiagnosticItems + 9},
		} {
			t.Run(tc.name, func(t *testing.T) {
				original := &sqlstate.Error{Code: "42883", Position: strconv.Itoa(tc.position)}
				err := plCaseExpressionError(original, branch, start)
				var mapped *sqlstate.Error
				if !errors.As(err, &mapped) {
					t.Fatal("mapped diagnostic unavailable", err)
				}
				if mapped.Position != "" || mapped.InternalPosition != strconv.Itoa(tc.want) || mapped.InternalQuery != branch.DiagnosticQuery || mapped.Context != "" || original.Position != strconv.Itoa(tc.position) {
					t.Fatal("synthetic/original coordinate confusion", mapped)
				}
			})
		}
		ordinary := context.Canceled
		if plCaseExpressionError(ordinary, branch, start) != ordinary {
			t.Fatal("ordinary error decorated")
		}
	})
	t.Run("diagnostic_error_chain", func(t *testing.T) {
		start := parser.Position{Offset: 20, Character: 21}
		branch := proc.PLBranch{DiagnosticQuery: `"__Case__Variable_1__" IN (called())`, DiagnosticOperator: 24, DiagnosticItems: 28}
		original := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "producer failure", Position: "21", Detail: "original detail"}
		before := *original
		failure := errors.New("provider cleanup failure")
		fatal := &plExceptionCleanupError{primary: original, cleanup: failure}
		mapped := plCaseExpressionError(fatal, branch, start)
		var cleanup *plExceptionCleanupError
		if !errors.As(mapped, &cleanup) || cleanup != fatal || !errors.Is(mapped, original) || !errors.Is(mapped, failure) {
			t.Fatal("CASE diagnostic discarded fatal cleanup classification or originating evidence", mapped)
		}
		var diagnostic *sqlstate.Error
		want := before
		want.Position, want.InternalPosition, want.InternalQuery = "", "28", branch.DiagnosticQuery
		if !errors.As(mapped, &diagnostic) || *diagnostic != want || *original != before {
			t.Fatal("CASE decoration lost mapped diagnostic or mutated its origin", diagnostic)
		}
	})
	t.Run("diagnostic_phase", func(t *testing.T) {
		branch := proc.PLBranch{DiagnosticQuery: `"__Case__Variable_2__" IN (12/x)`}
		for _, tc := range []struct {
			name     string
			planning bool
			context  string
		}{
			{"execution", false, ""},
			{"planning", true, `PL/pgSQL expression "` + branch.DiagnosticQuery + `"`},
			{"producer_context", false, "producer context"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				original := &sqlstate.Error{Code: "22012", Message: "division by zero"}
				if tc.name == "producer_context" {
					original.Context = tc.context
				}
				var cause error = original
				if tc.planning {
					cause = &plExpressionPlanningError{original}
				}
				mapped := plCaseExpressionError(cause, branch, parser.Position{})
				var diagnostic *sqlstate.Error
				var planning *plExpressionPlanningError
				if !errors.As(mapped, &diagnostic) || diagnostic.Context != tc.context {
					t.Fatal("CASE diagnostic ignored producer phase/context", diagnostic)
				}
				if errors.As(mapped, &planning) != tc.planning || !errors.Is(mapped, original) {
					t.Fatal("CASE diagnostic discarded producer phase or identity", mapped)
				}
			})
		}
	})
}
