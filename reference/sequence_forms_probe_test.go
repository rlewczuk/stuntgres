//go:build reference

package reference_test

import (
	"context"
	"reflect"
	"testing"
)

func TestReferenceSequenceProbeNotices(t *testing.T) {
	p := newReferencePair(t)
	f := newControlsReferenceFixture(t, p, true)
	sequenceProbeExec(t, p.ctx, f.a, "CREATE TABLE t(v INT)")
	for _, tc := range []struct{ q, code, msg string }{
		{"CREATE SEQUENCE IF NOT EXISTS t", "42P07", `relation "t" already exists, skipping`},
		{"ALTER SEQUENCE IF EXISTS missing RESTART", "00000", `relation "missing" does not exist, skipping`},
		{"DROP SEQUENCE IF EXISTS missing", "00000", `sequence "missing" does not exist, skipping`},
	} {
		sequenceProbeExec(t, p.ctx, f.a, tc.q)
		want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: tc.code, Message: tc.msg}
		if !reflect.DeepEqual(*f.notices, []referenceDiagnostic{want}) {
			t.Fatalf("notice got %#v want %#v", *f.notices, want)
		}
		*f.notices = nil
	}
}

func TestReferenceSequenceProbeACLAndLinkedOwner(t *testing.T) {
	p := newReferencePair(t)
	a := sequenceProbeConn(t, p)
	sequenceProbeExec(t, p.ctx, a, "CREATE ROLE sequence_probe_owner LOGIN")
	sequenceProbeExec(t, p.ctx, a, "CREATE ROLE sequence_probe_reader LOGIN")
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{"DROP OWNED BY sequence_probe_owner,sequence_probe_reader CASCADE", "DROP ROLE sequence_probe_owner,sequence_probe_reader"} {
			if _, err := referenceAdmin.ExecContext(ctx, q); err != nil {
				t.Error(q, err)
			}
		}
	})
	sequenceProbeExec(t, p.ctx, a, "GRANT USAGE,CREATE ON SCHEMA public TO sequence_probe_owner,sequence_probe_reader")
	sequenceProbeExec(t, p.ctx, a, "CREATE SEQUENCE s")
	sequenceProbeExec(t, p.ctx, a, "CREATE TABLE t(v INT)")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s OWNER TO sequence_probe_owner")
	sequenceProbeError(t, p.ctx, a, "ALTER SEQUENCE s OWNED BY t.v", nil)
	sequenceProbeExec(t, p.ctx, a, "ALTER TABLE t OWNER TO sequence_probe_owner")
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s OWNED BY t.v")
	sequenceProbeError(t, p.ctx, a, "ALTER SEQUENCE s OWNER TO sequence_probe_reader", nil)
	sequenceProbeExec(t, p.ctx, a, "CREATE SCHEMA other")
	defer sequenceProbeExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION; DROP SCHEMA other CASCADE")
	sequenceProbeExec(t, p.ctx, a, "CREATE TABLE other.t(v INT)")
	sequenceProbeExec(t, p.ctx, a, "ALTER TABLE other.t OWNER TO sequence_probe_owner")
	sequenceProbeError(t, p.ctx, a, "ALTER SEQUENCE s OWNED BY other.t.v", nil)
	sequenceProbeExec(t, p.ctx, a, "ALTER SEQUENCE s OWNED BY NONE")
	// Privileges are setup-only SQL in the oracle, never an invented native API.
	denied := controlsReferenceDiagnostic("42501", "permission denied for sequence s")
	for _, mask := range []string{"USAGE", "SELECT", "UPDATE"} {
		sequenceProbeExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION")
		sequenceProbeExec(t, p.ctx, a, "REVOKE ALL ON SEQUENCE s FROM sequence_probe_reader")
		sequenceProbeExec(t, p.ctx, a, "GRANT "+mask+" ON SEQUENCE s TO sequence_probe_reader")
		sequenceProbeExec(t, p.ctx, a, "SET SESSION AUTHORIZATION sequence_probe_reader")
		if mask == "SELECT" {
			sequenceProbeError(t, p.ctx, a, "SELECT nextval('s')", &denied)
		} else {
			var v int64
			if err := a.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{"SELECT currval('s')", "SELECT lastval()"} {
			if mask == "UPDATE" {
				sequenceProbeError(t, p.ctx, a, q, &denied)
			} else {
				var v int64
				if err := a.QueryRowContext(p.ctx, q).Scan(&v); err != nil {
					t.Fatal(q, err)
				}
			}
		}
		if mask == "UPDATE" {
			sequenceProbeValue(t, p.ctx, a, "SELECT setval('s',100)", 100)
		} else {
			sequenceProbeError(t, p.ctx, a, "SELECT setval('s',100)", &denied)
		}
		if mask == "SELECT" {
			var v int64
			if err := a.QueryRowContext(p.ctx, "SELECT last_value FROM s").Scan(&v); err != nil {
				t.Fatal(err)
			}
		} else {
			sequenceProbeError(t, p.ctx, a, "SELECT * FROM s", &denied)
		}
	}
	sequenceProbeExec(t, p.ctx, a, "RESET SESSION AUTHORIZATION")
}
