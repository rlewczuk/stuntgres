package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Both adapters report Nullable as unknown (false/false), including empty results.
func rowCompareBoolMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"answer"}) {
		t.Fatal("columns", columns)
	}
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 {
		t.Fatal("metadata width", len(meta))
	}
	m := meta[0]
	if m.Name() != "answer" || m.DatabaseTypeName() != "BOOL" || m.ScanType() != reflect.TypeFor[bool]() {
		t.Fatal("BOOL metadata", m.Name(), m.DatabaseTypeName(), m.ScanType())
	}
	if length, known := m.Length(); length != 0 || known {
		t.Fatal("Length", length, known)
	}
	if precision, scale, known := m.DecimalSize(); precision != 0 || scale != 0 || known {
		t.Fatal("DecimalSize", precision, scale, known)
	}
	if nullable, known := m.Nullable(); nullable || known {
		t.Fatal("Nullable", nullable, known)
	}
}

func TestReferenceRowCompareMetadata(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, expr := range []string{
			"ROW(1,2) = ROW(1,2)",
			"ROW(NULL::int,1) = ROW(NULL::int,1)",
			"ROW(NULL::int,1) IS DISTINCT FROM ROW(NULL::int,1)",
			"ROW() IS NOT DISTINCT FROM ROW()",
		} {
			for _, empty := range []bool{false, true} {
				q := "SELECT " + expr + " AS answer"
				var value any = true
				if expr == "ROW(NULL::int,1) = ROW(NULL::int,1)" {
					value = nil
				} else if expr == "ROW(NULL::int,1) IS DISTINCT FROM ROW(NULL::int,1)" {
					value = false
				}
				want := [][]any{{value}}
				if empty {
					q += " WHERE FALSE"
					want = nil
				}
				t.Run(q, func(t *testing.T) {
					s := fixture.Select(q, []string{"answer"}, []string{"BOOL"}, want)
					s.CheckMetadata = rowCompareBoolMetadata
					subqueryBoth(subqueryRun(t, p, b, c), s)
				})
			}
		}
	})
}

func TestReferenceRowCompareHeld(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ROW($1::int,$2::int) IS DISTINCT FROM ROW(NULL::int,1) AS answer"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		var retained any
		for i, tc := range []struct {
			a, b any
			want bool
		}{{nil, 1, false}, {nil, 2, true}, {7, 1, true}, {nil, 1, false}} {
			s := fixture.Select(q, []string{"answer"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.a, tc.b)
			s.CheckMetadata = rowCompareBoolMetadata
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			rows, err := stmt.QueryContext(p.Ctx, tc.a, tc.b)
			if err != nil {
				t.Fatal(err)
			}
			rowCompareBoolMetadata(t, rows)
			if !rows.Next() {
				rows.Close()
				t.Fatal("missing held row", rows.Err())
			}
			var cell any
			if err := rows.Scan(&cell); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if cell != tc.want {
				rows.Close()
				t.Fatal("held cell", cell, tc.want)
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal("held tail", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				retained = cell
			}
			if retained != false {
				t.Fatal("retained held export changed", retained)
			}
		}
		// Prepare an empty-result sibling without changing the held signature.
		s := fixture.Prepared(fixture.Select(q+" WHERE FALSE", []string{"answer"}, []string{"BOOL"}, nil, nil, 1))
		s.CheckMetadata = rowCompareBoolMetadata
		subqueryRun(t, p, b, c)(s)
	})
}

func rowCompareStoredRead(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, second bool) {
	t.Helper()
	run := subqueryRun(t, p, b, x)
	subqueryBoth(run, fixture.Select("SELECT k,g,ok FROM rc ORDER BY k", []string{"k", "g", "ok"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{
		{int64(1), true, true}, {int64(2), second, true}, {int64(3), false, true},
	}))
	var equality any
	if !second {
		equality = false
	}
	subqueryBoth(run, fixture.Select("SELECT k,eq,dist,zero,one,expanded,typed FROM rc_view ORDER BY k", []string{"k", "eq", "dist", "zero", "one", "expanded", "typed"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{
		{int64(1), nil, false, true, true, true, true},
		{int64(2), equality, !second, true, true, second, true},
		{int64(3), false, true, true, true, false, true},
	}))
}

func rowCompareStoredFuture(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor) {
	t.Helper()
	run := subqueryRun(t, p, b, x)
	run(fixture.Prepared(fixture.Exec("UPDATE rc SET b=$1 WHERE k=$2", 1, 1, 2)))
	rowCompareStoredRead(t, p, b, x, true)
	run(fixture.Exec("INSERT INTO rc(k,a,b) VALUES(4,NULL,1)", 1))
	subqueryBoth(run, fixture.Select("SELECT g,ok FROM rc WHERE k=4", []string{"g", "ok"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
	run(fixture.Exec("DELETE FROM rc WHERE k=4", 1))
}

func TestReferenceRowCompareStored(t *testing.T) {
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		for _, q := range []string{
			"CREATE DOMAIN rc_label AS text",
			"CREATE TABLE rc(k int,a int,b int,g bool GENERATED ALWAYS AS (ROW(a,b) IS NOT DISTINCT FROM ROW(NULL::int,1)) STORED,ok bool DEFAULT (ROW() IS NOT DISTINCT FROM ROW()),CONSTRAINT rc_nonzero CHECK (ROW(a,b) IS DISTINCT FROM ROW(0,0)))",
			"CREATE INDEX rc_expr ON rc ((ROW(a,b) IS DISTINCT FROM ROW(NULL::int,1))) WHERE ROW(a,b) IS DISTINCT FROM ROW(0,0)",
			"CREATE VIEW rc_view AS SELECT k,ROW(a,b)=ROW(NULL::int,1) AS eq,ROW(a,b) IS DISTINCT FROM ROW(NULL::int,1) AS dist,ROW() IS NOT DISTINCT FROM ROW() AS zero,ROW(a) IS NOT DISTINCT FROM ROW(a) AS one,ROW(t.*) IS NOT DISTINCT FROM ROW(k,NULL::int,1,g,ok) AS expanded,ROW('fixed'::rc_label,k) IS NOT DISTINCT FROM ROW('fixed'::rc_label,k) AS typed FROM rc t",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO rc(k,a,b) VALUES(1,NULL,1),(2,NULL,2),(3,2,2)", 3))
		rowCompareStoredRead(t, p, b, c, false)
		// Full CHECK diagnostic validated against pinned PostgreSQL 18.0.
		want := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "rc" violates check constraint "rc_nonzero"`)
		want.Detail = "Failing row contains (4, 0, 0, f, t)."
		want.Schema, want.Table, want.Constraint = "public", "rc", "rc_nonzero"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO rc(k,a,b) VALUES(4,0,0)", want)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			rowCompareStoredRead(t, p, b, c, false)
		}
		rows, err := c.QueryContext(p.Ctx, "SELECT ROW(a,b) IS NOT DISTINCT FROM ROW(NULL::int,1) AS answer FROM rc ORDER BY k")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		rowCompareBoolMetadata(t, rows)
		if !rows.Next() {
			t.Fatal("missing stored row", rows.Err())
		}
		var first any
		if err := rows.Scan(&first); err != nil || first != true {
			t.Fatal("stored first", first, err)
		}
		var dump bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		// PostgreSQL consumes its real SQL cursor; native consumes the eager
		// cursor retained across catalog replacement, with the same assertions.
		rowCompareBoolMetadata(t, rows)
		for range 2 {
			if !rows.Next() {
				t.Fatal("missing retained row", rows.Err())
			}
			var cell any
			if err := rows.Scan(&cell); err != nil || cell != false {
				t.Fatal("retained stored cell", cell, err)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("retained tail", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if first != true {
			t.Fatal("retained export changed", first)
		}
		rowCompareStoredRead(t, p, b, c, false)
		rowCompareStoredFuture(t, p, b, c)
		if !b.PG && p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("actual row-comparison Dump replay", err, dump.String())
			}
			pg := fixture.Backend{PG: true, Recorder: p.PostgresRecord}
			rowCompareStoredRead(t, p, pg, p.Postgres, false)
			rowCompareStoredFuture(t, p, pg, p.Postgres)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
