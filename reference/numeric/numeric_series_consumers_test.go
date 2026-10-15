package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func numericSeriesMetadata(t *testing.T, rows *sql.Rows, pg, relation bool) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 1 {
		t.Fatalf("columns=%v", cols)
	}
	col := cols[0]
	precision, scale, sized := col.DecimalSize()
	length, hasLength := col.Length()
	nullable, known := col.Nullable()
	scan := reflect.TypeFor[string]()
	wantPrecision, wantScale, wantSized := int64(0), int64(0), false
	if pg {
		scan = reflect.TypeFor[any]()
		wantPrecision, wantScale, wantSized = 65535, 65531, true
	}
	// Derived expressions expose no nullability proof. Stored relation columns
	// (including views) retain their existing catalog-owned nullable contract.
	wantNullable := relation && !pg
	if col.DatabaseTypeName() != "NUMERIC" || col.ScanType() != scan || precision != wantPrecision || scale != wantScale || sized != wantSized || length != 0 || hasLength || nullable != wantNullable || known != wantNullable {
		t.Fatalf("metadata type=%s scan=%v decimal=(%d,%d,%v) length=(%d,%v) nullable=(%v,%v)", col.DatabaseTypeName(), col.ScanType(), precision, scale, sized, length, hasLength, nullable, known)
	}
}

func TestReferenceNumericSeriesMetadata(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q, name string
			rows    [][]any
		}{
			{"SELECT generate_series(0.1,0.3,0.1) ORDER BY 1", "generate_series", numericSeriesRows("0.1", "0.2", "0.3")},
			{"SELECT generate_series(NULL::numeric,1,0) AS v", "v", nil},
			{"SELECT generate_series(1::numeric(3,2),2::numeric(3,2)) AS v ORDER BY v", "v", numericSeriesRows("1.00", "2.00")},
			{"SELECT v FROM generate_series(0.3,0.1,0.1) s(v)", "v", nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := fixture.Select(tc.q, []string{tc.name}, []string{"NUMERIC"}, tc.rows)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { numericSeriesMetadata(t, rows, b.PG, false) }
				seriesBoth(t, p, b, c, s)
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW numeric_series_metadata_view AS SELECT generate_series(0.1,0.3,0.1) AS v", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE numeric_series_metadata_data AS SELECT * FROM numeric_series_metadata_view", 3))
		for _, name := range []string{"numeric_series_metadata_view", "numeric_series_metadata_data"} {
			s := fixture.Select("SELECT v FROM "+name+" ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.2", "0.3"))
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { numericSeriesMetadata(t, rows, b.PG, true) }
			seriesBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceNumericSeriesHeldParameters(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "SELECT generate_series($1,0.3::numeric,$2) AS v ORDER BY v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, tc := range []struct {
			start, step any
			values      []string
			failure     bool
		}{
			{"0.1", "0.1", []string{"0.1", "0.2", "0.3"}, false},
			{"0.3", "0.1", []string{"0.3"}, false},
			{"0.4", "0.1", nil, false},
			{nil, "0", nil, false},
			{"0.1", nil, nil, false},
			{"0.1", "0", nil, true},
			{[]byte("0.1"), []byte("0.1"), []string{"0.1", "0.2", "0.3"}, false},
			{int64(0), "0.1", []string{"0", "0.1", "0.2", "0.3"}, false},
		} {
			var s fixture.Step
			if tc.failure {
				s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""), tc.start, tc.step)
				s.SelectRows, s.ErrorStage = true, "execute"
			} else {
				s = fixture.Select(q, []string{"v"}, []string{"NUMERIC"}, numericSeriesRows(tc.values...), tc.start, tc.step)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { numericSeriesMetadata(t, rows, b.PG, false) }
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		bad := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22P02", `invalid input syntax for type numeric: "oops"`, ""), "oops", "0.1")
		bad.SelectRows, bad.ErrorStage = true, "execute"
		bad.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		start, step := []byte("0.1"), []byte("0.1")
		rows, err := st.QueryContext(p.Ctx, start, step)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		start[0], step[0] = '9', '9'
		for _, want := range []string{"0.1", "0.2", "0.3"} {
			if !rows.Next() {
				t.Fatal("missing owned bind row", rows.Err())
			}
			var got string
			if err := rows.Scan(&got); err != nil || got != want {
				t.Fatal(got, err)
			}
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("owned bind tail", rows.Err())
		}
		rows.Close()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE numeric_series_held_sink(v numeric)", 0))
		const insert = "INSERT INTO numeric_series_held_sink SELECT generate_series($1,0.3::numeric,$2)"
		writer, err := b.DB.Prepare(insert)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		heldWriter := fixture.HeldStatement{Executor: b.DB, Statement: writer, SQL: insert}
		for _, plain := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(insert, seriesDiagnostic(insert, "", "22023", "step size cannot equal zero", ""), "0.1", "0")
			s.ErrorStage, s.NonContext = "execute", plain
			fixture.RunStep(t, p.Ctx, heldWriter, b.Recorder, s)
			s = fixture.Exec(insert, 3, "0.1", "0.1")
			s.NonContext = plain
			fixture.RunStep(t, p.Ctx, heldWriter, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM numeric_series_held_sink ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.1", "0.2", "0.2", "0.3", "0.3")))
	})
}

func TestReferenceNumericSeriesComposition(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			q           string
			cols, types []string
			rows        [][]any
		}{
			{"SELECT generate_series(0.1,0.3,0.1) AS a,generate_series(10,11) AS b ORDER BY a", []string{"a", "b"}, []string{"NUMERIC", "INT4"}, [][]any{{"0.1", int64(10)}, {"0.2", int64(11)}, {"0.3", nil}}},
			{"SELECT a,b,n FROM ROWS FROM(generate_series(0.1,0.3,0.1),generate_series(10,11)) WITH ORDINALITY s(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"NUMERIC", "INT4", "INT8"}, [][]any{{"0.1", int64(10), int64(1)}, {"0.2", int64(11), int64(2)}, {"0.3", nil, int64(3)}}},
			{"SELECT a,b FROM ROWS FROM(generate_series(NULL::numeric,1),generate_series(10,11)) s(a,b) ORDER BY b", []string{"a", "b"}, []string{"NUMERIC", "INT4"}, [][]any{{nil, int64(10)}, {nil, int64(11)}}},
			{"SELECT generate_series(0.1,0.3,0.1)+1 AS v ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("1.1", "1.2", "1.3")},
			{"SELECT id,v,n FROM (VALUES(1,0.1),(1,0.1),(2,NULL::numeric),(3,0.4)) s(id,lo),generate_series(lo,0.2,0.1) WITH ORDINALITY g(v,n) ORDER BY id,v,n", []string{"id", "v", "n"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(1), "0.1", int64(1)}, {int64(1), "0.1", int64(1)}, {int64(1), "0.2", int64(2)}, {int64(1), "0.2", int64(2)}}},
			{"SELECT generate_series(0.1,generate_series(0.1,0.2,0.1),0.1) AS v ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.1", "0.2")},
			{"SELECT id,v,n FROM (VALUES(1,0.1),(1,0.1)) s(id,lo) CROSS JOIN LATERAL generate_series(lo,0.2,0.1) WITH ORDINALITY g(v,n) ORDER BY id,v,n", []string{"id", "v", "n"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(1), "0.1", int64(1)}, {int64(1), "0.1", int64(1)}, {int64(1), "0.2", int64(2)}, {int64(1), "0.2", int64(2)}}},
		} {
			t.Run(tc.q, func(t *testing.T) { seriesBoth(t, p, b, c, fixture.Select(tc.q, tc.cols, tc.types, tc.rows)) })
		}
	})
}
