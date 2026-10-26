package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func segScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	queryIntScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION seg", 0))
		body(t, p, b, c)
	})
}

func segSelect(b fixture.Backend, q string, names []string, values [][]any, args ...any) fixture.Step {
	types := make([]string, len(names))
	for i, n := range names {
		if n == "id" {
			types[i] = "INT4"
		} else if !b.PG {
			types[i] = "SEG"
		}
	}
	s := fixture.Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if names[column] == "id" {
			return false
		}
		if v == nil {
			return true
		}
		if b.PG {
			if _, ok := v.([]byte); !ok {
				t.Fatalf("PG seg raw %T", v)
			}
		} else if _, ok := v.(string); !ok {
			t.Fatalf("native seg raw %T", v)
		}
		return true
	}
	return s
}

// Expectations follow REL_18_6 contrib/seg regression output and the released
// formatter, independently of either backend. Six, not seven, digits are stored.
var segCases = []struct{ in, out string }{
	{"1", "1"}, {"-1.0", "-1.0"}, {"1.00", "1.00"}, {"1.00000", "1.00000"},
	{"1.000000", "1.00000"}, {"12.34567890123456", "12.3457"},
	{"1e7", "1e+07"}, {"1.0e7", "1.0e+07"}, {"1e-7", "1e-07"}, {"1.0e-7", "1.0e-07"},
	{"2e-6", "2e-06"}, {"2e-5", "2e-05"}, {"2e-4", "0.0002"}, {"2e-3", "0.002"}, {"2e-2", "0.02"}, {"2e-1", "0.2"},
	{"2e-0", "2"}, {"2e+0", "2"}, {"2e+1", "2e1"}, {"2e+2", "2e2"}, {"2e+3", "2e3"}, {"2e+4", "2e4"}, {"2e+5", "2e+05"}, {"2e+6", "2e+06"},
	{"0.000000120", "1.20e-07"}, {"3.400e5", "3.400e+05"},
	{"~ 6.5", "~6.5"}, {"< 6.5", "<6.5"}, {"> 6.5", ">6.5"},
	{"~1.5 .. 2.5", "~1.5 .. 2.5"}, {"1.5 .. ~2.5", "1.5 .. ~2.5"}, {"~1.5 .. ~2.5", "~1.5 .. ~2.5"},
	{"<1.5 .. >2.5", "<1.5 .. >2.5"}, {">1.5 .. <2.5", ">1.5 .. <2.5"}, {"1.5 .. >2.5", "1.5 .. >2.5"}, {"<1.5 .. 2.5", "<1.5 .. 2.5"},
	{"0..", "0 .."}, {"0...", "0 .."}, {"0 ..", "0 .."}, {"0 ...", "0 .."},
	{"..0", ".. 0"}, {"...0", ".. 0"}, {".. 0", ".. 0"}, {"... 0", ".. 0"},
	{"0 .. 1", "0 .. 1"}, {"-1 .. 0", "-1 .. 0"}, {"-1 .. 1", "-1 .. 1"}, {"6.25 .. 6.50", "6.25 .. 6.50"},
	{"1.5e-2 .. 2E-2", "0.015 .. 0.02"}, {"0(+-)1", "-1 .. 1"}, {"0(+-)1.0", "-1.0 .. 1.0"}, {"1.0(+-)0.005", "0.995 .. 1.005"},
	{"101(+-)1", "1.00e2 .. 1.02e2"}, {"100(+-)1", "99.0 .. 1.01e2"}, {"5(+-)0.3", "4.7 .. 5.3"}, {"10(+-)1", "9.0 .. 1.1e1"},
	{"-0.00", "-0.00"}, {"0.000", "0.000"}, {"3.4028235e38", "3.40282e+38"}, {"1e-45", "1e-45"},
	{"1.0 .. 1.00", "1.0"}, {"0(+-)-1", "1 .. -1"}, {"~1(+-)1", "0 .. 2"},
}

func TestReferenceSegSyntaxPrecision(t *testing.T) {
	for i, tc := range segCases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE seg_syntax(id integer NOT NULL,v seg)", 0))
				run(fixture.Exec("INSERT INTO seg_syntax VALUES (1,"+queryIntLiteral(tc.in)+")", 1))
				run(fixture.Prepared(fixture.Exec("INSERT INTO seg_syntax VALUES ($1,$2)", 1, 2, tc.in)))
				s := segSelect(b, "SELECT id,v FROM seg_syntax ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), tc.out}, {int64(2), tc.out}})
				run(s)
				run(fixture.Prepared(s))
			})
		})
	}
}

func TestReferenceSegValues(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE seg_values(id integer NOT NULL,v public.seg,n seg NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO seg_values VALUES (1,'1.00','~1.5 .. 2.5'),(2,NULL,'0...')", 2))
		stmt, err := c.PrepareContext(p.Ctx, "INSERT INTO seg_values VALUES ($1,$2,$3)")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for i, v := range []any{"1.0", nil, []byte("<2.50"), "3"} {
			r, err := stmt.ExecContext(p.Ctx, i+3, v, "1")
			if err != nil {
				t.Fatal(err)
			}
			if n, err := r.RowsAffected(); err != nil || n != 1 {
				t.Fatalf("affected %d %v", n, err)
			}
		}
		run(fixture.Exec("INSERT INTO seg_values VALUES (7,$1,$2),(8,$3,$2)", 2, "4.00", "2", nil))
		s := segSelect(b, "SELECT id,v FROM seg_values ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1.00"}, {int64(2), nil}, {int64(3), "1.0"}, {int64(4), nil}, {int64(5), "<2.50"}, {int64(6), "3"}, {int64(7), "4.00"}, {int64(8), nil}})
		run(s)
		run(fixture.Prepared(s))
		for _, cast := range []fixture.Step{
			segSelect(b, "SELECT CAST('1.00'::text AS seg) AS v", []string{"v"}, [][]any{{"1.00"}}),
			segSelect(b, "SELECT '1.00'::public.seg AS v", []string{"v"}, [][]any{{"1.00"}}),
			fixture.Select("SELECT CAST('1.00'::seg AS text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"1.00"}}),
		} {
			run(cast)
			run(fixture.Prepared(cast))
		}
	})
}

func TestReferenceSegNotNull(t *testing.T) {
	segScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE seg_required(id integer NOT NULL,v seg NOT NULL)", 0))
		run(fixture.Exec("INSERT INTO seg_required VALUES (1,'1.00')", 1))
		want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "seg_required" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (2, null).", "public", "seg_required", "v"
		for _, prepared := range []bool{false, true} {
			for _, arg := range []any{nil, []byte(nil)} {
				queryIntError(t, p, b, c, "INSERT INTO seg_required VALUES (2,$1)", prepared, false, false, want, arg)
			}
			queryIntError(t, p, b, c, "INSERT INTO seg_required VALUES (2,NULL)", prepared, false, false, want)
			run(segSelect(b, "SELECT id,v FROM seg_required ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "1.00"}}))
		}
	})
}
