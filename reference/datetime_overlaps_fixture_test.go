package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"
)

// Expectations were independently validated against pinned PostgreSQL 18.0.
func datetimeOverlapsScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(q, 0))
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

func datetimeOverlapsBoth(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			s.prepared = prepared
			if s.selectRows && s.state == "" && s.checkMetadata == nil {
				s.checkMetadata = datetimeOverlapsMetadata
			}
			if s.state != "" {
				s.errorStage = "execute"
				if prepared && s.diagnostic.Position != "" {
					s.errorStage = "prepare"
				}
			}
			referenceRunStep(t, p.ctx, c, b.recorder, s)
		})
	}
}

func datetimeOverlapsValue(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, expression string, value any) {
	t.Helper()
	datetimeOverlapsBoth(t, p, b, c, referenceSelect("SELECT "+expression, []string{"overlaps"}, []string{"BOOL"}, [][]any{{value}}))
}

func datetimeOverlapsFailure(t *testing.T, p *referencePair, b referenceBackend, c referenceExecutor, query string, d referenceDiagnostic, args ...any) {
	t.Helper()
	s := referenceErrorDiagnostic(query, d, args...)
	s.selectRows = true
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
