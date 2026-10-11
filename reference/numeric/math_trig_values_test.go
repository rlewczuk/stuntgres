package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func referenceMathTrigBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, backend) })
	}
}

func referenceMathTrigMetadata(t *testing.T, rows *sql.Rows, pg, stored bool) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "FLOAT8" || typ.ScanType() != reflect.TypeOf(float64(0)) {
			t.Fatalf("FLOAT8 descriptor: %s %v", typ.DatabaseTypeName(), typ.ScanType())
		}
		if n, known := typ.Length(); known || n != 0 {
			t.Fatalf("FLOAT8 length: %d %v", n, known)
		}
		if precision, scale, known := typ.DecimalSize(); known || precision != 0 || scale != 0 {
			t.Fatalf("FLOAT8 decimal size: %d %d %v", precision, scale, known)
		}
		wantNullable := stored && !pg
		if nullable, known := typ.Nullable(); nullable != wantNullable || known != wantNullable {
			t.Fatalf("FLOAT8 nullability: %v %v want %v %v", nullable, known, wantNullable, wantNullable)
		}
	}
}

func referenceMathTrigStep(t *testing.T, p *fixture.Pair, backend fixture.Backend, step fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := step
		if prepared {
			s = fixture.Prepared(s)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
	}
}

func TestReferenceMathTrigAngles(t *testing.T) {
	p := fixture.NewScenario(t)
	var cases []referenceScalarFloatCase
	add := func(name, input string, want float64, ulps uint64) {
		cases = append(cases, referenceScalarFloatCase{name + "_" + input, "SELECT " + name + "(" + input + ") AS result", "FLOAT8", want, ulps})
	}
	// Exact degree results are REL_18_0 float8 regression witnesses.
	for _, tc := range []struct {
		input      string
		asin, acos float64
	}{
		{"'-1'", -90, 180}, {"'-0.5'", -30, 120}, {"'0'", 0, 90}, {"'0.5'", 30, 60}, {"'1'", 90, 0},
	} {
		add("asind", tc.input, tc.asin, 0)
		add("acosd", tc.input, tc.acos, 0)
	}
	for _, tc := range []struct {
		input string
		want  float64
	}{
		{"'-Infinity'", -90}, {"'-1'", -45}, {"'0'", 0}, {"'1'", 45}, {"'Infinity'", 90},
	} {
		add("atand", tc.input, tc.want, 0)
	}
	// Independently rounded mathematical constants, not Go/provider outputs.
	// Ordinary host libm results have the manual's last-bit platform caveat.
	for _, tc := range []struct {
		name, input string
		want        float64
	}{
		{"acos", "'-1'", 3.141592653589793}, {"acos", "'0'", 1.5707963267948966}, {"acos", "'0.5'", 1.0471975511965979},
		{"asin", "'-1'", -1.5707963267948966}, {"asin", "'0.5'", 0.5235987755982989}, {"asin", "'1'", 1.5707963267948966},
		{"atan", "'-Infinity'", -1.5707963267948966}, {"atan", "'1'", 0.7853981633974483}, {"atan", "'Infinity'", 1.5707963267948966},
		{"asin", "'0.25'", 0.25268025514207865}, {"acos", "'0.25'", 1.318116071652818}, {"atan", "'0.25'", 0.24497866312686414},
		{"asind", "'0.25'", 14.477512185929925}, {"acosd", "'0.25'", 75.52248781407008}, {"atand", "'0.25'", 14.036243467926479},
		{"asin", "'0.75'", 0.848062078981481}, {"acos", "'0.75'", 0.7227342478134157},
		{"asind", "'0.75'", 48.590377890729144}, {"acosd", "'0.75'", 41.40962210927086},
		{"asind", "'-0.75'", -48.590377890729144}, {"acosd", "'-0.75'", 138.59037789072914},
		{"atan2", "'0.25','0.75'", 0.3217505543966422}, {"atan2d", "'0.25','0.75'", 18.43494882292201},
		{"asind", "'0.49999999999999994'", 29.999999999999996}, {"asind", "'0.5000000000000001'", 30.000000000000007},
		{"acosd", "'0.49999999999999994'", 60.00000000000001}, {"acosd", "'0.5000000000000001'", 59.99999999999999},
		{"asin", "'0.9999999999999999'", 1.5707963118937354}, {"asin", "'-0.9999999999999999'", -1.5707963118937354},
		{"acos", "'0.9999999999999999'", 1.4901161193847656e-8}, {"acos", "'-0.9999999999999999'", 3.141592638688632},
		{"asind", "'0.9999999999999999'", 89.99999914622636}, {"asind", "'-0.9999999999999999'", -89.99999914622636},
		{"acosd", "'0.9999999999999999'", 8.537736462515939e-7}, {"acosd", "'-0.9999999999999999'", 179.99999914622634},
	} {
		add(tc.name, tc.input, tc.want, 2)
	}
	add("acos", "'1'", 0, 0)
	for _, tc := range []struct {
		y, x           string
		degree, radian float64
	}{
		{"0", "10", 0, 0}, {"10", "0", 90, 1.5707963267948966}, {"0", "-10", 180, 3.141592653589793}, {"-10", "0", -90, -1.5707963267948966},
		{"1", "1", 45, 0.7853981633974483}, {"1", "-1", 135, 2.356194490192345}, {"-1", "-1", -135, -2.356194490192345}, {"-1", "1", -45, -0.7853981633974483},
	} {
		input := "'" + tc.y + "','" + tc.x + "'"
		add("atan2d", input, tc.degree, 0)
		add("atan2", input, tc.radian, 2)
	}
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
				}
			})
		}
	})
}

func TestReferenceMathTrigIEEE(t *testing.T) {
	p := fixture.NewScenario(t)
	negativeZero := math.Copysign(0, -1)
	var cases []referenceScalarFloatCase
	add := func(name, input string, want float64, ulps uint64) {
		cases = append(cases, referenceScalarFloatCase{name + "_" + input, "SELECT " + name + "(" + input + ") AS result", "FLOAT8", want, ulps})
	}
	for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand"} {
		add(name, "'NaN'", math.NaN(), 0)
		for _, input := range []string{"'0'", "'-0'"} {
			want, ulps := float64(0), uint64(0)
			if input == "'-0'" {
				want = negativeZero
			}
			if name == "acos" {
				want, ulps = 1.5707963267948966, 2
			}
			if name == "acosd" {
				want = 90
			}
			add(name, input, want, ulps)
		}
	}
	for _, tc := range []struct {
		y, x           string
		degree, radian float64
	}{
		{"0", "0", 0, 0}, {"-0", "0", negativeZero, negativeZero}, {"0", "-0", 180, 3.141592653589793}, {"-0", "-0", -180, -3.141592653589793},
		{"Infinity", "Infinity", 45, 0.7853981633974483}, {"Infinity", "-Infinity", 135, 2.356194490192345}, {"-Infinity", "-Infinity", -135, -2.356194490192345}, {"-Infinity", "Infinity", -45, -0.7853981633974483},
		{"Infinity", "1", 90, 1.5707963267948966}, {"-Infinity", "-1", -90, -1.5707963267948966},
		{"1", "Infinity", 0, 0}, {"-1", "Infinity", negativeZero, negativeZero}, {"1", "-Infinity", 180, 3.141592653589793}, {"-1", "-Infinity", -180, -3.141592653589793},
	} {
		input := "'" + tc.y + "','" + tc.x + "'"
		add("atan2d", input, tc.degree, 0)
		add("atan2", input, tc.radian, 2)
	}
	for _, name := range []string{"atan2", "atan2d"} {
		for _, input := range []string{"'NaN',1", "1,'NaN'", "'NaN','Infinity'", "'-Infinity','NaN'", "'NaN','NaN'"} {
			add(name, input, math.NaN(), 0)
		}
	}
	// Subnormal degree outputs follow the release operation order, not real-number
	// x*180/pi accuracy: independently predicted 60/45 minimum-FLOAT8 units.
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
				}
			})
		}
		for _, name := range []string{"asin", "atan", "asind", "atand"} {
			want := math.SmallestNonzeroFloat64
			if name == "asind" {
				want = 60 * math.SmallestNonzeroFloat64
			}
			if name == "atand" {
				want = 45 * math.SmallestNonzeroFloat64
			}
			for _, sign := range []float64{1, -1} {
				tc := referenceScalarFloatCase{name + "_subnormal", "SELECT " + name + "('" + map[float64]string{1: "5e-324", -1: "-5e-324"}[sign] + "') AS result", "FLOAT8", sign * want, 0}
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
				}
			}
		}
	})
}

func TestReferenceMathTrigSignaturesMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE trig_types(id INT,s SMALLINT,i INTEGER,b BIGINT,n NUMERIC,r REAL,d DOUBLE PRECISION,acos INT)", 0))
		run(fixture.Exec("INSERT INTO trig_types VALUES(1,1,1,1,1,1,1,7),(2,NULL,NULL,NULL,NULL,NULL,NULL,8)", 2))
		for _, name := range []string{"acos", "acosd", "asin", "asind", "atan", "atand", "atan2", "atan2d"} {
			want := float64(0)
			switch name {
			case "asin":
				want = 1.5707963267948966
			case "asind":
				want = 90
			case "atan", "atan2":
				want = 0.7853981633974483
			case "atand", "atan2d":
				want = 45
			}
			for _, column := range []string{"s", "i", "b", "n", "r", "d"} {
				args := column
				if name == "atan2" || name == "atan2d" {
					args += ",d"
				}
				for _, id := range []string{"1", "2", "3"} {
					rows := [][]any{{any(want)}}
					if id == "2" {
						rows = [][]any{{nil}}
					}
					if id == "3" {
						rows = nil
					}
					step := fixture.Select("SELECT "+name+"("+args+") FROM trig_types WHERE id="+id, []string{name}, []string{"FLOAT8"}, rows)
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { referenceMathTrigMetadata(t, rows, backend.PG, false) }
					// These exact endpoint/atan1 constants are shared by host libm.
					referenceMathTrigStep(t, p, backend, step)
					if name == "atan2" || name == "atan2d" {
						step.Query = "SELECT " + name + "(d," + column + ") FROM trig_types WHERE id=" + id
						referenceMathTrigStep(t, p, backend, step)
					}
				}
			}
		}
		for _, query := range []string{"SELECT pg_catalog.asind('0.5')", "SELECT ASIND('0.5')", `SELECT "asind"('0.5')`} {
			referenceMathTrigStep(t, p, backend, fixture.Select(query, []string{"asind"}, []string{"FLOAT8"}, [][]any{{float64(30)}}))
		}
		referenceMathTrigStep(t, p, backend, fixture.Select("SELECT acos FROM trig_types ORDER BY id", []string{"acos"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
	})
}
