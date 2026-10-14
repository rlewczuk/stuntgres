package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var seriesStoredRows = [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}, {int64(3), nil}}

const seriesStoredQuery = "SELECT i,b FROM series_view ORDER BY i"

func seriesConsumeStored(t *testing.T, rows *sql.Rows) {
	t.Helper()
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 || cols[0].Name() != "i" || cols[1].Name() != "b" || cols[0].DatabaseTypeName() != "INT4" || cols[1].DatabaseTypeName() != "INT8" {
		t.Fatal("stored metadata", cols)
	}
	var got [][]any
	for rows.Next() {
		var a, b any
		if err := rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{fixture.Normalize(a), fixture.Normalize(b)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, seriesStoredRows) {
		t.Fatalf("stored rows %#v want %#v", got, seriesStoredRows)
	}
	rows.Close()
	if cols[0].DatabaseTypeName() != "INT4" || cols[1].DatabaseTypeName() != "INT8" {
		t.Fatal("closed metadata changed")
	}
}

// A real reader cancels after delivering input bytes to Restore. Cancellation
// is not a pre-admission shortcut and cannot install even the supplied prefix.
type seriesCancelReader struct {
	reader *bytes.Reader
	cancel context.CancelFunc
	read   bool
}

func (r *seriesCancelReader) Read(dst []byte) (int, error) {
	n, err := r.reader.Read(dst)
	if n > 0 {
		r.read = true
		r.cancel()
	}
	return n, err
}

var _ io.Reader = (*seriesCancelReader)(nil)

func seriesControls(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var emitted []byte
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE VIEW series_view AS SELECT generate_series(1,3) AS i,generate_series(10::bigint,11::bigint) AS b", 0))
			run(fixture.Exec("CREATE TABLE series_data AS SELECT i,b FROM series_view", 3))
			check := func() {
				for _, q := range []string{seriesStoredQuery, "SELECT i,b FROM series_data ORDER BY i"} {
					seriesBoth(t, p, b, c, fixture.Select(q, []string{"i", "b"}, []string{"INT4", "INT8"}, seriesStoredRows))
				}
			}
			check()
			if b.PG {
				return
			}
			retained, err := p.Native.QueryContext(p.Ctx, seriesStoredQuery)
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
			if err != nil || !reflect.DeepEqual(again, []string{"i", "b"}) {
				t.Fatal("aliased native Columns", again, err)
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
			run(fixture.Exec("UPDATE series_data SET i=99", 3))
			seriesConsumeStored(t, retained)
			if len(cached) != 2 || cached[0].DatabaseTypeName() != "INT4" || cached[1].DatabaseTypeName() != "INT8" {
				t.Fatal("retained descriptor changed")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("series-restored")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			for _, q := range []string{seriesStoredQuery, "SELECT i,b FROM series_data ORDER BY i"} {
				rows, err := fresh.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				seriesConsumeStored(t, rows)
			}
			// Restore deliberately does not admit INSERT query sources; even a
			// valid earlier prefix and later zero-step SQL cannot replace state.
			bad := "CREATE TABLE series_partial(v integer); INSERT INTO series_partial SELECT generate_series(1,2,0);"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT query sources are unsupported")
			fixture.AssertDiagnostic(t, err, &want)
			check()
			run(fixture.ImplicitError("SELECT * FROM series_partial", fixture.Diagnostic{Code: "42P01", Message: `relation "series_partial" does not exist`, Position: "15"}))
			ctx, cancel := context.WithCancel(p.Ctx)
			reader := &seriesCancelReader{reader: bytes.NewReader(emitted), cancel: cancel}
			err = stuntgres.Restore(ctx, p.Native, reader)
			cancel()
			if !reader.read || !errors.Is(err, context.Canceled) {
				t.Fatal("Restore read cancellation", reader.read, err)
			}
			check()
		})
	}
	if p.Postgres != nil {
		if len(emitted) == 0 {
			t.Fatal("no actual native SQL dump for PG replay")
		}
		for _, q := range []string{"DROP VIEW series_view", "DROP TABLE series_data"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("native emitted SQL replay", err)
		}
		for _, q := range []string{seriesStoredQuery, "SELECT i,b FROM series_data ORDER BY i"} {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Select(q, []string{"i", "b"}, []string{"INT4", "INT8"}, seriesStoredRows))
		}
	}
}

func TestReferenceSeriesControls(t *testing.T) {
	seriesControls(t, fixture.NewScenario(t))
}

func TestReferenceSeriesFeatureSmoke(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT generate_series(1,3) AS v ORDER BY v", []string{"v"}, []string{"INT4"}, seriesRows(1, 2, 3)),
			fixture.Select("SELECT generate_series(5::bigint,1::bigint,-2::bigint) AS v ORDER BY v DESC", []string{"v"}, []string{"INT8"}, seriesRows(5, 3, 1)),
			fixture.Select("SELECT generate_series(NULL::bigint,3::bigint) AS v", []string{"v"}, []string{"INT8"}, nil),
			fixture.Select("SELECT generate_series(2147483646,2147483647,2) AS v", []string{"v"}, []string{"INT4"}, seriesRows(2147483646)),
			fixture.Select("SELECT generate_series(1::bigint,9223372036854775807::bigint) AS v LIMIT 3", []string{"v"}, []string{"INT8"}, seriesRows(1, 2, 3)),
			fixture.Select("SELECT generate_series(1,3) AS a,generate_series(10,11) AS b ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}, {int64(3), nil}}),
		} {
			s.NonContext = true
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			s.NonContext = false
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		const q = "SELECT generate_series($1,3,$2) AS v ORDER BY v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""), int64(1), int64(0))
		s.SelectRows = true
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, seriesRows(1, 2, 3), int64(1), int64(1)))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE series_smoke_sink(v integer)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO series_smoke_sink VALUES(99)", 1))
		const bad = "INSERT INTO series_smoke_sink SELECT generate_series(1,2,step) FROM (VALUES(1,1),(2,0)) s(id,step)"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(bad, seriesDiagnostic(bad, "", "22023", "step size cannot equal zero", "")))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM series_smoke_sink", []string{"v"}, []string{"INT4"}, seriesRows(99)))
	})
	seriesControls(t, fixture.NewScenario(t))
}
