package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

var walRoleNames = []string{"pg_available_wal_summaries", "pg_wal_summary_contents", "pg_get_wal_summarizer_state"}
var walRoleSignatures = []string{"pg_available_wal_summaries()", "pg_wal_summary_contents(bigint,pg_lsn,pg_lsn)", "pg_get_wal_summarizer_state()"}
var walRoleColumns = [][]string{{"tli", "start_lsn", "end_lsn"}, {"relfilenode", "reltablespace", "reldatabase", "relforknumber", "relblocknumber", "is_limit_block"}, {"summarized_tli", "summarized_lsn", "pending_lsn", "summarizer_pid"}}
var walRoleTypes = [][]string{{"INT8", "PG_LSN", "PG_LSN"}, {"OID", "OID", "OID", "INT2", "INT8", "BOOL"}, {"INT8", "PG_LSN", "PG_LSN", "INT4"}}

func walRoleCases(t *testing.T, body func(*fixture.RoleMembershipScenario, *sql.DB)) {
	t.Helper()
	predefinedCases(t, []string{"wals_actor"}, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		if s.Backend.PG {
			var originals [3]sql.NullString
			for i := range originals {
				if err := s.C.QueryRowContext(s.P.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=$1", 6321+i).Scan(&originals[i]); err != nil {
					s.T.Fatal(err)
				}
			}
			defer func() {
				if _, err := s.C.ExecContext(s.P.Ctx, "RESET ROLE"); err != nil {
					s.T.Error(err)
				}
				if _, err := s.C.ExecContext(s.P.Ctx, "DROP OWNED BY wals_actor CASCADE"); err != nil {
					s.T.Error(err)
				}
				for i, raw := range originals {
					var value any
					if raw.Valid {
						value = raw.String
					}
					if _, err := s.C.ExecContext(s.P.Ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=$2", value, 6321+i); err != nil {
						s.T.Error(err)
					}
				}
			}()
		}
		body(s, db)
	})
}
func walRoleFailure(q, code, message string, args ...any) fixture.Step {
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
	step.SelectRows = true
	step.ErrorStage = "execute"
	return step
}
func walRoleOutcome(s *fixture.RoleMembershipScenario, i int, q string, args ...any) fixture.Step {
	step := fixture.Select(q, walRoleColumns[i], walRoleTypes[i], nil, args...)
	if i == 1 {
		if len(args) > 0 && args[0] == nil {
			return step
		}
		if len(args) > 0 && args[0] == int64(0) {
			return walRoleFailure(q, "22023", "invalid timeline 0", args...)
		}
		if s.Backend.PG {
			return walRoleFailure(q, "58P01", `could not open file "pg_wal/summaries/0000000100000000000000000000000000000001.summary": No such file or directory`, args...)
		}
	}
	if s.Backend.PG {
		if i == 2 {
			step.Rows = [][]any{{int64(0), "0/0", "0/0", nil}}
		}
		return step
	}
	mechanism := "physical WAL summary file listing"
	if i == 1 {
		mechanism = "physical WAL summary file access"
	}
	if i == 2 {
		mechanism = "physical WAL summarizer state"
	}
	return walRoleFailure(q, "0A000", walRoleNames[i]+" "+mechanism+" is unsupported in embedded mode", args...)
}
func walRoleHeld(s *fixture.RoleMembershipScenario, held fixture.HeldStatement, step fixture.Step) {
	s.T.Helper()
	step.Prepared = false
	fixture.RunStep(s.T, s.P.Ctx, held, s.Record, step)
}
func TestReferenceWALSummaryAuthorization(t *testing.T) {
	walRoleCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		admin, err := db.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer admin.Close()
		mutate := func(q string) {
			s.T.Helper()
			if _, err := admin.ExecContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(err)
			}
		}
		s.Exec("SET ROLE wals_actor")
		for i, sig := range walRoleSignatures {
			q := "SELECT * FROM " + sig
			if i == 1 {
				q = "SELECT * FROM pg_wal_summary_contents($1::bigint,'0/0','0/1')"
			}
			held := predefinedHeld(s, q)
			defer held.Statement.Close()
			variants := [][]any{nil}
			if i == 1 {
				variants = [][]any{{nil}, {int64(0)}, {int64(1)}}
			}
			for _, args := range variants {
				walRoleHeld(s, held, walRoleOutcome(s, i, q, args...))
			}
			// Parameter substitution can make immutable child work a planning constant.
			// random() keeps the second witness genuinely runtime-dependent instead.
			var children []fixture.HeldStatement
			if i == 1 {
				for _, childSQL := range []string{"SELECT * FROM pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn+$1::numeric),'0/1')", "SELECT * FROM pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn+((random())::numeric+$1::numeric)),'0/1')"} {
					child := predefinedHeld(s, childSQL)
					defer child.Statement.Close()
					children = append(children, child)
				}
			}
			childOutcomes := func(allowed bool) {
				s.T.Helper()
				for j, child := range children {
					bad := int64(-1)
					if j == 1 {
						bad = -2
					} // random() is in [0,1), so this always underflows.
					failure := walRoleFailure(child.SQL, "22023", "pg_lsn out of range", bad)
					if !allowed && j == 1 {
						failure = walRoleFailure(child.SQL, "42501", "permission denied for function pg_wal_summary_contents", bad)
					}
					walRoleHeld(s, child, failure)
					zero := fixture.Select(child.SQL, walRoleColumns[1], walRoleTypes[1], nil, int64(0))
					if !allowed {
						zero = walRoleFailure(child.SQL, "42501", "permission denied for function pg_wal_summary_contents", int64(0))
					}
					walRoleHeld(s, child, zero)
				}
			}
			mutate("REVOKE EXECUTE ON FUNCTION " + sig + " FROM PUBLIC")
			for _, args := range variants {
				walRoleHeld(s, held, walRoleFailure(q, "42501", "permission denied for function "+walRoleNames[i], args...))
			}
			denied := predefinedHeld(s, q)
			walRoleHeld(s, denied, walRoleFailure(q, "42501", "permission denied for function "+walRoleNames[i], variants[0]...))
			defer denied.Statement.Close()
			childOutcomes(false)
			if i == 1 {
				malformed := "SELECT * FROM pg_wal_summary_contents(NULL::bigint,'broken','0/1')"
				predefinedFailure(s, malformed, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type pg_lsn: "broken"`, Position: fixture.Position(strings.Index(malformed, "'broken'") + 1)}, true, "prepare")
			}
			mutate("GRANT EXECUTE ON FUNCTION " + sig + " TO wals_actor")
			for _, args := range variants {
				walRoleHeld(s, held, walRoleOutcome(s, i, q, args...))
			}
			childOutcomes(true)
			mutate("REVOKE EXECUTE ON FUNCTION " + sig + " FROM wals_actor")
			walRoleHeld(s, held, walRoleFailure(q, "42501", "permission denied for function "+walRoleNames[i], variants[0]...))
			mutate("GRANT EXECUTE ON FUNCTION " + sig + " TO PUBLIC")
			walRoleHeld(s, held, walRoleOutcome(s, i, q, variants[0]...))
		}
		s.Exec("RESET ROLE")
		s.Exec("BEGIN")
		s.Exec("REVOKE EXECUTE ON FUNCTION pg_wal_summary_contents(bigint,pg_lsn,pg_lsn) FROM PUBLIC")
		s.Exec("ROLLBACK")
		s.Exec("SET ROLE wals_actor")
		predefinedSelect(s, "SELECT * FROM pg_wal_summary_contents(NULL::bigint,'0/0','0/1')", walRoleColumns[1], walRoleTypes[1], nil)
		s.Exec("RESET ROLE")
	})
}
