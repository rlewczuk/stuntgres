package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceLoDiagnostics(t *testing.T) {
	for _, tc := range []struct{ input, code string }{
		{"", "22P02"}, {"bad", "22P02"}, {"08", "22P02"}, {"4294967296x", "22P02"},
		{"4294967296", "22003"}, {"-2147483649", "22003"}, {"18446744071562067967", "22003"}, {"18446744073709551616x", "22003"},
	} {
		t.Run("input_"+strconv.Quote(tc.input), func(t *testing.T) {
			loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				loInstall(t, p, b, c)
				loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_one(id integer)", 0))
				loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_one VALUES (1)", 1))
				loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_errors(v lo)", 0))
				for _, q := range []string{"SELECT '" + tc.input + "'::lo FROM lo_one", "SELECT /*é*/ '" + tc.input + "'::lo FROM lo_one", "INSERT INTO lo_errors VALUES ('" + tc.input + "')"} {
					for _, prepared := range []bool{false, true} {
						d := loInputDiagnostic(tc.input, tc.code)
						d.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'")]) + 1)
						s := fixture.ErrorDiagnostic(q, d)
						s.SelectRows = strings.HasPrefix(q, "SELECT")
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if prepared {
							s.ErrorStage = "prepare"
						}
						loRun(t, p, b, c, s)
					}
				}
				for _, q := range []string{"INSERT INTO lo_errors VALUES ($1)", "SELECT $1::lo AS v FROM lo_one"} {
					for _, prepared := range []bool{false, true} {
						s := loBoundError(q, tc.input, tc.code, "1", tc.input)
						s.SelectRows = strings.HasPrefix(q, "SELECT")
						s.Prepared = prepared
						loRun(t, p, b, c, s)
					}
				}
				loRows(t, p, b, c, fixture.Select("SELECT v FROM lo_errors", []string{"v"}, []string{"OID"}, [][]any{}))
			})
		})
	}
	t.Run("column_not_null", func(t *testing.T) {
		loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			loInstall(t, p, b, c)
			loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_required(v lo NOT NULL)", 0))
			d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "v" of relation "lo_required" violates not-null constraint`)
			d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (null).", "public", "lo_required", "v"
			for _, prepared := range []bool{false, true} {
				for _, bound := range []bool{false, true} {
					s := fixture.ErrorDiagnostic("INSERT INTO lo_required VALUES (NULL)", d)
					if bound {
						s = fixture.ErrorDiagnostic("INSERT INTO lo_required VALUES ($1)", d, nil)
					}
					s.Prepared = prepared
					s.ErrorStage = "execute"
					loRun(t, p, b, c, s)
				}
			}
			loMetadataRows(t, p, b, c, fixture.Select("SELECT v FROM lo_required", []string{"v"}, []string{"OID"}, [][]any{}), []bool{false})
		})
	})
}
