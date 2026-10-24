package proc

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqltype"
)

func integerForUnitRun(t *testing.T, source string) (*walkHost, *PLRun) {
	t.Helper()
	d := walkUnitDefinition(t, source)
	h := newWalkHost(t)
	args := NewFrame(nil)
	f := NewPLFrame(d.PL, d, args, sqltype.CollationRef{})
	args.Close()
	t.Cleanup(f.Close)
	return h, &PLRun{Host: h, Definition: d, Frame: f}
}

func TestPLpgSQLIntegerForFrame(t *testing.T) {
	for _, tc := range []struct {
		name, bounds, body string
		want               int64
		found              bool
	}{
		{"sum", "1..3", "s:=s+i;", 6, true},
		{"reverse", "REVERSE 3..1", "s:=s*10+i;", 321, true},
		{"step", "1..10 BY 3", "s:=s+i;", 22, true},
		{"empty", "3..1", "s:=99;", 0, false},
		{"equal", "7..7", "s:=s+i;", 7, true},
		{"exit", "1..3", "s:=i; EXIT;", 1, true},
		{"continue", "1..3", "s:=s+i; CONTINUE; s:=99;", 6, true},
		{"return", "1..3", "RETURN i;", 1, true},
		{"independent", "1..3", "s:=s*10+i; i:=99;", 123, true},
		{"upper_limit", "2147483646..2147483647", "s:=s+1;", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, run := integerForUnitRun(t, "DECLARE s integer:=0; BEGIN FOR i IN "+tc.bounds+" LOOP "+tc.body+" END LOOP; RETURN s; END")
			loop := run.Definition.PL.Root.Statements[0].Block.Statements[0].IntegerFor
			value, flow, err := run.Block(run.Definition.PL.Root)
			got, _ := value.Integer()
			found, _ := walkFound(t, run).Boolean()
			if err != nil || flow.Kind != PLReturn || got != tc.want || found != tc.found || len(run.Frame.Scopes) != 0 || run.Frame.Values[loop.Counter].Type() != nil {
				t.Fatal("range result/lifetime", got, flow, err, found, len(run.Frame.Scopes))
			}
			run.Frame.Close()
			if still, _ := value.Integer(); still != tc.want {
				t.Fatal("frame close mutated result", still)
			}
		})
	}
	for _, tc := range []struct {
		name, bounds, body, code string
		flow                     PLFlowKind
	}{
		{"outward_exit", "1..3", "EXIT o;", "", PLExit},
		{"outward_continue", "1..3", "CONTINUE o;", "", PLContinue},
		{"error", "1..3", "RETURN 1/0;", "22012", PLNormal},
		{"invalid_step", "3..1 BY 0", "", "22023", PLNormal},
		{"null_lower", "NULL..3", "", "22004", PLNormal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, run := integerForUnitRun(t, "DECLARE i integer:=77; BEGIN <<o>> LOOP FOR i IN "+tc.bounds+" LOOP "+tc.body+" END LOOP; EXIT o; END LOOP; RETURN i; END")
			root := run.Definition.PL.Root
			body := root.Statements[0].Block
			outer := body.Statements[0].Loop
			in := outer.Statements[0]
			for _, scope := range []*PLBlock{root, body, outer} {
				run.Frame.Enter(scope)
			}
			original, _ := sqltype.Integer(h.integer, 77)
			run.Frame.Values[body.Bindings["i"]] = original
			_, flow, err := run.executePLIntegerFor(in)
			var state interface{ SQLState() string }
			if tc.code != "" {
				if !errors.As(err, &state) || state.SQLState() != tc.code {
					t.Fatal("error", err)
				}
			} else if err != nil || flow.Kind != tc.flow || flow.Target != outer.Control {
				t.Fatal("outward flow", flow, err)
			}
			if len(run.Frame.Scopes) != 3 || run.Frame.Values[in.IntegerFor.Counter].Type() != nil || run.Frame.Values[body.Bindings["i"]] != original {
				t.Fatal("walker leaked scope/counter or overwrote outer")
			}
			for _, scope := range []*PLBlock{outer, body, root} {
				run.Frame.Leave(scope)
			}
		})
	}
}

func TestPLpgSQLIntegerForCancellation(t *testing.T) {
	for _, body := range []string{"", "CONTINUE;"} {
		t.Run("body_"+body, func(t *testing.T) {
			h, run := integerForUnitRun(t, "BEGIN FOR i IN 1..3 LOOP "+body+" END LOOP; RETURN 0; END")
			counter := run.Definition.PL.Root.Statements[0].Block.Statements[0].IntegerFor.Counter
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := int64(0)
			h.ctx = &assignedCancelContext{Context: ctx, cancel: cancel, assigned: func() bool {
				v := run.Frame.Values[counter]
				if v.Type() == nil {
					return false
				}
				observed, _ = v.Integer()
				return true
			}}
			_, _, err := run.Block(run.Definition.PL.Root)
			var state interface{ SQLState() string }
			found, _ := walkFound(t, run).Boolean()
			if !errors.Is(err, context.Canceled) || errors.As(err, &state) || observed != 1 || found || run.Frame.Values[counter].Type() != nil || len(run.Frame.Scopes) != 0 {
				t.Fatal("deterministic iteration fence/cleanup", err, observed, found, len(run.Frame.Scopes))
			}
		})
	}
}
