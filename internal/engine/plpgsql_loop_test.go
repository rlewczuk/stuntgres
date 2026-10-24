package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func loopUnitFrame(t *testing.T, source string) (*StatementScope, *catalog.Procedure, *proc.PLFrame, catalog.Read) {
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
			value, flow, err := s.newPLRun(d, f, read).Block(d.SQL.PL.Root)
			if err != nil || flow.Kind != proc.PLReturn {
				t.Fatal("loop flow failed", flow, err)
			}
			if len(f.Scopes) != 0 || f.HandledError() != nil || s.sqlRoutine.lexical != nil || s.activeSQLRoutineDepth != 0 {
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
		ctx, cancel := context.WithCancel(s.op.Context())
		s.op.Ctx = loopFrameCancel{Context: ctx, cancel: cancel, frame: f}
		_, flow, err := s.newPLRun(d, f, read).Block(d.SQL.PL.Root)
		cancel()
		if !errors.Is(err, context.Canceled) || flow.Kind != proc.PLNormal {
			t.Fatal("active loop cancellation lost", flow, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary cancellation acquired SQLSTATE", err)
		}
		if len(f.Scopes) != 0 || f.HandledError() != nil || s.sqlRoutine.lexical != nil || s.activeSQLRoutineDepth != 0 {
			t.Fatal("canceled loop leaked private/root state")
		}
	}
}
