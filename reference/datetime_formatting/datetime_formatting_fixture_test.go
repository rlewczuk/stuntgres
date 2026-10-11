package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Manual acceptance examples and explicit root-observed PostgreSQL 18.0
// contracts are both independent of native execution.
type datetimeFormattingCase struct {
	name, expression, sqlType string
	want                      any
	diagnostic                *fixture.Diagnostic
	nativeDiagnostic          *fixture.Diagnostic
}

func datetimeFormattingQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func datetimeFormattingDiagnostic(code, message, detail, hint string) *fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Detail, d.Hint = detail, hint
	return &d
}

func datetimeFormattingScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
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
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
			body(t, p, backend, conn)
		})
	}
}

func datetimeFormattingCases(t *testing.T, cases []datetimeFormattingCase) {
	t.Helper()
	datetimeFormattingScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						diagnostic := c.diagnostic
						if !backend.PG && c.nativeDiagnostic != nil {
							diagnostic = c.nativeDiagnostic
						}
						query := "SELECT " + c.expression + " AS result"
						if expected, ok := datetimeFormattingFindContract(query); ok {
							if !backend.PG && c.nativeDiagnostic != nil {
								expected = datetimeFormattingExpectation{diagnostic: c.nativeDiagnostic, prepareError: true}
							}
							s := datetimeFormattingContractStep(expected, backend.PG, query, prepared)
							fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
							return
						}
						var s fixture.Step
						if diagnostic != nil {
							s = fixture.ErrorDiagnostic(query, *diagnostic)
							s.SelectRows = true
						} else {
							typ := c.sqlType
							if typ == "" {
								typ = "TEXT"
							}
							s = fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{c.want}})
						}
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
					})
				}
			})
		}
	})
}

// Dense token/edge matrices use explicit SQL-keyed expectations split by
// semantic family. The statement bodies remain shared across both backends.
type datetimeFormattingExpectation struct {
	value          any
	columns, types []string
	rows           [][]any
	diagnostic     *fixture.Diagnostic
	prepareError   bool
}

var datetimeFormattingContracts = []map[string]datetimeFormattingExpectation{
	datetimeFormattingInputContracts1,
	datetimeFormattingInputContracts2,
	datetimeFormattingInputContracts3,
	datetimeFormattingIntervalContracts1,
	datetimeFormattingOutputContracts1,
	datetimeFormattingOutputContracts2,
	datetimeFormattingOutputContracts3,
	datetimeFormattingParseContracts1,
	datetimeFormattingRoutesContracts1,
}

func datetimeFormattingFindContract(query string) (datetimeFormattingExpectation, bool) {
	for _, contracts := range datetimeFormattingContracts {
		if expected, ok := contracts[query]; ok {
			return expected, true
		}
	}
	return datetimeFormattingExpectation{}, false
}

func datetimeFormattingContractStep(expected datetimeFormattingExpectation, postgres bool, query string, prepared bool, args ...any) fixture.Step {
	var s fixture.Step
	if expected.diagnostic != nil {
		s = fixture.ErrorDiagnostic(query, *expected.diagnostic, args...)
		s.SelectRows = true
		s.ErrorStage = "execute"
		if prepared && expected.prepareError {
			s.ErrorStage = "prepare"
		}
	} else if len(expected.columns) == 0 {
		s = fixture.Select(query, []string{"result"}, []string{"TEXT"}, [][]any{{expected.value}}, args...)
	} else {
		rows := make([][]any, len(expected.rows))
		for i, row := range expected.rows {
			rows[i] = append([]any(nil), row...)
			for j, value := range row {
				if n, ok := value.(int); ok {
					rows[i][j] = int64(n)
				}
				if date, ok := value.(time.Time); ok && expected.types[j] == "DATE" && !postgres {
					year := date.Year()
					suffix := ""
					if year <= 0 {
						year = 1 - year
						suffix = " BC"
					}
					rows[i][j] = fmt.Sprintf("%04d-%02d-%02d%s", year, date.Month(), date.Day(), suffix)
				}
			}
		}
		s = fixture.Select(query, expected.columns, expected.types, rows, args...)
	}
	if prepared {
		s = fixture.Prepared(s)
	}
	return s
}

func datetimeFormattingAssertContract(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, query string, prepared bool, args ...any) {
	t.Helper()
	expected, ok := datetimeFormattingFindContract(query)
	if !ok {
		t.Fatalf("missing independently recorded contract for %q", query)
	}
	fixture.RunStep(t, p.Ctx, conn, backend.Recorder, datetimeFormattingContractStep(expected, backend.PG, query, prepared, args...))
}
