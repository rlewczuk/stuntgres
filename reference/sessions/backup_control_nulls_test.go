package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBackupControlNullsMetadata(t *testing.T) {
	const role = "backup_null_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, call := range []string{"pg_backup_start(NULL)", "pg_backup_start(NULL::text)", "pg_backup_start('x',NULL)", "pg_backup_start(NULL,false)", "pg_backup_start(NULL,true)"} {
			backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT "+call+" AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{nil}}))
		}
		backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT pg_backup_start($1::text,NULL) AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{nil}}, strings.Repeat("é", 513)))
		for _, tc := range []struct {
			query string
			args  []any
		}{
			{"SELECT pg_backup_start(NULL::text,1/$1::integer=0)", []any{2}},
			{"SELECT pg_backup_start(NULL::text,abs(1/$1::integer)=0)", []any{2}},
			{"SELECT pg_backup_start($1::text,abs(1/$2::integer)=0)", []any{nil, 2}},
		} {
			backupRoutes(t, p, b, c, backupSelect(b.PG, tc.query, []string{"pg_backup_start"}, []string{"PG_LSN"}, [][]any{{nil}}, tc.args...))
		}
		for _, suffix := range []string{"", " LIMIT 0", " WHERE FALSE"} {
			want := [][]any{{nil, nil, nil}}
			scalar := [][]any{{nil}}
			if suffix != "" {
				want = nil
				scalar = nil
			}
			for _, q := range []string{"SELECT * FROM pg_backup_stop(NULL)", "SELECT (pg_backup_stop(NULL)).*"} {
				backupRoutes(t, p, b, c, backupSelect(b.PG, q+suffix, backupFields, backupTypes, want))
			}
			backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT * FROM pg_backup_stop(NULL) AS b(a,b,c)"+suffix, []string{"a", "b", "c"}, backupTypes, want))
			backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT pg_backup_stop(NULL) AS v"+suffix, []string{"v"}, []string{"RECORD"}, scalar))
			for i, field := range backupFields {
				backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT (pg_backup_stop(NULL))."+field+" AS v"+suffix, []string{"v"}, backupTypes[i:i+1], scalar))
			}
		}
		for _, q := range []string{"SELECT pg_backup_start('x') AS v WHERE FALSE", "SELECT pg_backup_start('x') AS v LIMIT 0"} {
			backupRoutes(t, p, b, c, backupSelect(b.PG, q, []string{"v"}, []string{"PG_LSN"}, nil))
		}
		for _, q := range []string{"SELECT * FROM pg_backup_stop() WHERE FALSE", "SELECT * FROM pg_backup_stop() LIMIT 0"} {
			backupRoutes(t, p, b, c, backupSelect(b.PG, q, backupFields, backupTypes, nil))
		}
		for _, tc := range []struct {
			q     string
			args  []any
			types []string
			names []string
		}{
			{"SELECT pg_backup_start($1::text,$2::boolean) AS v", []any{nil, true}, []string{"PG_LSN"}, []string{"v"}},
			{"SELECT pg_backup_stop($1::boolean) AS v", []any{nil}, []string{"RECORD"}, []string{"v"}},
		} {
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, backupSelect(b.PG, tc.q, tc.names, tc.types, [][]any{{nil}}, tc.args...))
			if len(tc.args) == 2 {
				backupPair(t, p, b, c, held, tc.q, "held_backup", "SELECT * FROM pg_backup_stop(false)", false, "held_backup", true)
			} else {
				s := backupAbsent(tc.q, b.PG)
				s.Args = []any{false}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, backupSelect(b.PG, tc.q, tc.names, tc.types, [][]any{{nil}}, tc.args...))
			stmt.Close()
		}
		run(fixture.Exec("CREATE ROLE "+role, 0))
		defer backupRestore(t, p, b, c, role)()
		run(fixture.Exec("CREATE TABLE backup_null_inputs(label text,wait boolean)", 0))
		run(fixture.Exec("INSERT INTO backup_null_inputs VALUES(NULL,NULL)", 1))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
		run(fixture.Exec("GRANT SELECT ON backup_null_inputs TO "+role, 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT pg_backup_start(NULL) AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{nil}}))
		backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT pg_backup_start($1::text) AS v", []string{"v"}, []string{"PG_LSN"}, [][]any{{nil}}, nil))
		parameterDenied := recoveryControlDenied("SELECT pg_backup_stop($1::boolean)", "pg_backup_stop")
		parameterDenied.Args = []any{nil}
		backupRoutes(t, p, b, c, parameterDenied)
		for _, q := range []string{"SELECT pg_backup_stop(NULL)", "SELECT * FROM pg_backup_stop(NULL)", "SELECT (pg_backup_stop(NULL)).*", "SELECT pg_backup_stop(wait) FROM backup_null_inputs", "SELECT * FROM backup_null_inputs i,LATERAL pg_backup_stop(i.wait) b"} {
			backupRoutes(t, p, b, c, recoveryControlDenied(q, "pg_backup_stop"))
		}
		backupRoutes(t, p, b, c, recoveryControlDenied("SELECT pg_backup_start(label) FROM backup_null_inputs", "pg_backup_start"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, id := range backupIdentities {
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+id+" TO "+role, 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
		backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT pg_backup_start(label) AS v FROM backup_null_inputs", []string{"v"}, []string{"PG_LSN"}, [][]any{{nil}}))
		backupRoutes(t, p, b, c, backupSelect(b.PG, "SELECT b.* FROM backup_null_inputs i,LATERAL pg_backup_stop(i.wait) b", backupFields, backupTypes, [][]any{{nil, nil, nil}}))
	})
}
