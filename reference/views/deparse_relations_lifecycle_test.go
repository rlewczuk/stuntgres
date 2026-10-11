package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeparseRelationsPreparedLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "deparse_a", "deparse_b")
	fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.DeparseRelationsSeed(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		hold := func(q string) *sql.Stmt {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			return s
		}
		indexQ := "SELECT pg_get_indexdef($1,$2,$3) AS definition"
		viewQ := "SELECT pg_get_viewdef($1) AS definition"
		wrapQ := "SELECT pg_get_viewdef($1,1) AS definition"
		prettyQ := "SELECT pg_get_viewdef($1,true) AS definition"
		ix, view, wrap, pretty := hold(indexQ), hold(viewQ), hold(wrapQ), hold(prettyQ)
		var indexID, viewID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='ix'").Scan(&indexID); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&viewID); err != nil {
			t.Fatal(err)
		}
		heldRun := func(stmt *sql.Stmt, q string, want any, args ...any) {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, want, args...))
		}
		for _, tc := range []struct {
			column any
			pretty any
			want   any
		}{{int64(1), false, "id"}, {int64(2), true, "txt"}, {int64(4), false, ""}, {nil, false, nil}, {int64(1), nil, nil}} {
			heldRun(ix, indexQ, tc.want, indexID, tc.column, tc.pretty)
		}
		heldRun(ix, indexQ, nil, nil, int64(0), false)
		heldRun(view, viewQ, nil, nil)
		heldRun(wrap, wrapQ, nil, nil)
		heldRun(pretty, prettyQ, nil, nil)
		compact := " SELECT id AS k,\n    txt AS label\n   FROM t\n  WHERE visible;"
		heldRun(view, viewQ, compact, "v")
		heldRun(pretty, prettyQ, compact, "v")
		heldRun(wrap, wrapQ, compact, viewID)
		indexOneQ := "SELECT pg_get_indexdef($1) AS definition"
		indexOne := hold(indexOneQ)
		heldRun(indexOne, indexOneQ, "CREATE INDEX ix ON public.t USING btree (id, txt DESC NULLS LAST, id NULLS FIRST)", indexID)
		heldRun(indexOne, indexOneQ, nil, nil)
		// SAME prepared consumer captures eager old string before catalog publication.
		rows, err := view.QueryContext(p.Ctx, "v")
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal(rows.Err())
		}
		var retained any
		if err := rows.Scan(&retained); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		fixture.DeparseRelationsMetadata(t, b.PG, rows)
		// pq streaming rows must finish before the same physical connection executes DDL.
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal(rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"ALTER TABLE t RENAME COLUMN id TO key", "ALTER TABLE t RENAME TO renamed", "ALTER VIEW v RENAME COLUMN k TO output", "CREATE TABLE t(id INT)", "INSERT INTO t VALUES(99)"} {
			count := int64(0)
			if q == "INSERT INTO t VALUES(99)" {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		if fixture.Normalize(retained) != compact {
			t.Fatal("returned definition changed", retained)
		}
		heldRun(ix, indexQ, "key", indexID, int64(1), false)
		renamed := " SELECT key AS output,\n    txt AS label\n   FROM renamed\n  WHERE visible;"
		heldRun(view, viewQ, renamed, "v")
		run(fixture.Exec("CREATE OR REPLACE VIEW v AS SELECT key+10 AS output,txt AS label,true AS extra FROM renamed WHERE visible", 0))
		definition := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('v') AS definition")
		heldRun(view, viewQ, definition, "v")
		run(fixture.Exec("CREATE VIEW current_copy AS "+definition, 0))
		s := fixture.Select("SELECT * FROM current_copy", []string{"output", "label", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(11), "a", true}, {int64(13), nil, true}, {int64(13), nil, true}})
		s.Bag = true
		run(s)
		for _, q := range []string{"CREATE SCHEMA deparse_a", "CREATE SCHEMA deparse_b", "CREATE VIEW deparse_a.shadow AS SELECT key FROM renamed", "CREATE VIEW deparse_b.shadow AS SELECT id FROM t", "SET search_path=deparse_a,public"} {
			run(fixture.Exec(q, 0))
		}
		a := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('deparse_a.shadow'::text) AS definition")
		heldRun(view, viewQ, a, "shadow")
		run(fixture.Exec("SET search_path=deparse_b,public", 0))
		bb := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('deparse_b.shadow'::text) AS definition")
		heldRun(view, viewQ, bb, "shadow")
		if a == bb {
			t.Fatal("shadow fixture did not distinguish path")
		}
		run(fixture.Exec("RESET search_path", 0))
		run(fixture.Exec("ALTER VIEW v SET SCHEMA deparse_a", 0))
		moved := fixture.DeparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('deparse_a.v'::text) AS definition")
		heldRun(view, viewQ, moved, "deparse_a.v")
		run(fixture.Exec("DROP VIEW current_copy", 0))
		run(fixture.Exec("DROP VIEW deparse_a.v", 0))
		run(fixture.Exec("DROP TABLE renamed CASCADE", 0))
		heldRun(wrap, wrapQ, nil, viewID)
		heldRun(ix, indexQ, nil, indexID, int64(0), false)
		missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "v" does not exist`)
		failure := fixture.ErrorDiagnostic(viewQ, missing, "v")
		failure.SelectRows = true
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: view, SQL: viewQ}, b.Recorder, failure)
	})
	t.Run("clauses-held-type", func(t *testing.T) {
		fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(n INT,s TEXT,p TEXT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(-1,'a ','space'),(1,'a','plain')", 2))
			run(fixture.Exec(`CREATE INDEX cover ON t(n int4_ops,s COLLATE "default" text_ops,lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST) INCLUDE(p)`, 0))
			q := "SELECT pg_get_indexdef($1,$2,false) AS definition"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			check := func(payload string) {
				// TYPE and Restore may replace logical identity: reacquire the actual
				// current OID while retaining the SAME prepared inquiry consumer.
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='cover'").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				full := `CREATE INDEX cover ON public.t USING btree (n, s, lower(s) COLLATE "POSIX" bpchar_ops DESC NULLS LAST) INCLUDE (` + payload + `)`
				for _, tc := range []struct {
					ordinal int64
					want    string
				}{{0, full}, {4, payload}, {5, ""}} {
					s := fixture.DeparseRelationsTextStep(t, b, q, tc.want, oid, tc.ordinal)
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
				}
			}
			check("p")
			run(fixture.Exec("ALTER TABLE t ALTER p TYPE TEXT USING p", 0))
			check("p")
			run(fixture.Exec("ALTER TABLE t RENAME p TO payload", 0))
			check("payload")
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				want := `CREATE INDEX "cover" ON "public"."t" USING btree ("n" ASC NULLS LAST, "s" ASC NULLS LAST, (pg_catalog.lower("s")) COLLATE "pg_catalog"."POSIX" "pg_catalog"."bpchar_ops" DESC NULLS LAST) INCLUDE ("payload");`
				if !strings.Contains(dump.String(), want) {
					t.Fatalf("held inquiry hid failed whole-index TYPE normalization: %s", dump.String())
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
				check("payload")
			}
			run(fixture.Prepared(fixture.Select("SELECT n,s,payload FROM t ORDER BY n", []string{"n", "s", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(-1), "a ", "space"}, {int64(1), "a", "plain"}})))
		})
	})
}
