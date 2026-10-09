package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceDeparseRelationsPreparedLifecycle(t *testing.T) {
	schemaReferenceCleanup(t, "deparse_a", "deparse_b")
	deparseRelationsScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		deparseRelationsSeed(t, p, b, c)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		hold := func(q string) *sql.Stmt {
			s, err := c.PrepareContext(p.ctx, q)
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
		if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='ix'").Scan(&indexID); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.ctx, "SELECT oid FROM pg_class WHERE relname='v'").Scan(&viewID); err != nil {
			t.Fatal(err)
		}
		heldRun := func(stmt *sql.Stmt, q string, want any, args ...any) {
			referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}, b.recorder, deparseRelationsTextStep(t, b, q, want, args...))
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
		rows, err := view.QueryContext(p.ctx, "v")
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
		deparseRelationsMetadata(t, b.pg, rows)
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
			run(referenceExec(q, count))
		}
		if referenceNormalize(retained) != compact {
			t.Fatal("returned definition changed", retained)
		}
		heldRun(ix, indexQ, "key", indexID, int64(1), false)
		renamed := " SELECT key AS output,\n    txt AS label\n   FROM renamed\n  WHERE visible;"
		heldRun(view, viewQ, renamed, "v")
		run(referenceExec("CREATE OR REPLACE VIEW v AS SELECT key+10 AS output,txt AS label,true AS extra FROM renamed WHERE visible", 0))
		definition := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('v') AS definition")
		heldRun(view, viewQ, definition, "v")
		run(referenceExec("CREATE VIEW current_copy AS "+definition, 0))
		s := referenceSelect("SELECT * FROM current_copy", []string{"output", "label", "extra"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(11), "a", true}, {int64(13), nil, true}, {int64(13), nil, true}})
		s.bag = true
		run(s)
		for _, q := range []string{"CREATE SCHEMA deparse_a", "CREATE SCHEMA deparse_b", "CREATE VIEW deparse_a.shadow AS SELECT key FROM renamed", "CREATE VIEW deparse_b.shadow AS SELECT id FROM t", "SET search_path=deparse_a,public"} {
			run(referenceExec(q, 0))
		}
		a := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('deparse_a.shadow'::text) AS definition")
		heldRun(view, viewQ, a, "shadow")
		run(referenceExec("SET search_path=deparse_b,public", 0))
		bb := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('deparse_b.shadow'::text) AS definition")
		heldRun(view, viewQ, bb, "shadow")
		if a == bb {
			t.Fatal("shadow fixture did not distinguish path")
		}
		run(referenceExec("RESET search_path", 0))
		run(referenceExec("ALTER VIEW v SET SCHEMA deparse_a", 0))
		moved := deparseRelationsRead(t, p, b, c, "SELECT pg_get_viewdef('deparse_a.v'::text) AS definition")
		heldRun(view, viewQ, moved, "deparse_a.v")
		run(referenceExec("DROP VIEW current_copy", 0))
		run(referenceExec("DROP VIEW deparse_a.v", 0))
		run(referenceExec("DROP TABLE renamed CASCADE", 0))
		heldRun(wrap, wrapQ, nil, viewID)
		heldRun(ix, indexQ, nil, indexID, int64(0), false)
		missing := controlsReferenceDiagnostic("42P01", `relation "v" does not exist`)
		failure := referenceErrorDiagnostic(viewQ, missing, "v")
		failure.selectRows = true
		referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: c, statement: view, query: viewQ}, b.recorder, failure)
	})
}
