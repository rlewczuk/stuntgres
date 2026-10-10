package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// Independently derived expectations were grounded by root's pinned 18.0
// discovery and corrected oracle validation (tests-1 and tests-2).
func datetimeExtractScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
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

func datetimeExtractBoth(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, s referenceStep) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%t", s.query, prepared), func(t *testing.T) {
			s.prepared = prepared
			referenceRunStep(t, p.ctx, x, b.recorder, s)
		})
	}
}

func datetimeExtractSQL(api, field, source string) string {
	field = "'" + strings.ReplaceAll(field, "'", "''") + "'"
	if api == "extract" {
		return "EXTRACT(" + field + " FROM " + source + ")"
	}
	return "date_part(" + field + "," + source + ")"
}

func datetimeExtractPair(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, field, source, numeric string, floating float64) {
	t.Helper()
	q := "SELECT " + datetimeExtractSQL("extract", field, source) + "," + datetimeExtractSQL("date_part", field, source)
	datetimeExtractBoth(t, p, b, x, referenceSelect(q, []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{numeric, floating}}))
}

func datetimeExtractFieldDiagnostic(code, field, typ string) referenceDiagnostic {
	verb := "not recognized"
	if code == "0A000" {
		verb = "not supported"
	}
	return controlsReferenceDiagnostic(code, fmt.Sprintf("unit %q %s for type %s", field, verb, typ))
}

func datetimeExtractDateWrapperDiagnostic(d referenceDiagnostic) referenceDiagnostic {
	d.Context = `SQL function "date_part" statement 1`
	return d
}

func datetimeExtractFailure(t *testing.T, p *referencePair, b referenceBackend, x referenceExecutor, q string, d referenceDiagnostic, prepareStage string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%t", q, prepared), func(t *testing.T) {
			s := referenceErrorDiagnostic(q, d, args...)
			s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
			if prepared {
				s.errorStage = prepareStage
			}
			referenceRunStep(t, p.ctx, x, b.recorder, s)
		})
	}
}

var datetimeExtractFields = strings.Fields("century day decade dow doy epoch hour isodow isoyear julian microseconds millennium milliseconds minute month quarter second timezone timezone_hour timezone_minute week year")

func datetimeExtractFloat(t *testing.T, s string) float64 {
	t.Helper()
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
