package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

// Expected values and complete range diagnostics are grounded in release sources
// and root's pinned PostgreSQL18.0 D1 observations (tests-1.md).
type referenceMathConstantsProbe struct {
	name, function, input string
	category              string // value, overflow or underflow
	want                  float64
	ulps                  uint64
	// Optional nonzero pinned PostgreSQL target for a documented libm variance.
	// Native always retains the independent mathematical want and same ulps.
	pgWantBits uint64
}

func referenceMathConstantsProbeError(t *testing.T, tc referenceMathConstantsProbe, query, phase string, err error) {
	t.Helper()
	got := fixture.ExtractDiagnostic(t, err)
	if tc.category == "overflow" || tc.category == "underflow" {
		// Pinned D1 confirmed execution-only range failures with empty logical
		// context/location fields, including both prepared parameter routes.
		want := fixture.Diagnostic{
			Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003",
			Message: "value out of range: " + tc.category,
			Detail:  "", Hint: "", Position: "", InternalPosition: "", InternalQuery: "",
			Context: "", Schema: "", Table: "", Column: "", DataType: "", Constraint: "",
		}
		if phase != "execute" || got != want {
			t.Errorf("%s SQL=%q phase=%s diagnostic=%#v; want phase=execute diagnostic=%#v", tc.name, query, phase, got, want)
		}
		return
	}
	t.Errorf("%s SQL=%q phase=%s unexpected diagnostic=%#v (category=%s)", tc.name, query, phase, got, tc.category)
}

// This small public executor seam accepts a pool, physical Conn or held Stmt.
// It consumes and closes all rows before comparing the independent expectation.
type referenceMathConstantsQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func referenceMathConstantsProbeQuery(t *testing.T, p *fixture.Pair, backend fixture.Backend, x referenceMathConstantsQuerier, query, column string, tc referenceMathConstantsProbe, args ...any) {
	t.Helper()
	ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
	rows, err := x.QueryContext(ctx, query, args...)
	if err != nil {
		referenceMathConstantsProbeError(t, tc, query, "execute", err)
		return
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || len(columns) != 1 || columns[0] != column {
		t.Fatalf("%q columns=%v error=%v", query, columns, err)
	}
	referenceMathTrigMetadata(t, rows, backend.PG, false)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			referenceMathConstantsProbeError(t, tc, query, "execute", err)
		} else {
			t.Errorf("%q phase=execute missing result", query)
		}
		return
	}
	var got float64
	if err := rows.Scan(&got); err != nil {
		t.Fatal(query, err)
	}
	if rows.Next() {
		t.Fatal(query, "unexpected second row")
	}
	if err := rows.Err(); err != nil {
		referenceMathConstantsProbeError(t, tc, query, "execute", err)
		return
	}
	if err := rows.Close(); err != nil {
		t.Fatal(query, err)
	}
	backend.Recorder.Mu.Lock()
	var captured *fixture.Operation
	for _, operation := range backend.Recorder.Operations {
		if operation.SQL == query && operation.Err == nil && operation.Columns != nil {
			captured = operation
		}
	}
	valid := captured != nil && len(captured.Columns) == 1 && captured.Columns[0] == column && len(captured.Rows) == 1 && len(captured.Rows[0]) == 1 && captured.Closed && captured.CloseErr == nil
	if valid {
		recorded, ok := fixture.Normalize(captured.Rows[0][0]).(float64)
		valid = ok && referenceScalarFloatMatches(recorded, got, 0)
	}
	backend.Recorder.Mu.Unlock()
	if !valid {
		t.Errorf("%q phase=execute invalid consumed recording", query)
	}
	want := tc.want
	if backend.PG && tc.pgWantBits != 0 {
		want = math.Float64frombits(tc.pgWantBits)
	}
	if tc.category != "value" {
		t.Errorf("%s SQL=%q args=%#v phase=execute unexpected success=%.17g bits=%016x signbit=%v (category=%s)", tc.name, query, args, got, math.Float64bits(got), math.Signbit(got), tc.category)
	} else if !referenceScalarFloatMatches(got, want, tc.ulps) {
		t.Errorf("%s SQL=%q phase=execute success=%.17g bits=%016x signbit=%v want=%.17g bits=%016x tolerance=%d ULP", tc.name, query, got, math.Float64bits(got), math.Signbit(got), want, math.Float64bits(want), tc.ulps)
	}
}

func referenceMathConstantsProbeModes(t *testing.T, p *fixture.Pair, backend fixture.Backend, tc referenceMathConstantsProbe) {
	t.Helper()
	for _, prefix := range []string{"", "/*é*/ "} {
		query := prefix + "SELECT " + tc.function + "('" + tc.input + "'::float8) AS result"
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("prefix=%q/prepared=%v", prefix, prepared), func(t *testing.T) {
				if !prepared {
					referenceMathConstantsProbeQuery(t, p, backend, backend.DB, query, "result", tc)
					return
				}
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
				stmt, err := backend.DB.PrepareContext(ctx, query)
				if err != nil {
					referenceMathConstantsProbeError(t, tc, query, "prepare", err)
					return
				}
				defer stmt.Close()
				held := fixture.HeldStatement{backend.DB, stmt, query}
				referenceMathConstantsProbeQuery(t, p, backend, held, query, "result", tc)
			})
		}
	}
}
