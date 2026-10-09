package reference_test

import (
	"context"
	"database/sql"
	"math"
	"testing"
)

// Feature-local IEEE assertions retain NaN and signed zero rather than changing
// shared fixture equality. Both backends and both execution modes independently
// satisfy these expected values, types and consumed recorder buffers.
type referenceScalarFloatCase struct {
	name, query, typ string
	want             float64
	ulps             uint64
}

func referenceScalarFloatMatches(got, want float64, ulps uint64) bool {
	if math.IsNaN(want) {
		return math.IsNaN(got)
	}
	if math.Signbit(got) != math.Signbit(want) {
		return false
	}
	if got == want {
		return true
	}
	if ulps == 0 || math.IsNaN(got) || math.IsInf(got, 0) || math.IsInf(want, 0) {
		return false
	}
	actual, expected := math.Float64bits(got), math.Float64bits(want)
	if actual > expected {
		return actual-expected <= ulps
	}
	return expected-actual <= ulps
}

func referenceScalarFloatQuery(t *testing.T, ctx context.Context, db *sql.DB, recorder *referenceRecorder, tc referenceScalarFloatCase, prepared bool) {
	t.Helper()
	ctx = context.WithValue(ctx, referenceRecorderKey{}, recorder)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := db.PrepareContext(ctx, tc.query)
		if e != nil {
			t.Fatal(tc.query, "Prepare", e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx)
	} else {
		rows, err = db.QueryContext(ctx, tc.query)
	}
	if err != nil {
		t.Fatal(tc.query, "Execute", err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != "result" {
		t.Fatal(tc.query, columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != tc.typ {
		t.Fatal(tc.query, types, err)
	}
	if !rows.Next() {
		t.Fatal(tc.query, "missing row", rows.Err())
	}
	var got float64
	if err := rows.Scan(&got); err != nil {
		t.Fatal(tc.query, err)
	}
	if !referenceScalarFloatMatches(got, tc.want, tc.ulps) {
		t.Fatalf("%s prepared=%v actual=%g bits=%016x want=%g bits=%016x tolerance=%d ULP", tc.query, prepared, got, math.Float64bits(got), tc.want, math.Float64bits(tc.want), tc.ulps)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(tc.query, "extra row or row error", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	// Retain the shared fixture's consumed-buffer and cursor ownership proof,
	// using the same exact IEEE/tolerance predicate for the recorded payload.
	recorder.mu.Lock()
	var captured *referenceOperation
	for _, operation := range recorder.operations {
		if operation.SQL == tc.query && operation.Err == nil && operation.Columns != nil {
			captured = operation
		}
	}
	valid := captured != nil && len(captured.Columns) == 1 && captured.Columns[0] == "result" && len(captured.Rows) == 1 && len(captured.Rows[0]) == 1 && captured.Closed && captured.CloseErr == nil
	var recorded float64
	if valid {
		var ok bool
		recorded, ok = referenceNormalize(captured.Rows[0][0]).(float64)
		valid = ok && referenceScalarFloatMatches(recorded, tc.want, tc.ulps)
	}
	recorder.mu.Unlock()
	if !valid {
		t.Fatalf("%s prepared=%v: invalid consumed recording (float=%g)", tc.query, prepared, recorded)
	}
}

func TestReferenceMathScalarFloatingOverloads(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE abs_float(id INT,r REAL,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO abs_float VALUES(1,'-17.5','-17.5'),(2,'-0','-0'),(3,'0','0'),(4,'NaN','NaN'),(5,'Infinity','Infinity'),(6,'-Infinity','-Infinity'),(7,'17.5','17.5')", 7))
	p.step(t, referenceExec("CREATE TABLE round_float(id INT,d DOUBLE PRECISION)", 0))
	p.step(t, referenceExec("INSERT INTO round_float VALUES(1,'42.8'),(2,'-42.8'),(3,'42'),(4,'-42'),(5,'-0.5'),(6,'0.5'),(7,'-0'),(8,'0'),(9,'NaN'),(10,'Infinity'),(11,'-Infinity')", 11))
	var cases []referenceScalarFloatCase
	for _, column := range []struct{ name, typ string }{{"r", "FLOAT4"}, {"d", "FLOAT8"}} {
		for _, tc := range []struct {
			id, name string
			want     float64
		}{
			{"1", "negative", 17.5}, {"2", "negative_zero", 0}, {"3", "positive_zero", 0}, {"4", "NaN", math.NaN()},
			{"5", "positive_infinity", math.Inf(1)}, {"6", "negative_infinity", math.Inf(1)}, {"7", "positive", 17.5},
		} {
			cases = append(cases, referenceScalarFloatCase{"abs_" + column.typ + "_" + tc.name, "SELECT abs(" + column.name + ") AS result FROM abs_float WHERE id=" + tc.id, column.typ, tc.want, 0})
		}
	}
	negativeZero := math.Copysign(0, -1)
	for _, name := range []string{"ceil", "ceiling", "floor"} {
		for _, tc := range []struct {
			id, label      string
			ceiling, floor float64
		}{
			{"1", "positive_fraction", 43, 42}, {"2", "negative_fraction", -42, -43},
			{"3", "positive_integer", 42, 42}, {"4", "negative_integer", -42, -42},
			{"5", "negative_half", negativeZero, -1}, {"6", "positive_half", 1, 0},
			{"7", "negative_zero", negativeZero, negativeZero}, {"8", "positive_zero", 0, 0},
			{"9", "NaN", math.NaN(), math.NaN()}, {"10", "positive_infinity", math.Inf(1), math.Inf(1)}, {"11", "negative_infinity", math.Inf(-1), math.Inf(-1)},
		} {
			want := tc.ceiling
			if name == "floor" {
				want = tc.floor
			}
			cases = append(cases, referenceScalarFloatCase{name + "_" + tc.label, "SELECT " + name + "(d) AS result FROM round_float WHERE id=" + tc.id, "FLOAT8", want, 0})
		}
	}
	for _, tc := range []struct {
		input, name string
		want        float64
		ulps        uint64
	}{
		{"0", "zero", 1, 0}, {"-0", "negative_zero", 1, 0},
		// Independently rounded mathematical constants; at most two ULPs
		// accommodates the PostgreSQL manual's ordinary host-libm caveat.
		{"1", "positive", 2.718281828459045, 2}, {"-1", "negative", 0.36787944117144233, 2},
		{"709", "large_finite", 8.218407461554972e307, 2},
		{"NaN", "NaN", math.NaN(), 0}, {"Infinity", "positive_infinity", math.Inf(1), 0}, {"-Infinity", "negative_infinity", 0, 0},
	} {
		cases = append(cases, referenceScalarFloatCase{"exp_" + tc.name, "SELECT exp('" + tc.input + "') AS result", "FLOAT8", tc.want, tc.ulps})
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						referenceScalarFloatQuery(t, p.ctx, backend.db, backend.recorder, tc, prepared)
					}
				})
			}
		})
	}
	for _, tc := range []struct{ input, message string }{{"710", "value out of range: overflow"}, {"-746", "value out of range: underflow"}} {
		s := referenceImplicitError("SELECT exp('"+tc.input+"')", referenceDiagnostic{Code: "22003", Message: tc.message})
		s.selectRows, s.errorStage = true, "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}
