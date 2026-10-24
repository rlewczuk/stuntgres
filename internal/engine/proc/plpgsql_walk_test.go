package proc

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestPLRunControlFlow(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		want         int64
		state        string
	}{
		{"nested_transfers", "DECLARE n integer:=0; s integer:=0; BEGIN <<o>> LOOP n:=n+1; EXIT WHEN n>=4; <<i>> LOOP CONTINUE o WHEN n=2; EXIT i; END LOOP; s:=s+n; END LOOP; RETURN s; END", 4, ""},
		{"block_exit", "DECLARE n integer:=0; BEGIN <<b>> BEGIN n:=1; EXIT b; n:=2; END b; RETURN n; END", 1, ""},
		{"return_from_loop", "DECLARE n integer:=0; BEGIN LOOP n:=n+1; IF n>=3 THEN RETURN n; END IF; END LOOP; END", 3, ""},
		{"if_elsif_else", "DECLARE n integer:=2; BEGIN IF n=1 THEN RETURN 10; ELSIF n=2 THEN RETURN 20; ELSE RETURN 30; END IF; END", 20, ""},
		{"if_null_condition", "BEGIN IF NULL THEN RETURN 1; END IF; RETURN 2; END", 2, ""},
		{"searched_case", "DECLARE n integer:=3; BEGIN CASE WHEN n=1 THEN RETURN 10; WHEN n=3 THEN RETURN 30; END CASE; RETURN 0; END", 30, ""},
		{"case_not_found", "BEGIN CASE WHEN false THEN RETURN 1; END CASE; RETURN 2; END", 0, "20000"},
		{"handler_matched", "DECLARE n integer:=0; BEGIN BEGIN n:=1; n:=1/0; EXCEPTION WHEN division_by_zero THEN RETURN n+10; END; END", 11, ""},
		{"handler_unmatched", "BEGIN BEGIN RETURN 1/0; EXCEPTION WHEN unique_violation THEN RETURN 1; END; END", 0, "22012"},
		{"missing_return", "DECLARE n integer:=0; BEGIN n:=1; END", 0, "2F005"},
		{"not_null_initializer", "DECLARE n integer NOT NULL:=NULL; BEGIN RETURN 1; END", 0, "22004"},
		{"rethrow_outside_handler", "BEGIN RAISE; END", 0, "0Z002"},
		{"rethrow_in_handler", "BEGIN BEGIN RETURN 1/0; EXCEPTION WHEN OTHERS THEN RAISE; END; END", 0, "22012"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newWalkHost(t)
			d := walkUnitDefinition(t, tc.source)
			value, err := (&PLRun{Host: h, Definition: d}).Invoke(NewFrame(nil), sqltype.CollationRef{})
			if got := walkState(err); got != tc.state {
				t.Fatalf("SQLSTATE %q, want %q: %v", got, tc.state, err)
			}
			if tc.state != "" {
				return
			}
			if got, err := value.Integer(); err != nil || got != tc.want {
				t.Fatal("result", got, tc.want, err)
			}
		})
	}
}

func TestPLRunExceptionBoundary(t *testing.T) {
	const source = "BEGIN BEGIN RETURN 1/0; EXCEPTION WHEN OTHERS THEN RETURN 7; END; END"
	t.Run("rollback_then_handler", func(t *testing.T) {
		h := newWalkHost(t)
		value, err := (&PLRun{Host: h, Definition: walkUnitDefinition(t, source)}).Invoke(NewFrame(nil), sqltype.CollationRef{})
		if got, _ := value.Integer(); err != nil || got != 7 {
			t.Fatal("handler result", got, err)
		}
		want := []string{"begin", "evaluate at RETURN", "end rollback=true", "evaluate at RETURN", "convert at RETURN"}
		if !reflect.DeepEqual(h.calls, want) {
			t.Fatal("boundary order", h.calls)
		}
	})
	t.Run("success_releases", func(t *testing.T) {
		h := newWalkHost(t)
		run := walkUnitRun(t, h, "BEGIN BEGIN RETURN 5; EXCEPTION WHEN OTHERS THEN RETURN 7; END; END")
		value, flow, err := run.Block(run.Definition.PL.Root)
		if got, _ := value.Integer(); err != nil || flow.Kind != PLReturn || got != 5 || h.calls[len(h.calls)-1] != "end rollback=false" {
			t.Fatal("protected RETURN", got, flow, err, h.calls)
		}
	})
	t.Run("cleanup_failure_skips_handler", func(t *testing.T) {
		h := newWalkHost(t)
		h.endErr = errors.New("cleanup veto")
		_, err := (&PLRun{Host: h, Definition: walkUnitDefinition(t, source)}).Invoke(NewFrame(nil), sqltype.CollationRef{})
		var cleanup *PLExceptionCleanupError
		if !errors.As(err, &cleanup) || !errors.Is(err, h.endErr) || walkState(err) != "22012" || h.calls[len(h.calls)-1] != "end rollback=true" {
			t.Fatal("cleanup failure", err, h.calls)
		}
	})
	t.Run("begin_failure_skips_body", func(t *testing.T) {
		h := newWalkHost(t)
		h.beginErr = errors.New("savepoint failure")
		_, err := (&PLRun{Host: h, Definition: walkUnitDefinition(t, source)}).Invoke(NewFrame(nil), sqltype.CollationRef{})
		if !errors.Is(err, h.beginErr) || !reflect.DeepEqual(h.calls, []string{"begin"}) {
			t.Fatal("begin failure", err, h.calls)
		}
	})
	t.Run("cancellation_not_caught", func(t *testing.T) {
		h := newWalkHost(t)
		ctx, cancel := context.WithCancel(context.Background())
		h.ctx = ctx
		h.onEvaluate = func(*PLFrame) { cancel() }
		_, err := (&PLRun{Host: h, Definition: walkUnitDefinition(t, "DECLARE n integer:=0; BEGIN BEGIN n:=1; n:=2; EXCEPTION WHEN OTHERS THEN RETURN 7; END; RETURN n; END")}).Invoke(NewFrame(nil), sqltype.CollationRef{})
		if !errors.Is(err, context.Canceled) || walkState(err) != "" {
			t.Fatal("OTHERS caught cancellation", err, h.calls)
		}
	})
}

func TestPLRunOrigins(t *testing.T) {
	h := newWalkHost(t)
	const source = "DECLARE n integer:=1; BEGIN n:=n+1; IF n=2 THEN RAISE NOTICE 'x'; END IF; LOOP EXIT WHEN true; END LOOP; ASSERT true; RETURN n; END"
	if _, err := (&PLRun{Host: h, Definition: walkUnitDefinition(t, source)}).Invoke(NewFrame(nil), sqltype.CollationRef{}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"evaluate " + plInitializerAction, "convert " + plInitializerAction,
		"evaluate at assignment", "convert at assignment",
		"evaluate at IF", "convert at IF", "raise",
		"evaluate at EXIT", "convert at EXIT",
		"assert",
		"evaluate at RETURN", "convert at RETURN",
	}
	if !reflect.DeepEqual(h.calls, want) {
		t.Fatalf("host calls\n got %q\nwant %q", h.calls, want)
	}
}

func TestPLRunLoopCancellation(t *testing.T) {
	for _, source := range []string{"BEGIN LOOP END LOOP; END", "BEGIN LOOP CONTINUE; END LOOP; END"} {
		h := newWalkHost(t)
		ctx, cancel := context.WithCancel(context.Background())
		h.ctx = &loopCancelContext{Context: ctx, cancel: cancel, after: 3}
		_, err := (&PLRun{Host: h, Definition: walkUnitDefinition(t, source)}).Invoke(NewFrame(nil), sqltype.CollationRef{})
		cancel()
		if !errors.Is(err, context.Canceled) || walkState(err) != "" {
			t.Fatal("empty loop cancellation", source, err)
		}
	}
}

// loopCancelContext cancels itself on its after-th Err check.
type loopCancelContext struct {
	context.Context
	cancel context.CancelFunc
	after  int
}

func (c *loopCancelContext) Err() error {
	c.after--
	if c.after < 0 {
		c.cancel()
	}
	return c.Context.Err()
}
