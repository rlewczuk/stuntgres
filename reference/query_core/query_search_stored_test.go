package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchStored(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		searchSeed(subqueryRun(t, p, b, c))
		for _, mode := range searchModes {
			t.Run(mode, func(t *testing.T) {
				run := subqueryRun(t, p, b, c)
				prefix := "search_" + strings.ToLower(mode)
				for _, kind := range []struct{ name, ddl string }{{"view", "CREATE VIEW "}, {"ctas", "CREATE TABLE "}, {"mv", "CREATE MATERIALIZED VIEW "}} {
					t.Run("pseudo "+kind.name, func(t *testing.T) {
						q := "/*é*/ " + kind.ddl + prefix + "_pseudo_" + kind.name + " AS " + searchTree(mode) + "SELECT seq FROM r"
						typeName := "record[]"
						if mode == "BREADTH" {
							typeName = "record"
						}
						want := fixture.ControlsReferenceDiagnostic("42P16", `column "seq" has pseudo-type `+typeName)
						searchAssertDiagnostic(t, p, b, c, q, &want)
					})
				}
				run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname IN ('"+prefix+"_pseudo_view','"+prefix+"_pseudo_ctas','"+prefix+"_pseudo_mv')", []string{"relname"}, []string{"NAME"}, nil))
				body := searchTree(mode) + "SELECT id,seq::text AS seq FROM r"
				run(fixture.Exec("CREATE VIEW "+prefix+"_view AS "+body, 0))
				fixture.ViewCopy(t, p, b, prefix+"_view", prefix+"_copy")
				run(fixture.Exec("ALTER VIEW "+prefix+"_copy RENAME TO "+prefix+"_renamed", 0))
				run(fixture.Exec("CREATE TABLE "+prefix+"_ctas AS "+body, 7))
				run(fixture.Exec("CREATE MATERIALIZED VIEW "+prefix+"_mv AS "+body, 7))
				run(fixture.Exec("CREATE TABLE "+prefix+"_sink(id INT,seq TEXT)", 0))
				run(fixture.Exec("INSERT INTO "+prefix+"_sink "+body, 7))
				for _, suffix := range []string{"_view", "_renamed", "_ctas", "_mv", "_sink"} {
					subqueryBoth(run, cteBag("SELECT * FROM "+prefix+suffix, []string{"id", "seq"}, []string{"INT4", "TEXT"}, searchTreeRows(mode)))
				}
			})
		}
		run := subqueryRun(t, p, b, c)
		run(fixture.Exec("ALTER TABLE search_nodes RENAME COLUMN parent TO parent_key", 0))
		run(fixture.Exec("INSERT INTO search_nodes VALUES(12,11)", 1))
		for _, mode := range searchModes {
			prefix := "search_" + strings.ToLower(mode)
			live := searchLiveRows(mode)
			subqueryBoth(run, cteBag("SELECT * FROM "+prefix+"_view", []string{"id", "seq"}, []string{"INT4", "TEXT"}, live))
			subqueryBoth(run, cteBag("SELECT * FROM "+prefix+"_renamed", []string{"id", "seq"}, []string{"INT4", "TEXT"}, live))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW "+prefix+"_mv", 0))
			run(cteBag("SELECT * FROM "+prefix+"_mv", []string{"id", "seq"}, []string{"INT4", "TEXT"}, live))
		}
		t.Run("restore atomicity", func(t *testing.T) { searchRestoreFailures(t, p, b, c) })
		if !b.PG {
			searchStoredReplay(t, p, b, c)
		}
	})
}

func searchLiveRows(mode string) [][]any {
	seq := "{(1),(10),(11),(12)}"
	if mode == "BREADTH" {
		seq = "(3,12)"
	}
	return append(searchTreeRows(mode), []any{int64(12), seq})
}

func searchRestoreFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := subqueryRun(t, p, b, c)
	run(fixture.Exec("CREATE TABLE search_restore_sentinel(n INT)", 0))
	run(fixture.Exec("INSERT INTO search_restore_sentinel VALUES(99)", 1))
	prefix := "CREATE TABLE search_restore_staged(n INT); "
	for _, mode := range searchModes {
		q := prefix + "CREATE VIEW search_restore_bad AS WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r)" + searchClause(mode, "missing") + "SELECT n FROM r;"
		var err error
		if b.PG {
			tx, e := c.BeginTx(p.Ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			_, err = tx.ExecContext(p.Ctx, q)
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
		} else {
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q))
		}
		want := cycleDiagnostic("42601", `search column "missing" not in WITH query column list`, q, "SEARCH")
		fixture.AssertDiagnostic(t, err, &want)
		run(fixture.Select("SELECT n FROM search_restore_sentinel", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
		run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname IN ('search_restore_staged','search_restore_bad')", []string{"relname"}, []string{"NAME"}, nil))
	}
	t.Run("malformed staged script", func(t *testing.T) {
		q := prefix + "CREATE VIEW search_restore_bad AS SELECT FROM ;"
		var err error
		if b.PG {
			tx, e := c.BeginTx(p.Ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			_, err = tx.ExecContext(p.Ctx, q)
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
		} else {
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(q))
		}
		want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near ";"`)
		want.Position = fixture.Position(len(q))
		fixture.AssertDiagnostic(t, err, &want)
		run(fixture.Select("SELECT n FROM search_restore_sentinel", []string{"n"}, []string{"INT4"}, recursiveCTEInts(99)))
		run(cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname IN ('search_restore_staged','search_restore_bad')", []string{"relname"}, []string{"NAME"}, nil))
	})
	if b.PG {
		return
	}
	before := fixture.ViewDump(t, p)
	failure := errors.New("search replay reader failure")
	err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader(prefix), fixture.CatalogControlFailingReader{Err: failure}))
	fixture.LexicalOrdinary(t, err, failure)
	ctx, cancel := context.WithCancel(p.Ctx)
	defer cancel()
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	fixture.LexicalOrdinary(t, err, context.Canceled)
	if after := fixture.ViewDump(t, p); !bytes.Equal(before, after) {
		t.Fatal("failed restore changed persistent state")
	}
}

func searchStoredReplay(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dump := fixture.ViewDump(t, p)
	var retained []*sql.Rows
	for _, mode := range searchModes {
		q := strings.Replace(searchTree(mode), "parent", "parent_key", -1) + "SELECT id,seq FROM r ORDER BY seq"
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		retained = append(retained, rows)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.ViewRestore(t, p, dump)
	for i, rows := range retained {
		var got [][]any
		for rows.Next() {
			var id, seq any
			if err := rows.Scan(&id, &seq); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{id, seq})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		cycleAssertBag(t, got, searchLiveRows(searchModes[i]))
	}
	var checks []fixture.Step
	for _, mode := range searchModes {
		prefix := "search_" + strings.ToLower(mode)
		for _, suffix := range []string{"_view", "_renamed", "_mv", "_ctas", "_sink"} {
			want := searchLiveRows(mode)
			if suffix == "_ctas" || suffix == "_sink" {
				want = searchTreeRows(mode)
			}
			checks = append(checks, cteBag("SELECT * FROM "+prefix+suffix, []string{"id", "seq"}, []string{"INT4", "TEXT"}, want))
		}
	}
	for _, s := range checks {
		subqueryBoth(subqueryRun(t, p, b, c), s)
	}
	foreign := fixture.NewFixture(t, false, false)
	fixture.ViewRestore(t, foreign, dump)
	for _, fb := range foreign.Backends() {
		for _, s := range checks {
			fixture.RunStep(t, foreign.Ctx, fb.DB, fb.Recorder, s)
		}
	}
	if !bytes.Equal(dump, fixture.ViewDump(t, p)) {
		t.Fatal("symbolic SEARCH redump changed")
	}
	if p.Postgres != nil {
		defer func() {
			for _, q := range []string{"DROP TABLE IF EXISTS search_nodes CASCADE", "DROP TABLE IF EXISTS search_depth_ctas", "DROP TABLE IF EXISTS search_breadth_ctas", "DROP TABLE IF EXISTS search_depth_sink", "DROP TABLE IF EXISTS search_breadth_sink", "DROP TABLE IF EXISTS search_restore_sentinel"} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
		}()
		if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
			t.Fatal("actual native SEARCH dump PostgreSQL replay", err)
		}
		for _, mode := range searchModes {
			name := "search_" + strings.ToLower(mode) + "_mv"
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.MaterializedViewUnpopulated(name))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("REFRESH MATERIALIZED VIEW "+name, 0))
		}
		for _, s := range checks {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(s))
		}
	}
}

func searchSmokeStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, mode := range searchModes {
		run := subqueryRun(t, p, b, c)
		name := "search_smoke_" + strings.ToLower(mode)
		run(fixture.Exec("CREATE VIEW "+name+" AS "+searchTree(mode)+"SELECT id,seq::text AS seq FROM r", 0))
		subqueryBoth(run, cteBag("SELECT * FROM "+name, []string{"id", "seq"}, []string{"INT4", "TEXT"}, searchTreeRows(mode)))
	}
	if b.PG {
		return
	}
	dump := fixture.ViewDump(t, p)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.ViewRestore(t, p, dump)
	foreign := fixture.NewFixture(t, false, false)
	fixture.ViewRestore(t, foreign, dump)
	for _, mode := range searchModes {
		s := cteBag("SELECT * FROM search_smoke_"+strings.ToLower(mode), []string{"id", "seq"}, []string{"INT4", "TEXT"}, searchTreeRows(mode))
		subqueryBoth(subqueryRun(t, p, b, c), s)
		for _, fb := range foreign.Backends() {
			fixture.RunStep(t, foreign.Ctx, fb.DB, fb.Recorder, s)
		}
	}
}
