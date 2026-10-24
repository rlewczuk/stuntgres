package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalSchemaAlter(t *testing.T) {
	for _, held := range []bool{false, true} {
		for _, goTx := range []bool{false, true} {
			for _, publish := range []bool{false, true} {
				t.Run(fmt.Sprint(held, goTx, publish), func(t *testing.T) {
					fixture.SchemaReferenceCleanup(t, "tx_alter_dest")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
							var e fixture.Executor = c
							finish := func() {
								end := "ROLLBACK"
								if publish {
									end = "COMMIT"
								}
								fixture.LockExec(t, p.Ctx, c, end, 0)
							}
							if goTx {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								finish = func() {
									var err error
									if publish {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								}
							} else {
								fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
								defer c.ExecContext(p.Ctx, "ROLLBACK")
							}
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							utility := func(q string) {
								if held {
									stmt, err := e.PrepareContext(p.Ctx, q)
									if err != nil {
										t.Fatal(err)
									}
									defer stmt.Close()
									fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
								} else {
									run(fixture.Exec(q, 0))
								}
							}
							utility("CREATE SCHEMA tx_alter_dest")
							utility("CREATE TABLE tx_alter_old(v INT DEFAULT 2, CONSTRAINT tx_alter_check CHECK(v>0))")
							identity := func(name string) int64 {
								rows, err := e.QueryContext(p.Ctx, "SELECT $1::regclass::oid", name)
								if err != nil {
									t.Fatal(err)
								}
								defer rows.Close()
								var oid int64
								if !rows.Next() {
									t.Fatal(rows.Err())
								}
								if err := rows.Scan(&oid); err != nil {
									t.Fatal(err)
								}
								if rows.Next() || rows.Err() != nil {
									t.Fatal(rows.Err())
								}
								return oid
							}
							original := identity("tx_alter_old")
							run(fixture.Exec("INSERT INTO tx_alter_old DEFAULT VALUES", 1))
							utility("ALTER TABLE tx_alter_old RENAME TO tx_alter_new")
							utility("ALTER TABLE tx_alter_new RENAME COLUMN v TO n")
							utility("ALTER TABLE tx_alter_new RENAME CONSTRAINT tx_alter_check TO tx_alter_check_new")
							utility("ALTER TABLE tx_alter_new SET SCHEMA tx_alter_dest")
							utility("ALTER TABLE tx_alter_dest.tx_alter_new ALTER COLUMN n SET DEFAULT 9")
							run(fixture.Exec("INSERT INTO tx_alter_dest.tx_alter_new DEFAULT VALUES", 1))
							utility("ALTER TABLE tx_alter_dest.tx_alter_new ALTER COLUMN n DROP DEFAULT")
							run(fixture.Exec("INSERT INTO tx_alter_dest.tx_alter_new DEFAULT VALUES", 1))
							utility("ALTER TABLE tx_alter_dest.tx_alter_new OWNER TO CURRENT_USER")
							if got := identity("tx_alter_dest.tx_alter_new"); got != original {
								t.Fatal("rename/move changed identity", original, got)
							}
							run(fixture.Select("SELECT count(*) FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('tx_alter_old','tx_alter_new')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							want := fixture.Select("SELECT n FROM tx_alter_dest.tx_alter_new ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(9)}, {nil}})
							run(want)
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_alter_dest'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							finish()
							if publish {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, want)
							} else {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_alter_dest'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							}
						})
					}
				})
			}
		}
	}
}
