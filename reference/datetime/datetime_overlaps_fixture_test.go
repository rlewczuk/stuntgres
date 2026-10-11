package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations were independently validated against pinned PostgreSQL 18.0.
func datetimeOverlapsScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func datetimeOverlapsMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	datetimeOverlapsMetadataExpected(t, rows, false, false)
}

func datetimeOverlapsMetadataExpected(t *testing.T, rows *sql.Rows, wantNullable, wantKnown bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range columns {
		length, hasLength := c.Length()
		precision, scale, decimal := c.DecimalSize()
		nullable, known := c.Nullable()
		if c.DatabaseTypeName() != "BOOL" || c.ScanType() != reflect.TypeFor[bool]() || length != 0 || hasLength || precision != 0 || scale != 0 || decimal || nullable != wantNullable || known != wantKnown {
			t.Fatalf("BOOL metadata name=%s type=%s scan=%v length=(%d,%t) decimal=(%d,%d,%t) nullable=(%t,%t)", c.Name(), c.DatabaseTypeName(), c.ScanType(), length, hasLength, precision, scale, decimal, nullable, known)
		}
	}
}

// Native stored relation columns expose catalog nullability; lib/pq does not.
// Computed projections continue to use the strict unknown-metadata callback.
func datetimeOverlapsStoredMetadata(pg bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		datetimeOverlapsMetadataExpected(t, rows, !pg, !pg)
	}
}

func datetimeOverlapsBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.Prepared = prepared
			if s.SelectRows && s.State == "" && s.CheckMetadata == nil {
				s.CheckMetadata = datetimeOverlapsMetadata
			}
			if s.State != "" {
				s.ErrorStage = "execute"
				if prepared && s.Diagnostic.Position != "" {
					s.ErrorStage = "prepare"
				}
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func datetimeOverlapsValue(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, expression string, value any) {
	t.Helper()
	datetimeOverlapsBoth(t, p, b, c, fixture.Select("SELECT "+expression, []string{"overlaps"}, []string{"BOOL"}, [][]any{{value}}))
}

func datetimeOverlapsFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	s := fixture.ErrorDiagnostic(query, d, args...)
	s.SelectRows = true
	datetimeOverlapsBoth(t, p, b, c, s)
}

func datetimeOverlapsPair(a, b, c, d string) string {
	return "(" + a + "," + b + ") OVERLAPS (" + c + "," + d + ")"
}

func datetimeOverlapsPoint(typ string, n int) string {
	switch typ {
	case "TIME":
		return fmt.Sprintf("TIME '%02d:00'", n)
	case "TIMETZ":
		return fmt.Sprintf("TIMETZ '%02d:00+00'", n)
	case "DATE":
		return fmt.Sprintf("DATE '2001-01-%02d'", n)
	case "TIMESTAMPTZ":
		return fmt.Sprintf("TIMESTAMPTZ '2001-01-%02d 00:00+00'", n)
	default:
		return fmt.Sprintf("TIMESTAMP '2001-01-%02d'", n)
	}
}
