package engine

import (
	"context"
	"errors"
	"testing"

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
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(s.Op)
			if err == nil {
				err = e.CompilePLRoutineLocked(s.Op, auth, d)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			input := proc.NewFrame(nil)
			defer input.Close()
			f := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
			defer f.Close()
			if tc.cancel {
				ctx, cancel := context.WithCancel(s.Op.Ctx)
				defer cancel()
				s.Op.Ctx = conditionalFrameCancel{Context: ctx, cancel: cancel, frame: f}
			}
			value, flow, err := ScopeNewPLRun(s, d, f, read).Block(d.SQL.PL.Root)
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
				if err != nil || failure != nil || flow.Kind != proc.PLReturn || n != 7 {
					t.Fatal("nested RETURN did not propagate", value, flow, err)
				}
			}
			if len(f.Scopes) != 0 || RoutineDemandLexical(ScopeSQLRoutineRef(s)) != nil || ScopeActiveSQLRoutineDepth(s) != 0 {
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
		e.Mu.Lock()
		binder := NewExpressionBinder(s.Op.Context(), e, s.Input, s.Op, nil)
		binder.PL = frame
		node, err := binder.Column(raw)
		e.Mu.Unlock()
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
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: s.Op.Context(), Host: HostOf(s), Catalog: e.CatalogRead(), Input: binder.Input, Output: s.Output}}
		before, err := local.Eval(&evaluation)
		if err != nil || before.Type() != varchar || !before.IsNull() || local.ResultUsage() != usage {
			t.Fatalf("captured search before frame close: value=%#v usage=%#v err=%v", before, local.ResultUsage(), err)
		}
		evaluated, effective, present, err := ScopeEvaluatePLExpression(s, frame, &parser.Select{Targets: []parser.Expr{raw}}, e.CatalogRead(), nil, NewPLNoticeOrigin(PLNoticeOriginFields{}))
		if err != nil || !present || effective != usage || evaluated.Type() != varchar || !evaluated.IsNull() {
			t.Fatalf("expression result lost full Usage/presence: value=%#v usage=%#v present=%t err=%v", evaluated, effective, present, err)
		}
		frame.Close()
		value, err := local.Eval(&evaluation)
		if err != nil || value.Type() != varchar || !value.IsNull() || local.ResultUsage() != usage {
			t.Fatalf("captured search after frame close: value=%#v usage=%#v err=%v", value, local.ResultUsage(), err)
		}
		e.Mu.Lock()
		_, err = binder.Column(raw)
		e.Mu.Unlock()
		if err == nil {
			t.Fatal("inactive private binding admitted")
		}
		for _, parts := range [][]parser.Identifier{
			{{Text: "\x00case:00"}},
			{{Text: "\x00case:1"}},
			{{Text: "ordinary"}, {Text: proc.PLSearchName(0)}},
		} {
			e.Mu.Lock()
			_, err := binder.Column(parser.ColumnExpr{Name: parser.ObjectName{Parts: parts}})
			e.Mu.Unlock()
			if err == nil {
				t.Fatal("noncanonical or qualified private binding admitted", parts)
			}
		}
	})
}
