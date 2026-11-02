package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func earthQueryRenamedDiagnosticVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, box, helper string) {
	t.Helper()
	boxName := box[strings.LastIndexByte(box, '.')+1:]
	helperName := helper[strings.LastIndexByte(helper, '.')+1:]
	for _, tc := range []struct {
		q, wrapper  string
		args, valid []any
	}{
		{"SELECT " + box + "('(6378168)'::earth,'5e-324'::float8) AS b", "during inlining", nil, nil},
		{"SELECT " + box + "($1::earth,$2::float8) AS b", "statement 1", []any{"(6378168)", "5e-324"}, []any{"(6378168)", "0"}},
		{"SELECT " + box + "('(6378168)'::earth,$1::float8) AS b", "during inlining", []any{"5e-324"}, []any{"0"}},
		{"SELECT " + box + "(NULL::earth,'5e-324'::float8) AS b", "during inlining", nil, nil},
		{"SELECT b FROM earth_query_error_view ORDER BY id", "", nil, nil},
		{"INSERT INTO earth_query_error_default(id) VALUES (2) RETURNING id", "during inlining", nil, nil},
	} {
		for _, route := range []string{"direct", "fresh", "held"} {
			earthCoordinateError(t, p, b, c, tc.q, route, tc.args, earthQueryUnderflow(helperName, boxName, tc.wrapper), false, tc.valid)
		}
	}
	for _, route := range []string{"direct", "fresh", "held"} {
		earthCoordinateError(t, p, b, c, "SELECT "+helper+"($1::float8)", route, []any{"5e-324"}, earthCoordinateBodyError(helperName, "22003", "value out of range: underflow"), false, []any{"0"})
		q := "SELECT " + box + "($1::earth,$2::float8) AS b"
		want := cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "(0)", 1)
		earthCoordinateError(t, p, b, c, q, route, []any{"(0)", "0"}, want, false, []any{"(6378168)", "0"})
	}
	earthCoordinateBoth(t, p, b, c, fixture.Select("SELECT id FROM earth_query_error_default ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	earthCoordinateBoth(t, p, b, c, cubeConstructorSelect(b, "SELECT "+box+"('(6378168)'::earth,0) AS b", []string{"b"}, []string{"CUBE"}, [][]any{{"(6378168)"}}))
}

func earthQueryRenamedDiagnosticControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	box, helper := "earth_query_error_moved.earth_query_box_now", "earth_query_error_moved.earth_query_chord_now"
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("renamed query same Restore", err)
	}
	earthQueryRenamedDiagnosticVerify(t, p, b, c, box, helper)
	target := fixture.NewFixture(t, false, true)
	fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE earth_query_error_allocator(v integer)", 0))
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal("renamed query fresh Restore", err)
	}
	tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
	earthQueryRenamedDiagnosticVerify(t, target, tb, target.Memory, box, helper)
	if p.Postgres != nil {
		reset := "DROP EXTENSION IF EXISTS earthdistance CASCADE; DROP EXTENSION IF EXISTS cube CASCADE; DROP SCHEMA public CASCADE; CREATE SCHEMA public; DROP SCHEMA IF EXISTS earth_query_error_moved CASCADE"
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("renamed query actual emitted SQL replay", err)
		}
		pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
		earthQueryRenamedDiagnosticVerify(t, p, pg, p.Postgres, box, helper)
		if _, err := p.Postgres.ExecContext(p.Ctx, reset); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceEarthQueryRenamedDiagnostics(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "earth_query_error_moved")
	earthCoordinateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		for _, q := range []string{
			"CREATE SCHEMA earth_query_error_moved",
			"CREATE TABLE earth_query_error_input(id integer,p earth,r float8)",
			"INSERT INTO earth_query_error_input VALUES (1,'(6378168)','5e-324'::float8)",
			"CREATE VIEW earth_query_error_view AS SELECT id,earth_box(p,r) AS b FROM earth_query_error_input",
			"CREATE TABLE earth_query_error_default(id integer,b cube DEFAULT earth_box('(6378168)'::earth,'5e-324'::float8))",
			"INSERT INTO earth_query_error_default VALUES (1,'(6378168)')",
		} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil {
				t.Fatal(q, err)
			}
		}
		oldSQL := "SELECT earth_box($1::earth,$2::float8) AS b"
		h := earthHeld(t, p, c, oldSQL)
		defer h.Statement.Close()
		for phase, names := range []struct{ box, helper string }{
			{"earth_box", "gc_to_sec"},
			{"earth_query_box_now", "gc_to_sec"},
			{"earth_query_box_now", "earth_query_chord_now"},
			{"earth_query_error_moved.earth_query_box_now", "earth_query_error_moved.earth_query_chord_now"},
		} {
			var changes []string
			switch phase {
			case 1:
				changes = []string{"ALTER FUNCTION earth_box(earth,float8) RENAME TO earth_query_box_now"}
			case 2:
				changes = []string{"ALTER FUNCTION gc_to_sec(float8) RENAME TO earth_query_chord_now"}
			case 3:
				changes = []string{"ALTER FUNCTION earth_query_box_now(earth,float8) SET SCHEMA earth_query_error_moved", "ALTER FUNCTION earth_query_chord_now(float8) SET SCHEMA earth_query_error_moved"}
			}
			for _, q := range changes {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			earthQueryRenamedDiagnosticVerify(t, p, b, c, names.box, names.helper)
			if phase != 0 {
				rows, err := h.Statement.QueryContext(p.Ctx, "(6378168)", "5e-324")
				if rows != nil {
					rows.Close()
				}
				want := earthCoordinateMissing("earth_box(earth, double precision)", "8")
				if err == nil {
					t.Fatal("old held query text survived rename")
				}
				fixture.AssertDiagnostic(t, err, &want)
			}
		}
		h.Statement.Close()
		if !b.PG {
			earthQueryRenamedDiagnosticControls(t, p, b, c)
		}
	})
}
