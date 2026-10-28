package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

type logicalDecodingSignature struct {
	name string
	oid int64
	data string
	dataOID string
}

var logicalDecodingSignatures = []logicalDecodingSignature{
	{"pg_logical_slot_get_changes", 3782, "TEXT", "25"},
	{"pg_logical_slot_get_binary_changes", 3783, "BYTEA", "17"},
	{"pg_logical_slot_peek_changes", 3784, "TEXT", "25"},
	{"pg_logical_slot_peek_binary_changes", 3785, "BYTEA", "17"},
}

func logicalDecodingCases(t *testing.T, body func(*fixture.RoleMembershipScenario, *sql.DB)) {
	t.Helper()
	predefinedCases(t, []string{"ld_actor", "ld_repl", "ld_member"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		if s.Backend.PG {
			var level string
			var slots int
			if err := s.C.QueryRowContext(s.P.Ctx, "SELECT current_setting('wal_level'),current_setting('max_replication_slots')::integer").Scan(&level, &slots); err != nil {
				s.T.Fatal(err)
			}
			if level != "logical" || slots <= 0 {
				s.T.Fatalf("logical decoding oracle prerequisites: wal_level=%s max_replication_slots=%d", level, slots)
			}
		}
		body(s, db)
	})
}

func logicalDecodingMissing(name string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "42704", Message: "replication slot \"" + name + "\" does not exist"}
}

func logicalDecodingDenied() fixture.Diagnostic {
	return fixture.Diagnostic{Code: "42501", Message: "permission denied to use replication slots", Detail: "Only roles with the REPLICATION attribute may use replication slots."}
}

func logicalDecodingFailure(s *fixture.RoleMembershipScenario, q string, d fixture.Diagnostic) {
	s.T.Helper()
	predefinedFailure(s, q, d, true, "execute")
}

func logicalDecodingHeldFailure(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, d fixture.Diagnostic, args ...any) {
	s.T.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	step := fixture.ErrorDiagnostic(held.SQL, d, args...)
	step.SelectRows, step.ErrorStage = true, "execute"
	fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
}

func logicalDecodingIdentity(s *fixture.RoleMembershipScenario, sig logicalDecodingSignature) {
	s.T.Helper()
	identity := sig.name + "(name,pg_lsn,integer,text[])"
	predefinedSelect(s, "SELECT " + fileListLiteral(identity) + "::regprocedure::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{sig.oid}})
	q := "SELECT proisstrict,provolatile,proparallel,proretset,pronargdefaults,provariadic::bigint,prorettype::bigint FROM pg_proc WHERE oid=" + strconv.FormatInt(sig.oid, 10)
	predefinedSelect(s, q, []string{"proisstrict", "provolatile", "proparallel", "proretset", "pronargdefaults", "provariadic", "prorettype"}, []string{"BOOL", "CHAR", "CHAR", "BOOL", "INT2", "INT8", "INT8"}, [][]any{{false, "v", "u", true, int64(1), int64(25), int64(2249)}})
}
