package reference_test

import (
	"context"
	"database/sql"
	"math"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var referenceMathTrigDirectNames = []string{"cos", "cosd", "cot", "cotd", "sin", "sind", "tan", "tand"}

func referenceMathTrigDirectCases(t *testing.T, p *fixture.Pair, backend fixture.Backend, cases []referenceScalarFloatCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				referenceScalarFloatQuery(t, p.Ctx, backend.DB, backend.Recorder, tc, prepared)
			}
		})
	}
}

func TestReferenceMathTrigDirectAngles(t *testing.T) {
	p := fixture.NewScenario(t)
	var cases []referenceScalarFloatCase
	add := func(name, input string, want float64, ulps uint64) {
		cases = append(cases, referenceScalarFloatCase{name + "_" + input, "SELECT " + name + "('" + input + "') AS result", "FLOAT8", want, ulps})
	}
	// PostgreSQL 18 manual examples; ordinary radian values permit its libm caveat.
	for _, tc := range []struct {
		name, input string
		want        float64
		ulps        uint64
	}{
		{"cos", "0", 1, 0}, {"cosd", "60", .5, 0},
		{"cot", "0.5", 1.830487721712452, 2}, {"cotd", "45", 1, 0},
		{"sin", "1", .8414709848078965, 2}, {"sind", "30", .5, 0},
		{"tan", "1", 1.5574077246549023, 2}, {"tand", "45", 1, 0},
		// Independently rounded real-number values, never provider output.
		{"sind", "15", .25881904510252074, 2}, {"cosd", "75", .25881904510252074, 2},
		{"tand", "15", .2679491924311227, 2}, {"cotd", "75", .2679491924311227, 2},
		{"sind", "-375", -.25881904510252074, 2}, {"cosd", "435", .25881904510252074, 2},
		{"sind", "360000000030", .5, 0}, {"cosd", "-360000000060", .5, 0},
		{"tand", "360000000045", 1, 0}, {"cotd", "-360000000045", -1, 0},
		{"sind", "29.999999999999996", .49999999999999994, 2},
		{"sind", "30.000000000000004", .5000000000000001, 2},
		{"cosd", "59.99999999999999", .5000000000000001, 2},
		{"cosd", "60.00000000000001", .4999999999999999, 2},
	} {
		add(tc.name, tc.input, tc.want, tc.ulps)
	}
	// Exact REL_18_0 float8.sql/float8.out degree matrix. No ULP tolerance.
	for _, tc := range []struct {
		name   string
		inputs []int
		wants  []float64
	}{
		{"sind", []int{0, 30, 90, 150, 180, 210, 270, 330, 360}, []float64{0, .5, 1, .5, 0, -.5, -1, -.5, 0}},
		{"cosd", []int{0, 60, 90, 120, 180, 240, 270, 300, 360}, []float64{1, .5, 0, -.5, -1, -.5, 0, .5, 1}},
		{"tand", []int{0, 45, 90, 135, 180, 225, 270, 315, 360}, []float64{0, 1, math.Inf(1), -1, 0, 1, math.Inf(-1), -1, 0}},
		{"cotd", []int{0, 45, 90, 135, 180, 225, 270, 315, 360}, []float64{math.Inf(1), 1, 0, -1, math.Inf(-1), 1, 0, -1, math.Inf(1)}},
	} {
		for i, input := range tc.inputs {
			add(tc.name, strconv.Itoa(input), tc.wants[i], 0)
		}
	}
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		referenceMathTrigDirectCases(t, p, backend, cases)
	})
}

// Boundary observations assert finite nonzero category, sign and independently
// bounded magnitude rather than imposing universal near-zero libm ULP parity.
func referenceMathTrigDirectBoundary(t *testing.T, p *fixture.Pair, backend fixture.Backend, query string, negative bool, small bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, e := backend.DB.PrepareContext(ctx, query)
			if e != nil {
				t.Fatal(e)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(ctx)
		} else {
			rows, err = backend.DB.QueryContext(ctx, query)
		}
		if err != nil {
			t.Fatal(query, err)
		}
		defer rows.Close()
		referenceMathTrigMetadata(t, rows, backend.PG, false)
		if !rows.Next() {
			t.Fatal(query, rows.Err())
		}
		var got float64
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		matches := func(value float64) bool {
			return !math.IsNaN(value) && !math.IsInf(value, 0) && value != 0 && math.Signbit(value) == negative && ((small && math.Abs(value) < 1e-14) || (!small && math.Abs(value) > 1e14))
		}
		if !matches(got) {
			t.Fatalf("%s prepared=%v boundary=%g bits=%016x", query, prepared, got, math.Float64bits(got))
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(query, rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		backend.Recorder.Mu.Lock()
		var captured *fixture.Operation
		for _, op := range backend.Recorder.Operations {
			if op.SQL == query && op.Err == nil && op.Columns != nil {
				captured = op
			}
		}
		valid := captured != nil && len(captured.Columns) == 1 && captured.Columns[0] == "result" && len(captured.Rows) == 1 && len(captured.Rows[0]) == 1 && captured.Closed && captured.CloseErr == nil
		if valid {
			value, ok := fixture.Normalize(captured.Rows[0][0]).(float64)
			valid = ok && matches(value)
		}
		backend.Recorder.Mu.Unlock()
		if !valid {
			t.Fatal(query, "invalid consumed boundary recording")
		}
	}
}

func TestReferenceMathTrigDirectIEEE(t *testing.T) {
	p := fixture.NewScenario(t)
	negativeZero := math.Copysign(0, -1)
	var cases []referenceScalarFloatCase
	add := func(name, input string, want float64) {
		cases = append(cases, referenceScalarFloatCase{name + "_" + input, "SELECT " + name + "('" + input + "') AS result", "FLOAT8", want, 0})
	}
	for _, name := range referenceMathTrigDirectNames {
		add(name, "NaN", math.NaN())
	}
	for _, input := range []string{"0", "-0", "5e-324", "-5e-324"} {
		negative := strings.HasPrefix(input, "-")
		zero, tiny, pole := float64(0), math.SmallestNonzeroFloat64, math.Inf(1)
		if negative {
			zero, tiny, pole = negativeZero, -tiny, math.Inf(-1)
		}
		add("cos", input, 1)
		add("cosd", input, 1)
		add("cot", input, pole)
		add("cotd", input, pole)
		add("sind", input, zero)
		add("tand", input, 0)
		if strings.Contains(input, "e") {
			zero = tiny
		}
		add("sin", input, zero)
		add("tan", input, zero)
	}
	// Release-operation-order witnesses, independently observed on pinned PG18.
	for _, tc := range []struct {
		name, input string
		want        float64
	}{
		{"sind", "-180", negativeZero}, {"sind", "-360", negativeZero},
		{"sind", "-30", -.5}, {"sind", "390", .5},
		{"cosd", "-120", -.5}, {"cosd", "-450", 0},
		{"tand", "-90", math.Inf(-1)}, {"tand", "-270", math.Inf(1)},
		{"tand", "-180", 0}, {"tand", "-360", 0}, {"tand", "-405", -1},
		{"cotd", "-180", math.Inf(1)}, {"cotd", "-360", math.Inf(-1)},
		{"cotd", "-90", 0}, {"cotd", "-270", 0}, {"cotd", "450", 0},
	} {
		add(tc.name, tc.input, tc.want)
	}
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		referenceMathTrigDirectCases(t, p, backend, cases)
		for _, tc := range []struct {
			name                                string
			angle                               float64
			belowNegative, aboveNegative, small bool
		}{
			{"cosd", 90, false, true, true}, {"tand", 90, false, true, false}, {"cotd", 90, false, true, true},
			{"sind", 180, false, true, true}, {"tand", 180, true, false, true}, {"cotd", 180, true, false, false},
			{"cosd", 270, true, false, true}, {"tand", 270, false, true, false}, {"cotd", 270, false, true, true},
		} {
			for _, below := range []bool{true, false} {
				direction, negative := math.Inf(1), tc.aboveNegative
				if below {
					direction, negative = math.Inf(-1), tc.belowNegative
				}
				input := strconv.FormatFloat(math.Nextafter(tc.angle, direction), 'g', -1, 64)
				referenceMathTrigDirectBoundary(t, p, backend, "SELECT "+tc.name+"('"+input+"') AS result", negative, tc.small)
			}
		}
		// Binary approximations of radian pi and pi/2 must not snap to ideal poles.
		for _, tc := range []struct {
			name, input string
			small       bool
		}{
			{"sin", "3.141592653589793", true}, {"cos", "1.5707963267948966", true},
			{"tan", "1.5707963267948966", false}, {"cot", "1.5707963267948966", true},
		} {
			referenceMathTrigDirectBoundary(t, p, backend, "SELECT "+tc.name+"('"+tc.input+"') AS result", false, tc.small)
		}
	})
}

func TestReferenceMathTrigDirectSignaturesMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	referenceMathTrigBackends(t, p, func(t *testing.T, backend fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Exec("CREATE TABLE direct_types(id INT,s SMALLINT,i INTEGER,b BIGINT,n NUMERIC(4,2),r REAL,d DOUBLE PRECISION,cos INT,cosd INT,cot INT,cotd INT,sin INT,sind INT,tan INT,tand INT)", 0))
		run(fixture.Exec("INSERT INTO direct_types VALUES(1,0,0,0,0,0,0,7,7,7,7,7,7,7,7),(2,NULL,NULL,NULL,NULL,NULL,NULL,8,8,8,8,8,8,8,8)", 2))
		for _, name := range referenceMathTrigDirectNames {
			want := float64(0)
			if name == "cos" || name == "cosd" {
				want = 1
			}
			if name == "cot" || name == "cotd" {
				want = math.Inf(1)
			}
			for _, column := range []string{"s", "i", "b", "n", "r", "d"} {
				for _, id := range []string{"1", "2", "3"} {
					query := "SELECT " + name + "(" + column + ") FROM direct_types WHERE id=" + id
					if id == "1" {
						referenceMathTrigDirectCases(t, p, backend, []referenceScalarFloatCase{{name + column, strings.Replace(query, ") FROM", ") AS result FROM", 1), "FLOAT8", want, 0}})
						continue
					}
					var rows [][]any
					if id == "2" {
						rows = [][]any{{nil}}
					}
					step := fixture.Select(query, []string{name}, []string{"FLOAT8"}, rows)
					step.CheckMetadata = func(t *testing.T, r *sql.Rows) { referenceMathTrigMetadata(t, r, backend.PG, false) }
					referenceMathTrigStep(t, p, backend, step)
				}
			}
			for _, spelling := range []string{name, "pg_catalog." + name, strings.ToUpper(name), `"` + name + `"`} {
				referenceMathTrigDirectCases(t, p, backend, []referenceScalarFloatCase{{spelling, "SELECT " + spelling + "(0) AS result", "FLOAT8", want, 0}})
			}
			referenceMathTrigStep(t, p, backend, fixture.Select("SELECT "+name+" FROM direct_types ORDER BY id", []string{name}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
			for _, arg := range []string{"NULL", "NULL::double precision"} {
				step := fixture.Select("SELECT "+name+"("+arg+")", []string{name}, []string{"FLOAT8"}, [][]any{{nil}})
				step.CheckMetadata = func(t *testing.T, r *sql.Rows) { referenceMathTrigMetadata(t, r, backend.PG, false) }
				referenceMathTrigStep(t, p, backend, step)
			}
		}
	})
}
