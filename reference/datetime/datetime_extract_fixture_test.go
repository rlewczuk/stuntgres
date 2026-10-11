package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Independently derived expectations were grounded by root's pinned 18.0
// discovery and corrected oracle validation (tests-1 and tests-2).
func datetimeExtractScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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

func datetimeExtractBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%t", s.Query, prepared), func(t *testing.T) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
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

func datetimeExtractPair(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, field, source, numeric string, floating float64) {
	t.Helper()
	q := "SELECT " + datetimeExtractSQL("extract", field, source) + "," + datetimeExtractSQL("date_part", field, source)
	datetimeExtractBoth(t, p, b, x, fixture.Select(q, []string{"extract", "date_part"}, []string{"NUMERIC", "FLOAT8"}, [][]any{{numeric, floating}}))
}

func datetimeExtractFieldDiagnostic(code, field, typ string) fixture.Diagnostic {
	verb := "not recognized"
	if code == "0A000" {
		verb = "not supported"
	}
	return fixture.ControlsReferenceDiagnostic(code, fmt.Sprintf("unit %q %s for type %s", field, verb, typ))
}

func datetimeExtractDateWrapperDiagnostic(d fixture.Diagnostic) fixture.Diagnostic {
	d.Context = `SQL function "date_part" statement 1`
	return d
}

func datetimeExtractFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, q string, d fixture.Diagnostic, prepareStage string, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("%s/prepared=%t", q, prepared), func(t *testing.T) {
			s := fixture.ErrorDiagnostic(q, d, args...)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = prepareStage
			}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
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
