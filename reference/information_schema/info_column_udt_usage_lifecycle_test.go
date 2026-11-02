package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoColumnUdtUsageLifecycle(t *testing.T) {
	for _, phase := range []string{"names_and_definitions", "transaction_frontiers", "retained_rows", "domain_view_cascade"} {
		t.Run(phase, func(t *testing.T) {
			cuScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				if phase == "names_and_definitions" {
					cuNames(t, p, b, c, r, database)
					return
				}
				cuSeed(run)
				where := "table_schema='cu_scope'"
				held := cduHold(t, p, c, cuProjection(b.PG, where, nil).Query)
				want := cuSeedRows(database)
				check := func() { cuCheck(t, p, c, r, held, cuProjection(b.PG, where, want)) }
				check()
				if phase == "domain_view_cascade" {
					run(fixture.Exec("DROP DOMAIN cu_scope.c CASCADE", 0))
					want = [][]any{want[0], want[2]}
					check()
					run(fixture.Exec("CREATE DOMAIN cu_scope.c AS text", 0))
					run(fixture.Exec("ALTER TABLE cu_scope.t ADD recreated cu_scope.c", 0))
					want = append(want, cuRow(database, "pg_catalog", "text", "cu_scope", "t", "recreated"))
					check()
					return
				}
				if phase == "retained_rows" {
					// pq streams: consume before DDL rather than hold a protocol lock.
					// Native eager Rows must distinguish old capture from fresh rebinding.
					rows, err := b.DB.QueryContext(p.Ctx, held.SQL)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					cuMetadata(t, columns, b.PG)
					if b.PG {
						cuRetained(t, rows, cuSeedRows(database))
					}
					run(fixture.Exec("ALTER TABLE cu_scope.t RENAME COLUMN a TO renamed", 0))
					want = [][]any{cuRow(database, "pg_catalog", "int4", "cu_scope", "t", "renamed"), want[1], want[2], want[3], want[4]}
					check()
					run(fixture.Exec("DROP VIEW cu_scope.v", 0))
					run(fixture.Exec("DROP TABLE cu_scope.t", 0))
					want = nil
					check()
					if !b.PG {
						cuRetained(t, rows, cuSeedRows(database))
					}
					cuMetadata(t, columns, b.PG)
					return
				}
				reader, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				committed := cuProjection(b.PG, where, cuSeedRows(database))
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				txRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, tx, r, s) }
				txRun(fixture.Exec("ALTER TABLE cu_scope.t ADD extra cu_scope.p", 0))
				txRun(fixture.Exec("ALTER TABLE cu_scope.t ALTER COLUMN plain TYPE text", 0))
				uncommitted := [][]any{want[0], want[1], cuRow(database, "pg_catalog", "text", "cu_scope", "t", "plain"), want[3], want[4], cuRow(database, "pg_catalog", "int4", "cu_scope", "t", "extra")}
				txStep := cuProjection(b.PG, where, uncommitted)
				txRun(txStep)
				txRun(fixture.Prepared(txStep))
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, committed)
				txRun(fixture.Exec("SAVEPOINT before_drop", 0))
				txRun(fixture.Exec("DROP DOMAIN cu_scope.c CASCADE", 0))
				txRun(cuProjection(b.PG, where, [][]any{uncommitted[0], uncommitted[2], uncommitted[5]}))
				txRun(fixture.Exec("ROLLBACK TO before_drop", 0))
				txRun(txStep)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				check()
				tx, err = c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				fixture.RunStep(t, p.Ctx, tx, r, fixture.Exec("ALTER TABLE cu_scope.t ADD committed cu_scope.c", 0))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				want = append(want, cuRow(database, "cu_scope", "p", "cu_scope", "t", "committed"))
				check()
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, cuProjection(b.PG, where, want))
			})
		})
	}
}

func cuNames(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	for _, q := range []string{"CREATE SCHEMA cu_scope", "CREATE SCHEMA cu_moved", "CREATE TYPE cu_scope.e AS ENUM('x')", "CREATE DOMAIN cu_scope.p AS cu_scope.e", "CREATE DOMAIN cu_scope.c AS cu_scope.p", "CREATE TABLE cu_scope.t(x cu_scope.e,y cu_scope.p,z cu_scope.c,plain integer)"} {
		run(fixture.Exec(q, 0))
	}
	where := "table_schema IN ('cu_scope','cu_moved','cu_renamed')"
	held := cduHold(t, p, c, cuProjection(b.PG, where, nil).Query)
	want := append(cuOwnedRows(database, "cu_scope"), cuRow(database, "pg_catalog", "int4", "cu_scope", "t", "plain"))
	check := func() { cuCheck(t, p, c, r, held, cuProjection(b.PG, where, want)) }
	mutate := func(q string, rows [][]any) { run(fixture.Exec(q, 0)); want = rows; check() }
	check()
	mutate("ALTER TYPE cu_scope.e RENAME TO renamed_e", [][]any{cuRow(database, "cu_scope", "renamed_e", "cu_scope", "t", "x"), cuRow(database, "cu_scope", "renamed_e", "cu_scope", "t", "y"), want[2], want[3]})
	mutate("ALTER TYPE cu_scope.renamed_e SET SCHEMA cu_moved", [][]any{cuRow(database, "cu_moved", "renamed_e", "cu_scope", "t", "x"), cuRow(database, "cu_moved", "renamed_e", "cu_scope", "t", "y"), want[2], want[3]})
	mutate("ALTER DOMAIN cu_scope.c RENAME TO renamed_c", want)
	mutate("ALTER DOMAIN cu_scope.p RENAME TO renamed_p", [][]any{want[0], want[1], cuRow(database, "cu_scope", "renamed_p", "cu_scope", "t", "z"), want[3]})
	mutate("ALTER DOMAIN cu_scope.renamed_p SET SCHEMA cu_moved", [][]any{want[0], want[1], cuRow(database, "cu_moved", "renamed_p", "cu_scope", "t", "z"), want[3]})
	mutate("ALTER TABLE cu_scope.t RENAME COLUMN x TO renamed", [][]any{cuRow(database, "cu_moved", "renamed_e", "cu_scope", "t", "renamed"), want[1], want[2], want[3]})
	mutate("ALTER TABLE cu_scope.t RENAME TO renamed_t", [][]any{cuRow(database, "cu_moved", "renamed_e", "cu_scope", "renamed_t", "renamed"), cuRow(database, "cu_moved", "renamed_e", "cu_scope", "renamed_t", "y"), cuRow(database, "cu_moved", "renamed_p", "cu_scope", "renamed_t", "z"), cuRow(database, "pg_catalog", "int4", "cu_scope", "renamed_t", "plain")})
	mutate("ALTER TABLE cu_scope.renamed_t SET SCHEMA cu_moved", [][]any{cuRow(database, "cu_moved", "renamed_e", "cu_moved", "renamed_t", "renamed"), cuRow(database, "cu_moved", "renamed_e", "cu_moved", "renamed_t", "y"), cuRow(database, "cu_moved", "renamed_p", "cu_moved", "renamed_t", "z"), cuRow(database, "pg_catalog", "int4", "cu_moved", "renamed_t", "plain")})
	mutate("ALTER SCHEMA cu_moved RENAME TO cu_renamed", [][]any{cuRow(database, "cu_renamed", "renamed_e", "cu_renamed", "renamed_t", "renamed"), cuRow(database, "cu_renamed", "renamed_e", "cu_renamed", "renamed_t", "y"), cuRow(database, "cu_renamed", "renamed_p", "cu_renamed", "renamed_t", "z"), cuRow(database, "pg_catalog", "int4", "cu_renamed", "renamed_t", "plain")})
	mutate("ALTER TABLE cu_renamed.renamed_t ADD extra integer", append(want, cuRow(database, "pg_catalog", "int4", "cu_renamed", "renamed_t", "extra")))
	mutate("ALTER TABLE cu_renamed.renamed_t ADD extra2 integer", append(want, cuRow(database, "pg_catalog", "int4", "cu_renamed", "renamed_t", "extra2")))
	mutate("ALTER TABLE cu_renamed.renamed_t DROP COLUMN y", [][]any{want[0], want[2], want[3], want[4], want[5]})
	mutate("ALTER TABLE cu_renamed.renamed_t ADD replacement text", append(want, cuRow(database, "pg_catalog", "text", "cu_renamed", "renamed_t", "replacement")))
	mutate("ALTER TABLE cu_renamed.renamed_t ALTER COLUMN plain TYPE text", [][]any{want[0], want[1], cuRow(database, "pg_catalog", "text", "cu_renamed", "renamed_t", "plain"), want[3], want[4], want[5]})
	mutate("ALTER TABLE cu_renamed.renamed_t ALTER COLUMN z TYPE cu_renamed.renamed_p", [][]any{want[0], cuRow(database, "cu_renamed", "renamed_e", "cu_renamed", "renamed_t", "z"), want[2], want[3], want[4], want[5]})
	mutate("CREATE VIEW cu_scope.v AS SELECT plain FROM cu_renamed.renamed_t", append(want, cuRow(database, "pg_catalog", "text", "cu_scope", "v", "plain")))
	mutate("DROP VIEW cu_scope.v", want[:6])
	mutate("CREATE VIEW cu_scope.v AS SELECT plain::integer AS changed FROM cu_renamed.renamed_t", append(want, cuRow(database, "pg_catalog", "int4", "cu_scope", "v", "changed")))
	mutate("DROP VIEW cu_scope.v", want[:6])
	mutate("DROP TABLE cu_renamed.renamed_t", nil)
	mutate("DROP TYPE cu_renamed.renamed_e CASCADE", nil)
	mutate("CREATE TYPE cu_renamed.renamed_e AS ENUM('new')", nil)
	mutate("CREATE TABLE cu_renamed.renamed_t(only_new cu_renamed.renamed_e)", [][]any{cuRow(database, "cu_renamed", "renamed_e", "cu_renamed", "renamed_t", "only_new")})
}
