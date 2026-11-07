package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func addGeneratedControlRows(g *addGeneratedCase) {
	g.t.Helper()
	g.postsRows(false)
	g.kinds("g", []any{"s", "s"}, []any{"v", "v"}, []any{"lazy", "v"})
	g.run(fixture.Select("SELECT a,s,v FROM g ORDER BY a", []string{"a", "s", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(2), int64(3), int64(4)}}))
	g.run(fixture.Select("SELECT column_name,dependent_column FROM information_schema.column_column_usage WHERE table_name='g' ORDER BY dependent_column", []string{"column_name", "dependent_column"}, []string{"NAME", "NAME"}, [][]any{{"a", "lazy"}, {"a", "s"}, {"a", "v"}}))
	g.run(fixture.Select("SELECT a.attname,pg_get_expr(d.adbin,d.adrelid) FROM pg_attribute a JOIN pg_attrdef d ON d.adrelid=a.attrelid AND d.adnum=a.attnum WHERE a.attrelid='g'::regclass ORDER BY a.attnum", []string{"attname", "pg_get_expr"}, []string{"NAME", "TEXT"}, [][]any{{"s", "(a + 1)"}, {"v", "(a * 2)"}, {"lazy", "(a / 0)"}}))
	g.run(fixture.Select("SELECT am.amname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_am am ON am.oid=c.relam WHERE c.relname='idx_posts_search_gin' AND i.indrelid='posts'::regclass", []string{"amname"}, []string{"NAME"}, [][]any{{"gin"}}))
	g.fail("SELECT lazy FROM g", addGeneratedDiagnostic("22012", "division by zero"), true, "execute")
}
func addGeneratedControlEffects(g *addGeneratedCase) {
	g.t.Helper()
	g.exec("INSERT INTO g(a) VALUES(4)", 1)
	g.exec("UPDATE g SET a=8 WHERE a=2", 1)
	g.run(fixture.Select("SELECT a,s,v FROM g ORDER BY a", []string{"a", "s", "v"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(4), int64(5), int64(8)}, {int64(8), int64(9), int64(16)}}))
	d := addGeneratedDiagnostic("428C9", `cannot insert a non-DEFAULT value into column "s"`)
	d.Detail = `Column "s" is a generated column.`
	stage := "execute"
	if g.prepared {
		stage = "prepare"
	}
	g.fail("INSERT INTO g(a,s) VALUES(9,$1)", d, false, stage, 10)
}

func TestReferenceAlterAddColumnGeneratedControls(t *testing.T) {
	addGeneratedRoutes(t, func(g *addGeneratedCase) {
		g.posts()
		g.gin()
		g.exec("CREATE TABLE g(a integer)", 0)
		g.exec("INSERT INTO g VALUES(2)", 1)
		g.exec("ALTER TABLE g ADD s integer GENERATED ALWAYS AS(a+1) STORED, ADD v integer GENERATED ALWAYS AS(a*2) VIRTUAL", 0)
		g.exec("ALTER TABLE g ADD lazy integer GENERATED ALWAYS AS(a/0)", 0)
		addGeneratedControlRows(g)
		if !g.b.PG {
			dump := fixture.CheckDump(g.t, g.p)
			for range 2 {
				if err := stuntgres.Reset(g.p.Ctx, g.p.Native); err != nil {
					g.t.Fatal(err)
				}
				if err := stuntgres.Restore(g.p.Ctx, g.p.Native, bytes.NewReader(dump)); err != nil {
					g.t.Fatal(err)
				}
				addGeneratedControlRows(g)
				if !bytes.Equal(dump, fixture.CheckDump(g.t, g.p)) {
					g.t.Fatal("roundtrip changed emitted SQL")
				}
			}
			bad := "CREATE TABLE broken(a integer,b integer GENERATED ALWAYS AS(a/0) STORED); INSERT INTO broken(a) VALUES(1);"
			want := addGeneratedDiagnostic("22012", "division by zero")
			fixture.AssertDiagnostic(g.t, stuntgres.Restore(g.p.Ctx, g.p.Native, strings.NewReader(bad)), &want)
			ctx, cancel := context.WithCancel(g.p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, g.p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				g.t.Fatal(err)
			}
			if !bytes.Equal(dump, fixture.CheckDump(g.t, g.p)) {
				g.t.Fatal("failed/canceled Restore changed installed state")
			}
			addGeneratedControlRows(g)
			connector, err := stuntgres.NewDriver().OpenConnector("added-generation-import")
			if err != nil {
				g.t.Fatal(err)
			}
			raw := sql.OpenDB(connector)
			defer raw.Close()
			recorded, rec := fixture.RecordedNativePool(g.t, connector)
			defer recorded.Close()
			if err := stuntgres.Restore(g.p.Ctx, raw, bytes.NewReader(dump)); err != nil {
				g.t.Fatal(err)
			}
			conn, err := recorded.Conn(g.p.Ctx)
			if err != nil {
				g.t.Fatal(err)
			}
			defer conn.Close()
			fresh := *g
			fresh.c = conn
			fresh.b = fixture.Backend{DB: recorded, Recorder: rec}
			addGeneratedControlRows(&fresh)
			addGeneratedControlEffects(&fresh)
			if g.p.Postgres != nil {
				pgc, err := g.p.Postgres.Conn(g.p.Ctx)
				if err != nil {
					g.t.Fatal(err)
				}
				func() {
					defer pgc.Close()
					defer func() {
						if _, err := pgc.ExecContext(g.p.Ctx, "DROP TABLE IF EXISTS g,posts CASCADE"); err != nil {
							g.t.Error(err)
						}
					}()
					if _, err := pgc.ExecContext(g.p.Ctx, string(dump)); err != nil {
						g.t.Fatal("unchanged emitted SQL replay", err)
					}
					pg := *g
					pg.c = pgc
					pg.b = fixture.Backend{PG: true, DB: g.p.Postgres, Recorder: g.p.PostgresRecord}
					addGeneratedControlRows(&pg)
					addGeneratedControlEffects(&pg)
				}()
			}
		}
		addGeneratedControlEffects(g)
	})
}
