package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var numericSeriesStoredRows = [][]any{{"0.1", int64(10)}, {"0.20", int64(11)}, {"0.30", nil}}

func numericSeriesConsumeStored(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || cols[0].Name() != "v" || cols[1].Name() != "i" || cols[0].DatabaseTypeName() != "NUMERIC" || cols[1].DatabaseTypeName() != "INT4" {
		t.Fatal("stored metadata", cols)
	}
	var got [][]any
	for rows.Next() {
		var v, i any
		if err := rows.Scan(&v, &i); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{fixture.Normalize(v), fixture.Normalize(i)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, numericSeriesStoredRows) {
		t.Fatal(got, numericSeriesStoredRows)
	}
	rows.Close()
	if cols[0].DatabaseTypeName() != "NUMERIC" {
		t.Fatal("closed metadata changed")
	}
}

func numericSeriesControls(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var emitted []byte
	queries := []string{"SELECT v,i FROM numeric_series_view ORDER BY v", "SELECT v,i FROM numeric_series_data ORDER BY v"}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE VIEW numeric_series_view AS SELECT generate_series(0.1,0.3,0.10) AS v,generate_series(10,11) AS i", 0))
			run(fixture.Exec("CREATE TABLE numeric_series_data AS SELECT v,i FROM numeric_series_view", 3))
			check := func() {
				for _, q := range queries {
					seriesBoth(t, p, b, c, fixture.Select(q, []string{"v", "i"}, []string{"NUMERIC", "INT4"}, numericSeriesStoredRows))
				}
			}
			check()
			if b.PG {
				return
			}
			retained, err := p.Native.QueryContext(p.Ctx, queries[0])
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			names, err := retained.Columns()
			if err != nil {
				t.Fatal(err)
			}
			names[0] = "detached"
			again, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(again, []string{"v", "i"}) {
				t.Fatal(again, err)
			}
			cached, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = bytes.Clone(dump.Bytes())
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			var redump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(emitted, redump.Bytes()) {
				t.Fatal("nondeterministic numeric replay")
			}
			run(fixture.Exec("UPDATE numeric_series_data SET v=99", 3))
			numericSeriesConsumeStored(t, retained)
			if len(cached) != 2 || cached[0].DatabaseTypeName() != "NUMERIC" {
				t.Fatal("retained descriptor changed")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("numeric-series-restored")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			for _, q := range queries {
				rows, err := fresh.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				numericSeriesConsumeStored(t, rows)
			}
			// A valid supplied prefix cannot leak through a later supported
			// INSERT input failure or the existing query-source exclusion.
			bad := "CREATE TABLE numeric_series_partial(v numeric); INSERT INTO numeric_series_partial VALUES('oops');"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			fixture.State(t, err, "22P02")
			check()
			bad = "CREATE TABLE numeric_series_partial(v numeric); INSERT INTO numeric_series_partial SELECT generate_series(0.1,0.3,0);"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT query sources are unsupported")
			fixture.AssertDiagnostic(t, err, &want)
			check()
			run(fixture.ImplicitError("SELECT * FROM numeric_series_partial", fixture.Diagnostic{Code: "42P01", Message: `relation "numeric_series_partial" does not exist`, Position: "15"}))
			ctx, cancel := context.WithCancel(p.Ctx)
			reader := &seriesCancelReader{reader: bytes.NewReader(emitted), cancel: cancel}
			err = stuntgres.Restore(ctx, p.Native, reader)
			cancel()
			if !reader.read || !errors.Is(err, context.Canceled) {
				t.Fatal(reader.read, err)
			}
			check()
		})
	}
	if p.Postgres != nil {
		if len(emitted) == 0 {
			t.Fatal("missing native dump")
		}
		for _, q := range []string{"DROP VIEW numeric_series_view", "DROP TABLE numeric_series_data"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("native SQL replay", err)
		}
		for _, q := range queries {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(q, []string{"v", "i"}, []string{"NUMERIC", "INT4"}, numericSeriesStoredRows))
		}
	}
}

func TestReferenceNumericSeriesControls(t *testing.T) {
	numericSeriesControls(t, fixture.NewScenario(t))
}

func TestReferenceNumericSeriesFeatureSmoke(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT generate_series(0.1,0.3,0.1) AS v ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.2", "0.3")),
			fixture.Select("SELECT generate_series(0.3,0.1,-0.1) AS v ORDER BY v DESC", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.3", "0.2", "0.1")),
			fixture.Select("SELECT generate_series(0.1,0.3,0.10) AS v ORDER BY v", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.20", "0.30")),
			fixture.Select("SELECT generate_series(0.1,1e100,0.1) AS v LIMIT 3", []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.2", "0.3")),
			fixture.Select("SELECT a,b,n FROM ROWS FROM(generate_series(0.1,0.3,0.1),generate_series(10,11)) WITH ORDINALITY s(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"NUMERIC", "INT4", "INT8"}, [][]any{{"0.1", int64(10), int64(1)}, {"0.2", int64(11), int64(2)}, {"0.3", nil, int64(3)}}),
		} {
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				s.NonContext = false
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		s := fixture.Select("SELECT generate_series(NULL::numeric,1,0) AS v", []string{"v"}, []string{"NUMERIC"}, nil)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { numericSeriesMetadata(t, rows, b.PG, false) }
		seriesBoth(t, p, b, c, s)
		const q = "SELECT generate_series($1,0.3::numeric,$2) AS v ORDER BY v"
		st, err := b.DB.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: st, SQL: q}
		for _, step := range []string{"0", "NaN", "0.1"} {
			if step != "0.1" {
				message := "step size cannot equal zero"
				if step == "NaN" {
					message = "step size cannot be NaN"
				}
				s = fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", message, ""), "0.1", step)
				s.SelectRows, s.ErrorStage = true, "execute"
			} else {
				s = fixture.Select(q, []string{"v"}, []string{"NUMERIC"}, numericSeriesRows("0.1", "0.2", "0.3"), "0.1", step)
			}
			s.NonContext = true
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			s.NonContext = false
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		numericSeriesAtomicity(t, p, b, c, false)
	})
	numericSeriesControls(t, fixture.NewScenario(t))
}
