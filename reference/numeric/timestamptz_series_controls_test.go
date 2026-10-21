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

func timestamptzSeriesRetained(t *testing.T, rows *sql.Rows, expected [][]any) {
	t.Helper()
	defer rows.Close()
	meta, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta) != 1 || meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMPTZ" {
		t.Fatal("retained schema", meta)
	}
	var got [][]any
	for rows.Next() {
		var v any
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{fixture.Normalize(v)})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("retained values", got, expected)
	}
	rows.Close()
	if meta[0].Name() != "v" || meta[0].DatabaseTypeName() != "TIMESTAMPTZ" {
		t.Fatal("closed schema changed")
	}
}

func TestReferenceTimestamptzSeriesControls(t *testing.T) {
	p := fixture.NewScenario(t)
	var emitted []byte
	const three = "generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day')"
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("SET TIME ZONE 'America/New_York'", 0))
			run(fixture.Exec("CREATE VIEW timestamptz_series_session AS SELECT "+three+" AS v", 0))
			run(fixture.Exec("CREATE VIEW timestamptz_series_explicit AS SELECT "+timestamptzSeriesNY+" AS v", 0))
			run(fixture.Exec("CREATE TABLE timestamptz_series_data AS SELECT v FROM timestamptz_series_session", 3))
			check := func() {
				for _, zone := range []string{"UTC", "America/New_York", "UTC"} {
					run(fixture.Exec("SET TIME ZONE '"+zone+"'", 0))
					for _, name := range []string{"session", "explicit", "data"} {
						want := timestamptzSeriesSpring()
						if name == "session" && zone == "UTC" {
							want = timestamptzSeriesUTC()
						}
						timestampSeriesBoth(t, p, b, c, fixture.Select("SELECT v FROM timestamptz_series_"+name+" ORDER BY v", []string{"v"}, []string{"TIMESTAMPTZ"}, want))
					}
				}
			}
			check()
			if b.PG {
				return
			}
			retained, err := c.QueryContext(p.Ctx, "SELECT v FROM timestamptz_series_data ORDER BY v")
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
			if err != nil || !reflect.DeepEqual(again, []string{"v"}) {
				t.Fatal(again, err)
			}
			meta, err := retained.ColumnTypes()
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
				t.Fatal("nondeterministic timezone series dump")
			}
			run(fixture.Exec("UPDATE timestamptz_series_data SET v=TIMESTAMPTZ '1999-01-01+00'", 3))
			timestamptzSeriesRetained(t, retained, timestamptzSeriesSpring())
			if meta[0].DatabaseTypeName() != "TIMESTAMPTZ" {
				t.Fatal("cached schema changed")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("timestamptz-series-restored")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			freshConn, err := fresh.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer freshConn.Close()
			fixture.RunStep(t, p.Ctx, freshConn, b.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
			for _, name := range []string{"session", "explicit", "data"} {
				want := timestamptzSeriesSpring()
				if name == "session" {
					want = timestamptzSeriesUTC()
				}
				rows, err := freshConn.QueryContext(p.Ctx, "SELECT v FROM timestamptz_series_"+name+" ORDER BY v")
				if err != nil {
					t.Fatal(err)
				}
				timestamptzSeriesRetained(t, rows, want)
			}
			bad := "CREATE TABLE timestamptz_series_partial(v timestamptz); INSERT INTO timestamptz_series_partial VALUES('oops');"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			fixture.State(t, err, "22007")
			check()
			bad = "CREATE TABLE timestamptz_series_partial(v timestamptz); INSERT INTO timestamptz_series_partial SELECT generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '0 days','UTC');"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT query sources are unsupported")
			fixture.AssertDiagnostic(t, err, &want)
			check()
			run(fixture.ImplicitError("SELECT * FROM timestamptz_series_partial", fixture.Diagnostic{Code: "42P01", Message: `relation "timestamptz_series_partial" does not exist`, Position: "15"}))
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
			t.Fatal("missing native timezone-series dump")
		}
		for _, q := range []string{"DROP VIEW timestamptz_series_session", "DROP VIEW timestamptz_series_explicit", "DROP TABLE timestamptz_series_data"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("native dump PostgreSQL replay", err)
		}
		c, err := p.Postgres.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		fixture.RunStep(t, p.Ctx, c, p.PostgresRecord, fixture.Exec("SET TIME ZONE 'UTC'", 0))
		for _, name := range []string{"session", "explicit", "data"} {
			want := timestamptzSeriesSpring()
			if name == "session" {
				want = timestamptzSeriesUTC()
			}
			fixture.TemporalRunStep(t, p.Ctx, c, p.PostgresRecord, fixture.Select("SELECT v FROM timestamptz_series_"+name+" ORDER BY v", []string{"v"}, []string{"TIMESTAMPTZ"}, want))
		}
	}
}
