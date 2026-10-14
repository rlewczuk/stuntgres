package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var srfFromStoredRows = [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), int64(1), int64(1)}, {int64(3), nil, nil}}
var srfFromMixedRows = [][]any{{int64(1), "x", int64(0), int64(1)}, {int64(2), "y", int64(1), int64(2)}, {int64(3), nil, nil, int64(3)}}

func srfFromConsume(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != len(names) {
		t.Fatal(cols)
	}
	for i, col := range cols {
		if col.Name() != names[i] || col.DatabaseTypeName() != types[i] {
			t.Fatalf("column %d: %s/%s", i, col.Name(), col.DatabaseTypeName())
		}
	}
	var got [][]any
	for rows.Next() {
		row := make([]any, len(names))
		dest := make([]any, len(names))
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i := range row {
			row[i] = fixture.Normalize(row[i])
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rows %#v want %#v", got, want)
	}
}

func srfFromControls(t *testing.T, p *fixture.Pair) {
	t.Helper()
	var emitted []byte
	queries := []fixture.Step{
		fixture.Select("SELECT * FROM srf_control_view ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, srfFromStoredRows),
		fixture.Select("SELECT * FROM srf_control_data ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, srfFromStoredRows),
		fixture.Select("SELECT * FROM srf_control_mixed ORDER BY n", []string{"v", "s", "i", "n"}, []string{"INT4", "TEXT", "INT4", "INT8"}, srfFromMixedRows),
	}
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, s := range []fixture.Step{
				fixture.Exec("CREATE TABLE srf_control_input(id integer,hi integer)", 0),
				fixture.Exec("INSERT INTO srf_control_input VALUES(1,2),(2,1),(3,0)", 3),
				fixture.Exec("CREATE VIEW srf_control_view AS SELECT id,v,n FROM srf_control_input s LEFT JOIN LATERAL generate_series(1,s.hi) WITH ORDINALITY g(v,n) ON true", 0),
				fixture.Exec("CREATE VIEW srf_control_mixed AS SELECT * FROM ROWS FROM(generate_series(1,3),string_to_table('x,y',','),generate_subscripts('[0:1]={NULL,NULL}'::integer[],1)) WITH ORDINALITY g(v,s,i,n)", 0),
				fixture.Exec("CREATE TABLE srf_control_data AS SELECT * FROM srf_control_view", 4),
				fixture.Exec("ALTER TABLE srf_control_input RENAME COLUMN hi TO bound", 0),
				fixture.Exec("ALTER TABLE srf_control_input RENAME TO srf_control_renamed", 0),
			} {
				run(s)
			}
			check := func() {
				for _, s := range queries {
					seriesBoth(t, p, b, c, s)
				}
			}
			check()
			if b.PG {
				return
			}
			retained, err := p.Native.QueryContext(p.Ctx, queries[0].Query)
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
			if err != nil || !reflect.DeepEqual(again, queries[0].Columns) {
				t.Fatal("aliased columns", again, err)
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
			run(fixture.Exec("UPDATE srf_control_renamed SET bound=0", 3))
			srfFromConsume(t, retained, queries[0].Columns, queries[0].Types, srfFromStoredRows)
			if len(cached) != 3 || cached[0].Name() != "id" || cached[0].DatabaseTypeName() != "INT4" || cached[2].Name() != "n" || cached[2].DatabaseTypeName() != "INT8" {
				t.Fatal("retained schema changed", cached)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			check()
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("srf-from-restored")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			for _, s := range queries {
				rows, err := fresh.QueryContext(p.Ctx, s.Query)
				if err != nil {
					t.Fatal(err)
				}
				srfFromConsume(t, rows, s.Columns, s.Types, s.Rows)
			}
			const bad = "CREATE TABLE srf_control_partial(v integer); INSERT INTO srf_control_partial SELECT * FROM generate_series(1,2,0);"
			err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(bad))
			want := fixture.ControlsReferenceDiagnostic("0A000", "restore INSERT query sources are unsupported")
			fixture.AssertDiagnostic(t, err, &want)
			check()
			run(fixture.ErrorDiagnostic("SELECT * FROM srf_control_partial", seriesDiagnostic("SELECT * FROM srf_control_partial", "srf_control_partial", "42P01", `relation "srf_control_partial" does not exist`, "")))
		})
	}
	if p.Postgres != nil {
		for _, q := range []string{"DROP VIEW srf_control_view", "DROP VIEW srf_control_mixed", "DROP TABLE srf_control_data", "DROP TABLE srf_control_renamed"} {
			if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if len(emitted) == 0 {
			t.Fatal("no actual native SQL dump for PG replay")
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, string(emitted)); err != nil {
			t.Fatal("actual native dump replay", err)
		}
		for _, s := range queries {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s)
		}
	}
}

func TestReferenceSRFFromControls(t *testing.T) { srfFromControls(t, fixture.NewScenario(t)) }

func TestReferenceSRFFromFeatureSmoke(t *testing.T) {
	seriesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select("SELECT * FROM generate_series(5::bigint,1::bigint,-2::bigint) WITH ORDINALITY g(v,n) ORDER BY n", []string{"v", "n"}, []string{"INT8", "INT8"}, [][]any{{int64(5), int64(1)}, {int64(3), int64(2)}, {int64(1), int64(3)}}),
			fixture.Select("SELECT * FROM ROWS FROM(generate_series(1,2),generate_series(10::bigint,10::bigint)) WITH ORDINALITY g(a,b,n) ORDER BY n", []string{"a", "b", "n"}, []string{"INT4", "INT8", "INT8"}, [][]any{{int64(1), int64(10), int64(1)}, {int64(2), nil, int64(2)}}),
			fixture.Select("SELECT id,v,n FROM (VALUES(1,2),(2,0),(3,2)) s(id,hi) LEFT JOIN LATERAL generate_series(1,s.hi) WITH ORDINALITY g(v,n) ON true ORDER BY id,n", []string{"id", "v", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), nil, nil}, {int64(3), int64(1), int64(1)}, {int64(3), int64(2), int64(2)}}),
			fixture.Select("SELECT * FROM generate_series(NULL::bigint,3::bigint) WITH ORDINALITY g(v,n)", []string{"v", "n"}, []string{"INT8", "INT8"}, nil),
		} {
			seriesBoth(t, p, b, c, s)
		}
		const q = "SELECT * FROM generate_series(1,3,$1) WITH ORDINALITY g(v,n) ORDER BY n"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		s := fixture.ErrorDiagnostic(q, seriesDiagnostic(q, "", "22023", "step size cannot equal zero", ""), int64(0))
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}}, int64(1)))
	})
	srfFromControls(t, fixture.NewScenario(t))
}
