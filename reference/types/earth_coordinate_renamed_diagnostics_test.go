package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Full portable diagnostics and execute stages were observed on pinned
// PostgreSQL 18.6 in root tests15, including preexisting views and defaults.
func earthRenamedDiagnosticError(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, phase, q, route, code, message string, args, valid []any, point bool) {
	t.Helper()
	name := "earth_rename_arc_now"
	if point {
		name = "earth_rename_point_now"
	}
	want := earthCoordinateBodyError(name, code, message)
	assertError := func(stage string, err error) {
		if stage != "execute" {
			t.Fatalf("%s/%s failed at %s, want execute: %v", phase, route, stage, err)
		}
		if err == nil {
			t.Fatalf("%s/%s unexpectedly succeeded", phase, route)
		}
		fixture.AssertDiagnostic(t, err, &want)
	}
	var executor fixture.Executor = c
	var stmt *sql.Stmt
	if route != "direct" {
		var err error
		stmt, err = c.PrepareContext(p.Ctx, q)
		if err != nil {
			assertError("prepare", err)
			return
		}
		defer stmt.Close()
		executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	}
	repeats := 1
	if route == "held" {
		repeats = 2
	}
	for iteration := range repeats {
		rows, err := executor.QueryContext(p.Ctx, q, args...)
		if rows != nil {
			columns, e := rows.Columns()
			if e != nil {
				t.Error(e)
			}
			for rows.Next() {
				values, dest := make([]any, len(columns)), make([]any, len(columns))
				for i := range dest {
					dest[i] = &values[i]
				}
				if e := rows.Scan(dest...); e != nil {
					t.Error(e)
				}
				t.Errorf("%s/%s iteration=%d returned unexpected prefix row=%#v", phase, route, iteration, values)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		assertError("execute", err)
	}
	if route == "held" && valid != nil {
		var s fixture.Step
		if point {
			s = earthSelect(b, q, []string{"p"}, [][]any{{"(6378168, 0, 0)"}}, valid...)
		} else {
			s = fixture.Select(q, []string{"arc"}, []string{"FLOAT8"}, [][]any{{float64(0)}}, valid...)
		}
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
	}
}

func earthRenamedDiagnosticVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, phase string) {
	t.Helper()
	// The two views and defaults were captured under the original names, before
	// ALTER. They must retain hard operation identity but current presentation.
	for _, tc := range []struct {
		name, query, code, message string
		args, valid                []any
		point                      bool
	}{
		{"arc-literal", "SELECT earth_rename_arc_now('5e-324'::float8) AS arc", "22003", "value out of range: underflow", nil, nil, false},
		{"arc-parameter", "SELECT earth_rename_arc_now($1::float8) AS arc", "22003", "value out of range: underflow", []any{"5e-324"}, []any{"0"}, false},
		{"point-literal", "SELECT earth_rename_point_now('Infinity'::float8,0) AS p", "22003", "input is out of range", nil, nil, true},
		{"point-parameter", "SELECT earth_rename_point_now($1::float8,0) AS p", "22003", "input is out of range", []any{"Infinity"}, []any{"0"}, true},
		{"arc-hard-view", "SELECT arc FROM earth_rename_arc_view ORDER BY id", "22003", "value out of range: underflow", nil, nil, false},
		{"point-hard-view", "SELECT p FROM earth_rename_point_view ORDER BY id", "22003", "input is out of range", nil, nil, true},
		{"arc-hard-default", "INSERT INTO earth_rename_arc_default(id) VALUES (2) RETURNING id", "22003", "value out of range: underflow", nil, nil, false},
		{"point-hard-default", "INSERT INTO earth_rename_point_default(id) VALUES (2) RETURNING id", "22003", "input is out of range", nil, nil, true},
	} {
		for _, route := range []string{"direct", "fresh", "held"} {
			earthRenamedDiagnosticError(t, p, b, c, phase+"/"+tc.name, tc.query, route, tc.code, tc.message, tc.args, tc.valid, tc.point)
		}
	}
	for _, table := range []string{"earth_rename_arc_default", "earth_rename_point_default"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	}
	earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT earth_rename_arc_now(0) AS arc", []string{"arc"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
	earthCoordinateBoth(t, p, b, c, earthSelect(b, "SELECT earth_rename_point_now(0,0) AS p", []string{"p"}, [][]any{{"(6378168, 0, 0)"}}))
}

func earthRenamedDiagnosticControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal("renamed diagnostic Dump", err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("renamed diagnostic same Restore", err)
	}
	earthRenamedDiagnosticVerify(t, p, b, c, "same-restore")
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE earth_rename_allocator(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("renamed diagnostic fresh Restore", err)
	}
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	earthRenamedDiagnosticVerify(t, target, tb, target.Memory, "fresh-restore")
	if p.Postgres != nil {
		reset := "DROP EXTENSION IF EXISTS earthdistance CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public"
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("renamed diagnostic emitted SQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		earthRenamedDiagnosticVerify(t, p, pg, p.Postgres, "emitted-sql-replay")
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceEarthCoordinateRenamedDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			earthInstall(t, p, b, c)
			for _, q := range []string{
				"CREATE TABLE earth_rename_source(id integer,d float8,lat float8,lon float8)",
				"CREATE VIEW earth_rename_arc_view AS SELECT id,sec_to_gc(d) AS arc FROM earth_rename_source",
				"CREATE VIEW earth_rename_point_view AS SELECT id,ll_to_earth(lat,lon) AS p FROM earth_rename_source",
				"CREATE TABLE earth_rename_arc_default(id integer,arc float8 DEFAULT sec_to_gc('5e-324'::float8))",
				"CREATE TABLE earth_rename_point_default(id integer,p earth DEFAULT ll_to_earth('Infinity'::float8,0))",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_rename_source VALUES (1,'5e-324'::float8,'Infinity'::float8,0)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_rename_arc_default VALUES (1,0)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO earth_rename_point_default VALUES (1,ll_to_earth(0,0))", 1))
			for _, q := range []string{
				"ALTER FUNCTION sec_to_gc(float8) RENAME TO earth_rename_arc_now",
				"ALTER FUNCTION ll_to_earth(float8,float8) RENAME TO earth_rename_point_now",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			earthRenamedDiagnosticVerify(t, p, b, c, b.Name+"/live-alter")
			if !b.PG {
				earthRenamedDiagnosticControls(t, p, b, c)
			}
		})
	}
}
