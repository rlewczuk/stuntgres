package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryCTEAuthority(t *testing.T) {
	t.Run("prepared lookup and signature", cteAuthorityFreshness)
	fixture.RoleReferenceCleanup(t, []string{"cte_reader"})
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		cteFixture(run)
		subqueryBoth(run, cteBag("WITH a AS (SELECT id FROM t WHERE id=$1), b AS (SELECT id+$1 AS n FROM a) SELECT n FROM b WHERE n=$2", []string{"n"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(4)}}, int64(2), int64(4)))
		q := "WITH c AS MATERIALIZED (SELECT id FROM t) SELECT id FROM c ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}}))
		tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}}))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t VALUES(3,30)", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Prepared(fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}})))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Exec("INSERT INTO t VALUES(4,40)", 1))
		fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(4)}}))
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(4)}}))
		run(fixture.Exec("CREATE ROLE cte_reader", 0))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cte_reader", 0))
		}
		run(fixture.Exec("GRANT SELECT(id) ON t TO cte_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION cte_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		subqueryBoth(run, cteBag("WITH c(k) AS (SELECT id FROM t) SELECT k FROM c", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(2)}, {int64(3)}, {int64(4)}}))
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t")
		s := fixture.ErrorDiagnostic("WITH c AS (SELECT v FROM t) SELECT * FROM c", want)
		s.SelectRows = true
		s.ErrorStage = "execute"
		subqueryBoth(run, s)
		for _, suffix := range []string{" LIMIT 0", " WHERE FALSE"} {
			denied := fixture.ErrorDiagnostic("WITH c AS (SELECT v FROM t) SELECT * FROM c"+suffix, want)
			denied.SelectRows = true
			denied.ErrorStage = "execute"
			subqueryBoth(run, denied)
		}
		// Unused SELECT definitions are analyzed but omitted from executable
		// permission checks. Missing names still fail in Diagnostics above.
		subqueryBoth(run, cteBag("WITH c AS (SELECT v FROM t) SELECT 7 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(7)}}))
		// A held statement must recheck current authority, not cache the grant.
		if _, err := b.DB.ExecContext(p.Ctx, "REVOKE SELECT(id) ON t FROM cte_reader"); err != nil {
			t.Fatal(err)
		}
		denied := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table t"))
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
	})
}

func TestReferenceQueryCTELifecycle(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		cteFixture(run)
		q := "WITH c AS MATERIALIZED (SELECT id,v FROM t) SELECT * FROM c ORDER BY id"
		if b.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancel(t, p.Ctx, c, prepared, true)
				subqueryBoth(run, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}))
			}
			return
		}
		for _, prepared := range []bool{false, true} {
			var x fixture.Executor = c
			if prepared {
				s, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				x = fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := x.QueryContext(ctx, q)
			if rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled publication", rows, err)
			}
		}
		rows, err := c.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(names, []string{"id", "v"}) {
			t.Fatal(names, err)
		}
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "INT4" {
				t.Fatal(typ.DatabaseTypeName())
			}
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("INSERT INTO t VALUES(9,90)", 1))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		missing := fixture.ControlsReferenceDiagnostic("42P01", `relation "t" does not exist`)
		missing.Position = "42"
		subqueryAnalysisFailure(run, q, missing)
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		for _, want := range [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}} {
			if !rows.Next() {
				t.Fatal("retained row missing", rows.Err())
			}
			var id, value any
			if err := rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]any{id, value}, want) {
				t.Fatal(id, value, want)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained suffix", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		subqueryBoth(run, fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {int64(2), nil}}))
	})
}
