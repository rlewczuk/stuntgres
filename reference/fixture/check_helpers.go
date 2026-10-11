package fixture

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

// Failed CHECK executions must reach Execute, expose no Result, and retain the
// entire logical diagnostic. In particular, never execute twice to record it.
func CheckRun(t *testing.T, ctx context.Context, x Executor, rec *Recorder, s Step) Observed {
	t.Helper()
	if s.State == "" || s.SelectRows {
		return RunStep(t, ctx, x, rec, s)
	}
	ctx = context.WithValue(ctx, RecorderKey{}, rec)
	var result sql.Result
	var err error
	if s.Prepared {
		stmt, e := x.PrepareContext(ctx, s.Query)
		if e != nil {
			t.Fatalf("CHECK Prepare must succeed: %v", e)
		}
		defer func() {
			if e := stmt.Close(); e != nil {
				t.Error(e)
			}
		}()
		result, err = stmt.ExecContext(ctx, s.Args...)
	} else {
		result, err = x.ExecContext(ctx, s.Query, s.Args...)
	}
	if result != nil {
		t.Fatal("failed CHECK execution exposed Result", result)
	}
	state := State(t, err, s.State)
	diagnostic := CheckDiagnostic(t, err, s, "execute")
	return Observed{state: state, Diagnostic: diagnostic, ErrorStage: "execute"}
}

func CheckDump(t *testing.T, p *Pair) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.Bytes()
}

func CheckRoundtrip(t *testing.T, p *Pair, dump []byte) {
	t.Helper()
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if again := CheckDump(t, p); !bytes.Equal(dump, again) {
		t.Fatal("CHECK replay changed definitions/data/provider state", string(dump), string(again))
	}
}
