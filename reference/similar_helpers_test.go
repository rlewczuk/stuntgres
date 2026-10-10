package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"testing"
)

func similarReferenceRoutes(t *testing.T, p *referencePair, b referenceBackend, step referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t/%s/%v", prepared, step.query, step.args), func(t *testing.T) {
			s := step
			s.prepared = prepared
			if s.state != "" {
				if !prepared || s.errorStage != "prepare" {
					s.errorStage = "execute"
				}
			}
			referenceRunStep(t, p.ctx, b.db, b.recorder, s)
		})
	}
}

func similarReferenceError(query string, diagnostic referenceDiagnostic, args ...any) referenceStep {
	step := referenceImplicitError(query, diagnostic, args...)
	step.selectRows = true
	step.errorStage = "execute"
	return step
}

func similarReferenceBindingError(query string, diagnostic referenceDiagnostic) referenceStep {
	step := similarReferenceError(query, diagnostic)
	step.errorStage = "prepare"
	return step
}

func similarReferenceRegexError(query, reason, context string, args ...any) referenceStep {
	return similarReferenceError(query, referenceDiagnostic{Code: "2201B", Message: "invalid regular expression: " + reason, Context: context}, args...)
}

// Expression nullability is unknown on both adapters; unbounded TEXT length is
// MaxInt64 in the logical native contract and the observed pq contract.
func similarReferenceMetadata(t *testing.T, rows *sql.Rows) {
	t.Helper()
	similarReferenceNullableMetadata(t, rows, false, false)
}

// Native declared view/catalog columns retain their nullable descriptor;
// PostgreSQL's adapter does not expose nullable metadata for these columns.
func similarReferenceDeclaredMetadata(postgres bool) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) { similarReferenceNullableMetadata(t, rows, !postgres, !postgres) }
}

func similarReferenceNullableMetadata(t *testing.T, rows *sql.Rows, wantNullable, wantNullableKnown bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range columns {
		var scan reflect.Type
		switch c.DatabaseTypeName() {
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "TEXT":
			scan = reflect.TypeFor[string]()
		default:
			t.Fatalf("unexpected Similar metadata type %q", c.DatabaseTypeName())
		}
		if c.ScanType() != scan {
			t.Fatalf("%s ScanType=%v want %v", c.Name(), c.ScanType(), scan)
		}
		if nullable, known := c.Nullable(); nullable != wantNullable || known != wantNullableKnown {
			t.Fatalf("%s nullability (%t,%t) want (%t,%t)", c.Name(), nullable, known, wantNullable, wantNullableKnown)
		}
		wantLength, wantKnown := int64(0), false
		if c.DatabaseTypeName() == "TEXT" {
			wantLength, wantKnown = math.MaxInt64, true
		}
		if length, known := c.Length(); length != wantLength || known != wantKnown {
			t.Fatalf("%s length=(%d,%t) want (%d,%t)", c.Name(), length, known, wantLength, wantKnown)
		}
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("%s invented decimal metadata (%d,%d,%t)", c.Name(), precision, scale, known)
		}
	}
}
