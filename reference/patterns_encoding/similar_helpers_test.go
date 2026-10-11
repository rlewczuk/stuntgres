package reference_test

import (
	"database/sql"
	"fmt"
	"math"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func similarReferenceRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, step fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t/%s/%v", prepared, step.Query, step.Args), func(t *testing.T) {
			s := step
			s.Prepared = prepared
			if s.State != "" {
				if !prepared || s.ErrorStage != "prepare" {
					s.ErrorStage = "execute"
				}
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
}

func similarReferenceError(query string, diagnostic fixture.Diagnostic, args ...any) fixture.Step {
	step := fixture.ImplicitError(query, diagnostic, args...)
	step.SelectRows = true
	step.ErrorStage = "execute"
	return step
}

func similarReferenceBindingError(query string, diagnostic fixture.Diagnostic) fixture.Step {
	step := similarReferenceError(query, diagnostic)
	step.ErrorStage = "prepare"
	return step
}

func similarReferenceRegexError(query, reason, context string, args ...any) fixture.Step {
	return similarReferenceError(query, fixture.Diagnostic{Code: "2201B", Message: "invalid regular expression: " + reason, Context: context}, args...)
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
