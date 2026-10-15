package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func mathLogPowerScenario(t *testing.T, body func(*testing.T, context.Context, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			body(t, p.Ctx, conn, func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			})
		})
	}
}

func mathLogPowerBoth(run func(fixture.Step), s fixture.Step) {
	run(s)
	run(fixture.Prepared(s))
}
func mathLogPowerErrors(run func(fixture.Step), query string, want fixture.Diagnostic, prepareFailure bool, args ...any) {
	s := fixture.ImplicitError(query, want, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	run(s)
	if prepareFailure {
		s.ErrorStage = "prepare"
	}
	run(fixture.Prepared(s))
}

// Finite irrational targets are independently specified constants, confirmed by
// pinned PostgreSQL18.0. Only those values admit a two-ULP libm difference;
// NaN, infinities and signed zeros retain exact categories and signs.
func mathLogPowerFloat(t *testing.T, ctx context.Context, conn *sql.Conn, query string, want float64, ulps uint64) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%v", query, prepared), func(t *testing.T) {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, prepareErr := conn.PrepareContext(ctx, query)
				if prepareErr != nil {
					t.Fatalf("quiet prepare: %v", prepareErr)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(ctx)
			} else {
				rows, err = conn.QueryContext(ctx, query)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 1 || columns[0].Name() != "result" || columns[0].DatabaseTypeName() != "FLOAT8" {
				t.Fatalf("FLOAT8 result metadata=%v error=%v", columns, err)
			}
			if !rows.Next() {
				t.Fatalf("missing result: %v", rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			got, ok := raw.(float64)
			if !ok || !referenceScalarFloatMatches(got, want, ulps) {
				t.Errorf("got %T %.17g bits=%016x want %.17g bits=%016x tolerance=%dULP", raw, got, math.Float64bits(got), want, math.Float64bits(want), ulps)
			}
			if rows.Next() {
				t.Fatal("unexpected second row")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
