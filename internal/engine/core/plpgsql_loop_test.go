package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func loopUnitFrame(t *testing.T, source string) (*core.StatementScope, *catalog.Procedure, *proc.PLFrame, catalog.Read) {
	t.Helper()
	e, s := plExceptionUnitScope(t)
	read := e.CatalogRead()
	integer, _ := read.LookupType(sqltype.Int4)
	language, err := catalog.LookupLanguage(read, "plpgsql")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	d := &catalog.Procedure{ObjectName: "loop_unit", Language: language.Address(), SQL: &proc.Definition{Kind: proc.PLpgSQLBody, Name: "loop_unit", Source: source, Result: integer.Usage(), PLSyntax: raw}}
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
	t.Cleanup(input.Close)
	frame := proc.NewPLFrame(d.SQL.PL, d.SQL, input, sqltype.CollationRef{})
	t.Cleanup(frame.Close)
	return s, d, frame, read
}

func TestPLpgSQLLoopFrame(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         int64
	}{
		{"reentry", "DECLARE n integer:=0; s integer:=0; BEGIN <<o>> LOOP n:=n+1; EXIT WHEN n>3; DECLARE x integer:=10; BEGIN x:=x+1; s:=s+x; CONTINUE o; END; END LOOP; RETURN s; END", 33},
		{"block_exit", "DECLARE n integer:=0; BEGIN <<b>> BEGIN LOOP n:=n+1; EXIT b WHEN n=2; END LOOP; n:=99; END b; RETURN n; END", 2},
		{"handler_continue", "DECLARE n integer:=0; BEGIN <<o>> LOOP n:=n+1; BEGIN n:=1/0; EXCEPTION WHEN division_by_zero THEN CONTINUE o WHEN n<3; EXIT o; END; RETURN 99; END LOOP; RETURN n; END", 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d, f, read := loopUnitFrame(t, tc.source)
			value, flow, err := core.ScopeNewPLRun(s, d, f, read).Block(d.SQL.PL.Root)
			if err != nil || flow.Kind != proc.PLReturn {
				t.Fatal("loop flow failed", flow, err)
			}
			if len(f.Scopes) != 0 || f.HandledError() != nil || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil || core.ScopeActiveSQLRoutineDepth(s) != 0 {
				t.Fatal("loop transfer retained scope/handler/query authority")
			}
			for slot, v := range f.Values {
				if slot != f.Program.FoundSlot && v.Type() != nil {
					t.Fatal("unwound local retained value")
				}
			}
			f.Close()
			got, err := value.Integer()
			if err != nil || got != tc.want {
				t.Fatal("returned immutable result lost on frame close", got, tc.want, err)
			}
		})
	}
}

type loopFrameCancel struct {
	context.Context
	cancel context.CancelFunc
	frame  *proc.PLFrame
}

func (c loopFrameCancel) Err() error {
	for _, scope := range c.frame.Scopes {
		if scope.Control != nil && scope.Control.Loop {
			c.cancel()
			break
		}
	}
	return c.Context.Err()
}

func TestPLpgSQLLoopCancellation(t *testing.T) {
	for _, source := range []string{"BEGIN LOOP END LOOP; RETURN 7; END", "BEGIN LOOP CONTINUE; END LOOP; RETURN 7; END"} {
		s, d, f, read := loopUnitFrame(t, source)
		ctx, cancel := context.WithCancel(s.Op.Context())
		s.Op.Ctx = loopFrameCancel{Context: ctx, cancel: cancel, frame: f}
		_, flow, err := core.ScopeNewPLRun(s, d, f, read).Block(d.SQL.PL.Root)
		cancel()
		if !errors.Is(err, context.Canceled) || flow.Kind != proc.PLNormal {
			t.Fatal("active loop cancellation lost", flow, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary cancellation acquired SQLSTATE", err)
		}
		if len(f.Scopes) != 0 || f.HandledError() != nil || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil || core.ScopeActiveSQLRoutineDepth(s) != 0 {
			t.Fatal("canceled loop leaked private/root state")
		}
	}
}

func TestPLpgSQLWhileFrame(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         int64
		fail         bool
	}{
		{"false", "DECLARE n integer:=0; BEGIN WHILE false LOOP n:=1; END LOOP; RETURN n; END", 0, false},
		{"null", "DECLARE n integer:=0; BEGIN WHILE NULL::boolean LOOP n:=1; END LOOP; RETURN n; END", 0, false},
		{"reevaluation", "DECLARE n integer:=0; BEGIN WHILE n<3 LOOP n:=n+1; CONTINUE; END LOOP; RETURN n; END", 3, false},
		{"block_reentry", "DECLARE n integer:=0; s integer:=0; BEGIN <<o>> WHILE n<3 LOOP n:=n+1; DECLARE x integer:=10; BEGIN x:=x+1; s:=s+x; CONTINUE o; END; END LOOP; RETURN s; END", 33, false},
		{"outward_exit", "DECLARE n integer:=0; BEGIN <<b>> BEGIN WHILE true LOOP n:=n+1; EXIT b WHEN n=2; END LOOP; n:=99; END b; RETURN n; END", 2, false},
		{"return", "BEGIN WHILE true LOOP RETURN 7; END LOOP; RETURN 99; END", 7, false},
		{"handler_continue", "DECLARE n integer:=0; BEGIN <<o>> WHILE n<3 LOOP n:=n+1; BEGIN n:=1/0; EXCEPTION WHEN division_by_zero THEN CONTINUE o; END; RETURN 99; END LOOP; RETURN n; END", 3, false},
		{"handled_condition", "DECLARE n integer:=0; BEGIN BEGIN WHILE 1/0=0 LOOP END LOOP; EXCEPTION WHEN division_by_zero THEN n:=7; END; RETURN n; END", 7, false},
		{"condition_error", "BEGIN WHILE 1/0=0 LOOP END LOOP; RETURN 7; END", 0, true},
		{"conversion_error", "BEGIN WHILE 'nope'::text LOOP END LOOP; RETURN 7; END", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, d, f, read := loopUnitFrame(t, tc.source)
			value, flow, err := core.ScopeNewPLRun(s, d, f, read).Block(d.SQL.PL.Root)
			if tc.fail {
				if err == nil || flow.Kind != proc.PLNormal {
					t.Fatal("condition failure lost", flow, err)
				}
			} else if err != nil || flow.Kind != proc.PLReturn {
				t.Fatal("WHILE flow failed", flow, err)
			}
			if len(f.Scopes) != 0 || f.HandledError() != nil || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil || core.ScopeActiveSQLRoutineDepth(s) != 0 {
				t.Fatal("WHILE retained lexical/handler/query authority")
			}
			for slot, v := range f.Values {
				if slot != f.Program.FoundSlot && v.Type() != nil {
					t.Fatal("WHILE retained unwound local value")
				}
			}
			f.Close()
			if !tc.fail {
				got, err := value.Integer()
				if err != nil || got != tc.want {
					t.Fatal("immutable WHILE result lost after frame close", got, err)
				}
			}
		})
	}
	for _, source := range []string{"BEGIN WHILE true LOOP END LOOP; RETURN 7; END", "BEGIN WHILE true LOOP CONTINUE; END LOOP; RETURN 7; END"} {
		s, d, f, read := loopUnitFrame(t, source)
		ctx, cancel := context.WithCancel(s.Op.Context())
		s.Op.Ctx = loopFrameCancel{Context: ctx, cancel: cancel, frame: f}
		_, flow, err := core.ScopeNewPLRun(s, d, f, read).Block(d.SQL.PL.Root)
		cancel()
		if !errors.Is(err, context.Canceled) || flow.Kind != proc.PLNormal {
			t.Fatal("WHILE cancellation lost", flow, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("WHILE cancellation acquired SQLSTATE", err)
		}
		if len(f.Scopes) != 0 || f.HandledError() != nil || core.RoutineDemandLexical(core.ScopeSQLRoutineRef(s)) != nil || core.ScopeActiveSQLRoutineDepth(s) != 0 {
			t.Fatal("canceled WHILE retained frame authority")
		}
	}
}
