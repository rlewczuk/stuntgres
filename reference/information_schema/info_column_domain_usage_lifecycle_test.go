package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnDomainUsageLifecycle(t *testing.T) {
	for _, phase := range []string{"names_and_definitions", "transaction_frontiers"} {
		t.Run(phase, func(t *testing.T) {
			cduScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				cduSeed(run)
				where := "table_schema IN ('cdu','cdu_moved')"
				held := cduHold(t, p, c, cduProjection(b.PG, where, nil).Query)
				want := cduSeedRows(database)
				check := func() { cduCheck(t, p, c, r, held, cduProjection(b.PG, where, want)) }
				check()
				if phase == "transaction_frontiers" {
					reader, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer reader.Close()
					committed := cduProjection(b.PG, where, cduSeedRows(database))
					tx, err := c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					txRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, r, s) }
					txRun(fixture.Exec("ALTER TABLE cdu.t ADD extra cdu.parent", 0))
					five := append(cduSeedRows(database), cduRow(database, "cdu", "parent", "cdu", "t", "extra"))
					txRun(cduProjection(b.PG, where, five))
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, committed)
					txRun(fixture.Exec("SAVEPOINT before_drop", 0))
					txRun(fixture.Exec("DROP DOMAIN cdu.child CASCADE", 0))
					txRun(cduProjection(b.PG, where, [][]any{five[0], five[4]}))
					txRun(fixture.Exec("ROLLBACK TO before_drop", 0))
					txRun(cduProjection(b.PG, where, five))
					if err := tx.Rollback(); err != nil {
						t.Fatal(err)
					}
					check()
					tx, err = c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					fixture.RunStep(t, p.Ctx, tx, r, fixture.Exec("ALTER TABLE cdu.t ADD committed cdu.child", 0))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					want = append(want, cduRow(database, "cdu", "child", "cdu", "t", "committed"))
					check()
					fixture.RunStep(t, p.Ctx, reader, b.Recorder, cduProjection(b.PG, where, want))
					return
				}
				mutate := func(q string, rows [][]any) { run(fixture.Exec(q, 0)); want = rows; check() }
				mutate("ALTER TABLE cdu.t RENAME COLUMN a TO renamed", [][]any{cduRow(database, "cdu", "parent", "cdu", "t", "renamed"), want[1], want[2], want[3]})
				mutate("ALTER TABLE cdu.t RENAME TO renamed_t", [][]any{cduRow(database, "cdu", "parent", "cdu", "renamed_t", "renamed"), cduRow(database, "cdu", "child", "cdu", "renamed_t", "b"), want[2], want[3]})
				mutate("ALTER DOMAIN cdu.child RENAME TO renamed_child", [][]any{want[0], cduRow(database, "cdu", "renamed_child", "cdu", "renamed_t", "b"), want[2], cduRow(database, "cdu", "renamed_child", "cdu", "v", "b")})
				run(fixture.Exec("CREATE SCHEMA cdu_moved", 0))
				mutate("ALTER DOMAIN cdu.renamed_child SET SCHEMA cdu_moved", [][]any{want[0], cduRow(database, "cdu_moved", "renamed_child", "cdu", "renamed_t", "b"), want[2], cduRow(database, "cdu_moved", "renamed_child", "cdu", "v", "b")})
				mutate("ALTER TABLE cdu.renamed_t SET SCHEMA cdu_moved", [][]any{cduRow(database, "cdu", "parent", "cdu_moved", "renamed_t", "renamed"), cduRow(database, "cdu_moved", "renamed_child", "cdu_moved", "renamed_t", "b"), want[2], want[3]})
				mutate("DROP VIEW cdu.v", [][]any{want[0], want[1]})
				mutate("ALTER TABLE cdu_moved.renamed_t DROP COLUMN renamed", [][]any{want[1]})
				mutate("ALTER TABLE cdu_moved.renamed_t ADD replacement cdu.parent", append(want, cduRow(database, "cdu", "parent", "cdu_moved", "renamed_t", "replacement")))
				mutate("ALTER TABLE cdu_moved.renamed_t ALTER COLUMN replacement TYPE integer", [][]any{want[0]})
				mutate("ALTER TABLE cdu_moved.renamed_t ALTER COLUMN replacement TYPE cdu_moved.renamed_child", append(want, cduRow(database, "cdu_moved", "renamed_child", "cdu_moved", "renamed_t", "replacement")))
				mutate("DROP TABLE cdu_moved.renamed_t", nil)
				mutate("CREATE TABLE cdu_moved.renamed_t(only_new cdu.parent)", [][]any{cduRow(database, "cdu", "parent", "cdu_moved", "renamed_t", "only_new")})
				mutate("CREATE VIEW cdu.v AS SELECT only_new FROM cdu_moved.renamed_t", append(want, cduRow(database, "cdu", "parent", "cdu", "v", "only_new")))
				mutate("DROP VIEW cdu.v", [][]any{want[0]})
				mutate("CREATE VIEW cdu.v AS SELECT only_new::integer AS plain FROM cdu_moved.renamed_t", want)
			})
		})
	}
}
