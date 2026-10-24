package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalPrivilegesControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		for _, publication := range []string{"COMMIT", "ROLLBACK TO before_acl", "held"} {
			t.Run(replacement+"/"+publication, func(t *testing.T) {
				fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), _ bool) {
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7)", 1))
					q := "GRANT SELECT ON t TO rg_reader"
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					run(fixture.Exec("BEGIN", 0))
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("SAVEPOINT before_acl", 0))
					run(fixture.Exec(q, 0))
					run(fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO rg_reader", 0))
					if b.PG {
						run(fixture.Exec("ROLLBACK", 0))
						return
					}
					var dump bytes.Buffer
					if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
						t.Fatal("private ACL/default leaked into committed-only dump", err)
					}
					replay := fixture.NewScenario(t)
					if err := stuntgres.Restore(p.Ctx, replay.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					target := replay.Backends()[0]
					fixture.RunStep(t, p.Ctx, target.DB, target.Recorder, fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT); GRANT SELECT ON replacement TO rg_reader"))
					fixture.State(t, err, "0A000")
					run(fixture.Exec("SET ROLE rg_reader", 0))
					run(fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
					run(fixture.Exec("RESET ROLE", 0))
					if replacement == "reset" {
						err = stuntgres.Reset(p.Ctx, p.Native)
					} else {
						err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT); INSERT INTO replacement VALUES(9)"))
					}
					if err != nil {
						t.Fatal(err)
					}
					if publication == "held" {
						_, err = stmt.ExecContext(p.Ctx)
					} else {
						_, err = c.ExecContext(p.Ctx, publication)
					}
					var state interface{ SQLState() string }
					if err == nil || errors.As(err, &state) || err.Error() != "stuntgres: catalog replaced before publication" {
						t.Fatal("stale publication fence", err)
					}
					run(fixture.Exec("ROLLBACK", 0))
					run(fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					run(fixture.Exec("CREATE TABLE fresh(a INT)", 0))
					run(fixture.Exec("INSERT INTO fresh VALUES(7)", 1))
					run(fixture.Exec("GRANT SELECT ON fresh TO rg_reader", 0))
					dump.Reset()
					err = stuntgres.Dump(p.Ctx, p.Native, &dump)
					if err == nil || errors.As(err, &state) || dump.Len() != 0 {
						t.Fatal("committed ACL dump must fail ordinarily before output", err, dump.String())
					}
				})
			})
		}
	}
}
