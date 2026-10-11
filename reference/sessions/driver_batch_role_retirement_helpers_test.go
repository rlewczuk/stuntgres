package reference_test

import (
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func batchRetirementReject(t *testing.T, p *fixture.Pair, x fixture.Executor, q string) {
	t.Helper()
	r, err := x.ExecContext(p.Ctx, q)
	if r != nil || err == nil || err.Error() != "stuntgres: catalog replaced before publication" {
		t.Fatalf("retirement publication authority: result=%v error=%v", r, err)
	}
	var state interface{ SQLState() string }
	if errors.As(err, &state) && state.SQLState() != "" {
		t.Fatalf("native optimistic retirement failure invented SQLSTATE %q", state.SQLState())
	}
}

func batchRetirementRole(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, present bool) {
	t.Helper()
	n := int64(0)
	if present {
		n = 1
	}
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_user WHERE usename='batch_retired'", []string{"n"}, []string{"INT8"}, [][]any{{n}}))
}

func batchRetirementPeer(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, kind string, present bool) {
	t.Helper()
	if !present {
		if kind == "schema" {
			fixture.RunStep(t, p.Ctx, f.B, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='batch_retirement_peer_schema'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		} else {
			batchPolicyMissing(t, p, f.B, "retirement_peer")
		}
		return
	}
	if kind == "schema" {
		// An actual authorization consumer proves the owner still resolves and
		// owns the exact peer schema; no native pg_namespace inquiry is added.
		batchExec(t, p, b, f.B, "SET ROLE batch_retired", 0)
		batchExec(t, p, b, f.B, "CREATE TABLE batch_retirement_peer_schema.owner_guard(i INT)", 0)
		batchExec(t, p, b, f.B, "RESET ROLE", 0)
		batchExec(t, p, b, f.B, "INSERT INTO batch_retirement_peer_schema.owner_guard VALUES(41)", 1)
		batchValues(t, p, b, f.B, "SELECT i FROM batch_retirement_peer_schema.owner_guard", 41)
	} else {
		batchExec(t, p, b, f.B, "ALTER POLICY retirement_peer ON policy_t", 0)
		if b.PG {
			batchPolicyOID(t, p, f.B, "retirement_peer", "batch_retired")
		} else {
			batchPolicyDefinition(t, p, "retirement_peer", "batch_retired", "policy_t.id>0", "policy_t.allowed")
		}
		batchPolicyRows(t, p, b, f.B)
	}
}
