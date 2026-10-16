package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func batchUtilityRoles(t *testing.T) {
	cases := []batchUtilityCase{
		{"role_create", nil, "CREATE ROLE batch_role; ALTER ROLE batch_role LOGIN", "SELECT count(*) AS v FROM pg_user WHERE usename='batch_role'", int64(0), int64(1), "INT8"},
		{"role_alter", []string{"CREATE ROLE batch_role"}, "ALTER ROLE batch_role LOGIN", "SELECT count(*) AS v FROM pg_user WHERE usename='batch_role'", int64(0), int64(1), "INT8"},
		{"role_drop", []string{"CREATE ROLE batch_role LOGIN"}, "DROP ROLE batch_role", "SELECT count(*) AS v FROM pg_user WHERE usename='batch_role'", int64(1), int64(0), "INT8"},
		{"role_comment", []string{"CREATE ROLE batch_role LOGIN"}, "COMMENT ON ROLE batch_role IS 'new'", "SELECT shobj_description(usesysid,'pg_authid') AS v FROM pg_user WHERE usename='batch_role'", nil, "new", "TEXT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"batch_role", "batch_member"})
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				for _, q := range tc.seed {
					batchExec(t, p, b, a, q, 0)
				}
				probe := func(x fixture.Executor, v any) {
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(tc.probe, []string{"v"}, []string{tc.typ}, [][]any{{v}}))
				}
				q := tc.change + "; " + tc.probe + "; SELECT missing"
				w := batchDiag("42703", `column "missing" does not exist`)
				w.Position = batchPosition(q, "missing")
				r := batchQuery(t, p, a, q)
				fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"v"}, []string{tc.typ}, [][]any{{tc.after}}, false})
				fixture.AssertDiagnostic(t, r.Err(), &w)
				r.Close()
				probe(a, tc.before)
				probe(peer, tc.before)
				fixture.BatchSets(t, batchQuery(t, p, a, tc.change+"; "+tc.probe+"; BEGIN"), fixture.BatchSet{[]string{"v"}, []string{tc.typ}, [][]any{{tc.after}}, false})
				probe(peer, tc.before)
				batchExec(t, p, b, a, "COMMIT", 0)
				probe(peer, tc.after)
			})
		})
	}
	batchUtilityPrivilegeEffects(t)
	for _, command := range []string{"SET ROLE batch_role", "SET SESSION AUTHORIZATION batch_role"} {
		t.Run(command, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"batch_role"})
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				batchExec(t, p, b, a, "CREATE ROLE batch_role", 0)
				var identity string
				if err := a.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&identity); err != nil {
					t.Fatal(err)
				}
				q := command + "; SELECT current_user AS v; SELECT missing"
				w := batchDiag("42703", `column "missing" does not exist`)
				w.Position = batchPosition(q, "missing")
				r := batchQuery(t, p, a, q)
				fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"v"}, []string{"NAME"}, [][]any{{"batch_role"}}, false})
				fixture.AssertDiagnostic(t, r.Err(), &w)
				r.Close()
				fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT current_user AS v", []string{"v"}, []string{"NAME"}, [][]any{{identity}}))
				fixture.BatchSets(t, batchQuery(t, p, a, command+"; SELECT current_user AS v"), fixture.BatchSet{[]string{"v"}, []string{"NAME"}, [][]any{{"batch_role"}}, false})
				batchExec(t, p, b, a, "RESET SESSION AUTHORIZATION; RESET ROLE", 0)
			})
		})
	}
}
func TestReferenceDriverBatchDiagnosticsAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"batch_denied"})
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		batchExec(t, p, b, a, "CREATE ROLE batch_denied", 0)
		batchExec(t, p, b, a, "CREATE TABLE secret(i INT)", 0)
		batchExec(t, p, b, a, "GRANT INSERT,SELECT ON batch_t TO batch_denied", 0)
		if b.PG {
			batchExec(t, p, b, a, "GRANT USAGE ON SCHEMA public TO batch_denied", 0)
		}
		batchExec(t, p, b, a, "SET ROLE batch_denied", 0)
		for _, later := range []string{"SELECT i FROM secret", "INSERT INTO secret VALUES(1)"} {
			q := "INSERT INTO batch_t VALUES(1); " + later + "; INSERT INTO batch_t VALUES(2)"
			batchError(t, p, a, q, batchDiag("42501", "permission denied for table secret"))
			batchValues(t, p, b, peer, "SELECT i FROM batch_t", 7)
		}
		q := "INSERT INTO secret VALUES(1); SELECT i FROM batch_t WHERE"
		w := batchDiag("42601", "syntax error at end of input")
		w.Position = fixture.Position(utf8.RuneCountInString(q) + 1)
		batchError(t, p, a, q, w)
		batchExec(t, p, b, a, "RESET ROLE", 0)
	})
}
func batchUtilityNonrollback(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		batchExec(t, p, b, a, "CREATE SEQUENCE us START 10", 0)
		q := "SELECT nextval('us') AS v; SELECT setval('us',20) AS v; PREPARE batch_saved AS SELECT i FROM batch_t; SELECT pg_advisory_lock(123456); SELECT missing"
		w := batchDiag("42703", `column "missing" does not exist`)
		w.Position = batchPosition(q, "missing")
		r := batchQuery(t, p, a, q)
		fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"v"}, []string{"INT8"}, [][]any{{int64(10)}}, false})
		if !r.NextResultSet() {
			t.Fatal(r.Err())
		}
		fixture.BatchReadSet(t, r, fixture.BatchSet{[]string{"v"}, []string{"INT8"}, [][]any{{int64(20)}}, false})
		if !r.NextResultSet() {
			t.Fatal(r.Err())
		}
		if !r.Next() {
			t.Fatal(r.Err())
		}
		r.Next()
		fixture.AssertDiagnostic(t, r.Err(), &w)
		r.Close()
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT currval('us'),lastval(),nextval('us')", []string{"currval", "lastval", "nextval"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(20), int64(20), int64(21)}}))
		batchValues(t, p, b, a, "EXECUTE batch_saved", 7)
		batchExec(t, p, b, a, "DEALLOCATE batch_saved", 0)
		fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT pg_advisory_unlock(123456)", []string{"pg_advisory_unlock"}, []string{"BOOL"}, [][]any{{true}}))
		q = "INSERT INTO batch_t VALUES(1); CREATE INDEX CONCURRENTLY ui ON batch_t(i)"
		batchError(t, p, a, q, batchDiag("25001", "CREATE INDEX CONCURRENTLY cannot run inside a transaction block"))
		batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
		batchExec(t, p, b, a, "CREATE INDEX CONCURRENTLY ui ON batch_t(i)", 0)
	})
}
