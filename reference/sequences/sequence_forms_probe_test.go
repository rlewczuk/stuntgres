//go:build reference

package reference_test

import (
	"context"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSequenceProbeNotices(t *testing.T) {
	p := fixture.NewPair(t)
	f := fixture.NewControlsReferenceFixture(t, p, true)
	fixture.SequenceProbeExec(t, p.Ctx, f.A, "CREATE TABLE t(v INT)")
	for _, tc := range []struct{ q, code, msg string }{
		{"CREATE SEQUENCE IF NOT EXISTS t", "42P07", `relation "t" already exists, skipping`},
		{"ALTER SEQUENCE IF EXISTS missing RESTART", "00000", `relation "missing" does not exist, skipping`},
		{"DROP SEQUENCE IF EXISTS missing", "00000", `sequence "missing" does not exist, skipping`},
	} {
		fixture.SequenceProbeExec(t, p.Ctx, f.A, tc.q)
		want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: tc.code, Message: tc.msg}
		if !reflect.DeepEqual(*f.Notices, []fixture.Diagnostic{want}) {
			t.Fatalf("notice got %#v want %#v", *f.Notices, want)
		}
		*f.Notices = nil
	}
}

func TestReferenceSequenceProbeACLAndLinkedOwner(t *testing.T) {
	p := fixture.NewPair(t)
	a := sequenceProbeConn(t, p)
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE ROLE sequence_probe_owner LOGIN")
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE ROLE sequence_probe_reader LOGIN")
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []string{"DROP OWNED BY sequence_probe_owner,sequence_probe_reader CASCADE", "DROP ROLE sequence_probe_owner,sequence_probe_reader"} {
			if _, err := fixture.Admin.ExecContext(ctx, q); err != nil {
				t.Error(q, err)
			}
		}
	})
	fixture.SequenceProbeExec(t, p.Ctx, a, "GRANT USAGE,CREATE ON SCHEMA public TO sequence_probe_owner,sequence_probe_reader")
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SEQUENCE s")
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE t(v INT)")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s OWNER TO sequence_probe_owner")
	fixture.SequenceProbeError(t, p.Ctx, a, "ALTER SEQUENCE s OWNED BY t.v", nil)
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER TABLE t OWNER TO sequence_probe_owner")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s OWNED BY t.v")
	fixture.SequenceProbeError(t, p.Ctx, a, "ALTER SEQUENCE s OWNER TO sequence_probe_reader", nil)
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE SCHEMA other")
	defer fixture.SequenceProbeExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION; DROP SCHEMA other CASCADE")
	fixture.SequenceProbeExec(t, p.Ctx, a, "CREATE TABLE other.t(v INT)")
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER TABLE other.t OWNER TO sequence_probe_owner")
	fixture.SequenceProbeError(t, p.Ctx, a, "ALTER SEQUENCE s OWNED BY other.t.v", nil)
	fixture.SequenceProbeExec(t, p.Ctx, a, "ALTER SEQUENCE s OWNED BY NONE")
	// Privileges are setup-only SQL in the oracle, never an invented native API.
	denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for sequence s")
	for _, mask := range []string{"USAGE", "SELECT", "UPDATE"} {
		fixture.SequenceProbeExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION")
		fixture.SequenceProbeExec(t, p.Ctx, a, "REVOKE ALL ON SEQUENCE s FROM sequence_probe_reader")
		fixture.SequenceProbeExec(t, p.Ctx, a, "GRANT "+mask+" ON SEQUENCE s TO sequence_probe_reader")
		fixture.SequenceProbeExec(t, p.Ctx, a, "SET SESSION AUTHORIZATION sequence_probe_reader")
		if mask == "SELECT" {
			fixture.SequenceProbeError(t, p.Ctx, a, "SELECT nextval('s')", &denied)
		} else {
			var v int64
			if err := a.QueryRowContext(p.Ctx, "SELECT nextval('s')").Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range []string{"SELECT currval('s')", "SELECT lastval()"} {
			if mask == "UPDATE" {
				fixture.SequenceProbeError(t, p.Ctx, a, q, &denied)
			} else {
				var v int64
				if err := a.QueryRowContext(p.Ctx, q).Scan(&v); err != nil {
					t.Fatal(q, err)
				}
			}
		}
		if mask == "UPDATE" {
			fixture.SequenceProbeValue(t, p.Ctx, a, "SELECT setval('s',100)", 100)
		} else {
			fixture.SequenceProbeError(t, p.Ctx, a, "SELECT setval('s',100)", &denied)
		}
		if mask == "SELECT" {
			var v int64
			if err := a.QueryRowContext(p.Ctx, "SELECT last_value FROM s").Scan(&v); err != nil {
				t.Fatal(err)
			}
		} else {
			fixture.SequenceProbeError(t, p.Ctx, a, "SELECT * FROM s", &denied)
		}
	}
	fixture.SequenceProbeExec(t, p.Ctx, a, "RESET SESSION AUTHORIZATION")
}
