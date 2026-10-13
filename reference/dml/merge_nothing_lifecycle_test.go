package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

const mergeNothingHeld = "MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED AND $1::boolean THEN DO NOTHING WHEN MATCHED THEN UPDATE SET v=t.v+1 RETURNING merge_action(),old.v,new.v,$2 AS label"

func mergeNothingHeldExercise(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	held, e := b.DB.PrepareContext(p.Ctx, mergeNothingHeld)
	if e != nil {
		t.Fatal(e)
	}
	defer held.Close()
	x := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: mergeNothingHeld}
	for i, flag := range []any{true, false, nil} {
		rows := [][]any{}
		if i > 0 {
			rows = [][]any{{"UPDATE", int64(9 + i), int64(10 + i), "fresh"}}
		}
		fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select(mergeNothingHeld, []string{"merge_action", "v", "v", "label"}, []string{"TEXT", "INT4", "INT4", "TEXT"}, rows, flag, "fresh"))
	}
}
func TestReferenceMergeNothingPrepared(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		mergeNothingHeldExercise(t, p, b)
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}))
		run(fixture.Exec("UPDATE t SET v=10", 1))
		held, e := b.DB.PrepareContext(p.Ctx, mergeNothingHeld)
		if e != nil {
			t.Fatal(e)
		}
		defer held.Close()
		x := fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: mergeNothingHeld}
		for _, flag := range []any{true, false, nil} {
			n := int64(1)
			if flag == true {
				n = 0
			}
			if b.PG {
				n = 0
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Exec(mergeNothingHeld, n, flag, "ignored"))
		}
		run(fixture.Select("SELECT v FROM t", []string{"v"}, []string{"INT4"}, [][]any{{int64(12)}}))
	})
}
func TestReferenceMergeNothingMetadata(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := fixture.MergeRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE t(id INT NOT NULL,v VARCHAR(5),n NUMERIC(8,2))", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,'word',12.30)", 1))
		s := insertReturningBag("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING RETURNING merge_action() AS \"Action\",old.id AS dup,new.id AS dup,v,n", []string{"Action", "dup", "dup", "v", "n"}, []string{"TEXT", "INT4", "INT4", "VARCHAR", "NUMERIC"}, [][]any{}, prepared)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			m, e := rows.ColumnTypes()
			if e != nil {
				t.Fatal(e)
			}
			if n, ok := m[3].Length(); !ok || n != 5 {
				t.Fatal("varchar length", n, ok)
			}
			if n, scale, ok := m[4].DecimalSize(); !ok || n != 8 || scale != 2 {
				t.Fatal("numeric size", n, scale, ok)
			}
			want := reflect.TypeOf("")
			if b.PG {
				want = reflect.TypeOf((*any)(nil)).Elem()
			}
			if m[4].ScanType() != want {
				t.Fatal("numeric transport", m[4].ScanType())
			}
			for _, i := range []int{1, 2} {
				n, ok := m[i].Nullable()
				if b.PG && ok {
					t.Fatal("pq nullability", n, ok)
				}
				if !b.PG && (!ok || !n) {
					t.Fatal("transition nullability", n, ok)
				}
			}
		}
		run(s)
	})
}
func TestReferenceMergeNothingAtomicity(t *testing.T) {
	mergeNothingRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v INT NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(9,90)", 1))
		original := fixture.Select("SELECT * FROM t", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(90)}})
		base := "MERGE INTO t USING (SELECT id,v FROM (VALUES(1,10),(2,20),(3,NULL::integer)) x(id,v) ORDER BY id) s ON FALSE WHEN NOT MATCHED AND s.id=1 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,s.v)"
		d := fixture.Diagnostic{Code: "23502", Message: `null value in column "v" of relation "t" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "t", Column: "v"}
		for _, query := range []bool{false, true} {
			insertReturningFailure(t, p, b, a, base+" RETURNING merge_action(),t.id", prepared, query, d)
			run(original)
			for _, q := range []string{
				"MERGE INTO t USING (SELECT id FROM (VALUES(1),(2),(3)) x(id) ORDER BY id) s ON FALSE WHEN NOT MATCHED AND s.id=1 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,10/(3-s.id)) RETURNING t.id",
				"MERGE INTO t USING (SELECT id FROM (VALUES(1),(2),(3)) x(id) ORDER BY id) s ON FALSE WHEN NOT MATCHED AND s.id=1 THEN DO NOTHING WHEN NOT MATCHED THEN INSERT VALUES(s.id,10) RETURNING 10/(3-t.id)",
			} {
				insertReturningFailure(t, p, b, a, q, prepared, query, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
				run(original)
			}
		}
		for _, savepoint := range []bool{false, true} {
			tx, e := a.BeginTx(p.Ctx, nil)
			if e != nil {
				t.Fatal(e)
			}
			defer tx.Rollback()
			fixture.LockExec(t, p.Ctx, tx, "INSERT INTO t VALUES(8,80)", 1)
			if savepoint {
				fixture.LockExec(t, p.Ctx, tx, "SAVEPOINT kept", 0)
			}
			insertReturningFailure(t, p, b, tx, base+" RETURNING t.id", prepared, true, d)
			if savepoint {
				fixture.LockExec(t, p.Ctx, tx, "ROLLBACK TO kept", 0)
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.Select("SELECT v FROM t WHERE id=8", []string{"v"}, []string{"INT4"}, [][]any{{int64(80)}}))
				healthy := fixture.Exec("MERGE INTO t USING (VALUES(8)) s(id) ON t.id=s.id WHEN MATCHED THEN DO NOTHING", 0)
				healthy.Prepared = prepared
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, healthy)
			} else {
				s := fixture.ImplicitError("SELECT * FROM t", fixture.TransactionAborted())
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, tx, b.Recorder, s)
			}
			if e = tx.Rollback(); e != nil {
				t.Fatal(e)
			}
			run(original)
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, e := a.QueryContext(ctx, base+" RETURNING t.id")
			if rows != nil {
				rows.Close()
				t.Fatal("cancel exposed cursor")
			}
			if !errors.Is(e, context.Canceled) {
				t.Fatal(e)
			}
			run(original)
		}
	})
}
