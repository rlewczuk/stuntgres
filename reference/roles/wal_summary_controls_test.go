package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func walControlSnapshot(s *fixture.RoleMembershipScenario) []byte {
	s.T.Helper()
	var snapshot bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &snapshot); err != nil {
		s.T.Fatal(err)
	}
	return snapshot.Bytes()
}
func walControlWitness(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	predefinedSelect(s, "SELECT id FROM wals_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
	predefinedSelect(s, "SELECT proacl::text AS acl FROM pg_proc WHERE oid IN (6321,6323) ORDER BY oid", []string{"acl"}, []string{"TEXT"}, [][]any{{"{}"}, {nil}})
	predefinedSelect(s, "SELECT has_function_privilege('wals_actor','pg_available_wal_summaries()','EXECUTE') AS a,has_function_privilege('wals_actor','pg_wal_summary_contents(bigint,pg_lsn,pg_lsn)','EXECUTE') AS b,has_function_privilege('wals_actor','pg_get_wal_summarizer_state()','EXECUTE') AS c", []string{"a", "b", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true}})
	predefinedSelect(s, "SELECT * FROM wals_null", walRoleColumns[1], walRoleTypes[1], nil)
	predefinedSelect(s, "SELECT * FROM wals_available LIMIT 0", walRoleColumns[0], walRoleTypes[0], nil)
	predefinedSelect(s, "SELECT * FROM wals_state LIMIT 0", walRoleColumns[2], walRoleTypes[2], nil)
}
func TestReferenceWALSummaryControls(t *testing.T) {
	walRoleCases(t, func(s *fixture.RoleMembershipScenario, _ *sql.DB) {
		if !s.Backend.PG {
			baseline := string(walControlSnapshot(s))
			for _, name := range walRoleNames {
				if strings.Contains(baseline, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.Contains(baseline, name) {
					s.T.Fatal("redundant baseline builtin ACL", name)
				}
			}
		}
		s.Exec("CREATE TABLE wals_sentinel(id integer)")
		s.Run(fixture.Exec("INSERT INTO wals_sentinel VALUES(7)", 1))
		s.Exec("CREATE VIEW wals_null AS SELECT * FROM pg_wal_summary_contents(end_lsn=>'0/1',tli=>NULL::bigint,start_lsn=>'0/0')")
		s.Exec("CREATE VIEW wals_available AS SELECT * FROM pg_available_wal_summaries()")
		s.Exec("CREATE VIEW wals_state AS SELECT * FROM pg_get_wal_summarizer_state()")
		s.Exec("REVOKE EXECUTE ON FUNCTION pg_available_wal_summaries() FROM PUBLIC")
		s.Exec("REVOKE EXECUTE ON FUNCTION pg_wal_summary_contents(bigint,pg_lsn,pg_lsn) FROM PUBLIC")
		s.Exec("GRANT EXECUTE ON FUNCTION pg_wal_summary_contents(bigint,pg_lsn,pg_lsn) TO wals_actor")
		// acldefault('c',0) constructs a genuine one-dimensional empty ACL.
		// A parsed '{}'::aclitem[] has zero dimensions and is invalid for ACL checks.
		if s.Backend.PG {
			s.Run(fixture.Exec(`UPDATE pg_catalog.pg_proc SET proacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid) WHERE oid=6321`, 1))
		} else {
			lines := strings.Split(string(walControlSnapshot(s)), "\n")
			replacements := 0
			for i, line := range lines {
				if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && strings.Contains(line, "pg_available_wal_summaries") {
					_, locator, ok := strings.Cut(line, " WHERE ")
					if !ok {
						s.T.Fatal(line)
					}
					lines[i] = `UPDATE pg_catalog.pg_proc SET proacl=pg_catalog.acldefault('c'::pg_catalog."char",0::pg_catalog.oid)::pg_catalog.aclitem[] WHERE ` + locator
					replacements++
				}
			}
			if replacements != 1 {
				s.T.Fatal("missing genuine emitted repair", replacements)
			}
			if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(strings.Join(lines, "\n"))); err != nil {
				s.T.Fatal(err)
			}
		}
		walControlWitness(s)
		var original sql.NullString
		if err := s.C.QueryRowContext(s.P.Ctx, "SELECT proacl::text FROM pg_proc WHERE oid=6322").Scan(&original); err != nil || !original.Valid {
			s.T.Fatal(original, err)
		}
		s.Exec("SET application_name='wals_controls_live'")
		s.Exec("SET ROLE wals_actor")
		held := predefinedHeld(s, "SELECT * FROM pg_wal_summary_contents($1::bigint,'0/0','0/1')")
		defer held.Statement.Close()
		walRoleHeld(s, held, walRoleOutcome(s, 1, held.SQL, nil))
		s.Exec("RESET ROLE")
		snapshot := walControlSnapshot(s)
		if s.Backend.PG {
			// Replay only actual native-emitted repairs after removing shared grant dependencies.
			s.Exec("DROP OWNED BY wals_actor CASCADE")
			count := 0
			for line := range strings.SplitSeq(string(snapshot), "\n") {
				if strings.HasPrefix(line, "UPDATE pg_catalog.pg_proc SET proacl=") && (strings.Contains(line, "pg_available_wal_summaries") || strings.Contains(line, "pg_wal_summary_contents")) {
					s.Run(fixture.Exec(line, 1))
					count++
				}
			}
			if count != 2 {
				s.T.Fatal("portable ACL repair count", count)
			}
			walControlWitness(s)
			return
		}
		s.Exec("REVOKE EXECUTE ON FUNCTION pg_wal_summary_contents(bigint,pg_lsn,pg_lsn) FROM wals_actor")
		s.Exec("SET ROLE wals_actor")
		walRoleHeld(s, held, walRoleFailure(held.SQL, "42501", "permission denied for function pg_wal_summary_contents", nil))
		if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
			s.T.Fatal(err)
		}
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewReader(snapshot)); err != nil {
			s.T.Fatal(err)
		}
		predefinedSelect(s, "SELECT current_setting('application_name') AS app,current_user::text AS actor", []string{"app", "actor"}, []string{"TEXT", "TEXT"}, [][]any{{"wals_controls_live", "wals_actor"}})
		walRoleHeld(s, held, walRoleOutcome(s, 1, held.SQL, nil))
		walRoleHeld(s, held, walRoleOutcome(s, 1, held.SQL, int64(1)))
		s.Exec("RESET ROLE")
		walControlWitness(s)
		predefinedSelect(s, "SELECT proacl::text AS acl FROM pg_proc WHERE oid=6322", []string{"acl"}, []string{"TEXT"}, [][]any{{original.String}})
		driver := &stuntgres.Driver{}
		connector, err := driver.OpenConnector("wals_destination")
		if err != nil {
			s.T.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		exec := func(q string) {
			s.T.Helper()
			if _, err := fresh.ExecContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(err)
			}
		}
		exec("CREATE TABLE wals_destination_sentinel(id integer); INSERT INTO wals_destination_sentinel VALUES(91)")
		exec("CREATE ROLE wals_destination_reader; GRANT EXECUTE ON FUNCTION pg_available_wal_summaries() TO wals_destination_reader")
		if err := stuntgres.Restore(s.P.Ctx, fresh, bytes.NewReader(snapshot)); err == nil {
			s.T.Fatal("missing principal accepted")
		}
		var sentinel int
		if err := fresh.QueryRowContext(s.P.Ctx, "SELECT id FROM wals_destination_sentinel").Scan(&sentinel); err != nil || sentinel != 91 {
			s.T.Fatal("failed restore replaced destination", sentinel, err)
		}
		var allowed bool
		if err := fresh.QueryRowContext(s.P.Ctx, "SELECT has_function_privilege('wals_destination_reader','pg_available_wal_summaries()','EXECUTE')").Scan(&allowed); err != nil || !allowed {
			s.T.Fatal("failed restore changed destination ACL", allowed, err)
		}
		exec("CREATE ROLE wals_actor LOGIN")
		if err := stuntgres.Restore(s.P.Ctx, fresh, bytes.NewReader(snapshot)); err != nil {
			s.T.Fatal(err)
		}
		wrapped, record := fixture.RecordedNativePool(s.T, connector)
		c, err := wrapped.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer c.Close()
		for _, step := range []fixture.Step{
			fixture.Select("SELECT id FROM wals_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}),
			fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid IN (6321,6323) ORDER BY oid", []string{"acl"}, []string{"TEXT"}, [][]any{{"{}"}, {nil}}),
			fixture.Select("SELECT proacl::text AS acl FROM pg_proc WHERE oid=6322", []string{"acl"}, []string{"TEXT"}, [][]any{{original.String}}),
			fixture.Select("SELECT has_function_privilege('wals_actor','pg_available_wal_summaries()','EXECUTE') AS a,has_function_privilege('wals_actor','pg_wal_summary_contents(bigint,pg_lsn,pg_lsn)','EXECUTE') AS b,has_function_privilege('wals_actor','pg_get_wal_summarizer_state()','EXECUTE') AS c", []string{"a", "b", "c"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true}}),
			fixture.Select("SELECT * FROM wals_null", walRoleColumns[1], walRoleTypes[1], nil),
			fixture.Select("SELECT * FROM wals_available LIMIT 0", walRoleColumns[0], walRoleTypes[0], nil),
			fixture.Select("SELECT * FROM wals_state LIMIT 0", walRoleColumns[2], walRoleTypes[2], nil),
		} {
			fixture.RunStep(s.T, s.P.Ctx, c, record, step)
		}
		if _, err := c.ExecContext(s.P.Ctx, "SET ROLE wals_actor"); err != nil {
			s.T.Fatal(err)
		}
		stmt, err := c.PrepareContext(s.P.Ctx, held.SQL)
		if err != nil {
			s.T.Fatal(err)
		}
		defer stmt.Close()
		freshHeld := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: held.SQL}
		fixture.RunStep(s.T, s.P.Ctx, freshHeld, record, fixture.Select(held.SQL, walRoleColumns[1], walRoleTypes[1], nil, nil))
		fixture.RunStep(s.T, s.P.Ctx, freshHeld, record, walRoleFailure(held.SQL, "0A000", "pg_wal_summary_contents physical WAL summary file access is unsupported in embedded mode", int64(1)))
		canceled, cancel := context.WithCancel(s.P.Ctx)
		cancel()
		rows, cancelErr := stmt.QueryContext(canceled, nil)
		if rows != nil {
			rows.Close()
			s.T.Fatal("pre-canceled source published rows")
		}
		if !errors.Is(cancelErr, context.Canceled) {
			s.T.Fatal("ordinary native cancellation", cancelErr)
		}
	})
}
