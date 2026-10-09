package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

// Failed CHECK executions must reach Execute, expose no Result, and retain the
// entire logical diagnostic. In particular, never execute twice to record it.
func referenceCheckRun(t *testing.T, ctx context.Context, x referenceExecutor, rec *referenceRecorder, s referenceStep) referenceObserved {
	t.Helper()
	if s.state == "" || s.selectRows {
		return referenceRunStep(t, ctx, x, rec, s)
	}
	ctx = context.WithValue(ctx, referenceRecorderKey{}, rec)
	var result sql.Result
	var err error
	if s.prepared {
		stmt, e := x.PrepareContext(ctx, s.query)
		if e != nil {
			t.Fatalf("CHECK Prepare must succeed: %v", e)
		}
		defer func() {
			if e := stmt.Close(); e != nil {
				t.Error(e)
			}
		}()
		result, err = stmt.ExecContext(ctx, s.args...)
	} else {
		result, err = x.ExecContext(ctx, s.query, s.args...)
	}
	if result != nil {
		t.Fatal("failed CHECK execution exposed Result", result)
	}
	state := referenceState(t, err, s.state)
	diagnostic := referenceCheckDiagnostic(t, err, s, "execute")
	return referenceObserved{state: state, diagnostic: diagnostic, errorStage: "execute"}
}

func referenceCheckDump(t *testing.T, p *referencePair) []byte {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	return dump.Bytes()
}

func referenceCheckRoundtrip(t *testing.T, p *referencePair, dump []byte) {
	t.Helper()
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	if again := referenceCheckDump(t, p); !bytes.Equal(dump, again) {
		t.Fatal("CHECK replay changed definitions/data/provider state", string(dump), string(again))
	}
}
