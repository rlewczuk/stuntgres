package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Manual acceptance examples and explicit root-observed PostgreSQL 18.0
// contracts are both independent of native execution.
type datetimeFormattingCase struct {
	name, expression, sqlType string
	want                      any
	diagnostic                *referenceDiagnostic
	nativeDiagnostic          *referenceDiagnostic
}

func datetimeFormattingQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func datetimeFormattingDiagnostic(code, message, detail, hint string) *referenceDiagnostic {
	d := controlsReferenceDiagnostic(code, message)
	d.Detail, d.Hint = detail, hint
	return &d
}

func datetimeFormattingScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, *sql.Conn)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			referenceRunStep(t, p.ctx, conn, backend.recorder, referenceExec("SET TIME ZONE 'UTC'", 0))
			body(t, p, backend, conn)
		})
	}
}

func datetimeFormattingCases(t *testing.T, cases []datetimeFormattingCase) {
	t.Helper()
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn) {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						diagnostic := c.diagnostic
						if !backend.pg && c.nativeDiagnostic != nil {
							diagnostic = c.nativeDiagnostic
						}
						query := "SELECT " + c.expression + " AS result"
						if expected, ok := datetimeFormattingFindContract(query); ok {
							if !backend.pg && c.nativeDiagnostic != nil {
								expected = datetimeFormattingExpectation{diagnostic: c.nativeDiagnostic, prepareError: true}
							}
							s := datetimeFormattingContractStep(expected, backend.pg, query, prepared)
							referenceRunStep(t, p.ctx, conn, backend.recorder, s)
							return
						}
						var s referenceStep
						if diagnostic != nil {
							s = referenceErrorDiagnostic(query, *diagnostic)
							s.selectRows = true
						} else {
							typ := c.sqlType
							if typ == "" {
								typ = "TEXT"
							}
							s = referenceSelect(query, []string{"result"}, []string{typ}, [][]any{{c.want}})
						}
						if prepared {
							s = referencePrepared(s)
						}
						referenceRunStep(t, p.ctx, conn, backend.recorder, s)
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
	diagnostic     *referenceDiagnostic
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

func datetimeFormattingContractStep(expected datetimeFormattingExpectation, postgres bool, query string, prepared bool, args ...any) referenceStep {
	var s referenceStep
	if expected.diagnostic != nil {
		s = referenceErrorDiagnostic(query, *expected.diagnostic, args...)
		s.selectRows = true
		s.errorStage = "execute"
		if prepared && expected.prepareError {
			s.errorStage = "prepare"
		}
	} else if len(expected.columns) == 0 {
		s = referenceSelect(query, []string{"result"}, []string{"TEXT"}, [][]any{{expected.value}}, args...)
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
		s = referenceSelect(query, expected.columns, expected.types, rows, args...)
	}
	if prepared {
		s = referencePrepared(s)
	}
	return s
}

func datetimeFormattingAssertContract(t *testing.T, p *referencePair, backend referenceBackend, conn *sql.Conn, query string, prepared bool, args ...any) {
	t.Helper()
	expected, ok := datetimeFormattingFindContract(query)
	if !ok {
		t.Fatalf("missing independently recorded contract for %q", query)
	}
	referenceRunStep(t, p.ctx, conn, backend.recorder, datetimeFormattingContractStep(expected, backend.pg, query, prepared, args...))
}
