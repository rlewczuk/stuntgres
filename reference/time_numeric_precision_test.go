//go:build reference

package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceTimeNumericPrecision(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			for _, head := range []string{"TIME", `"time"`, `U&"t\0069me"`} {
				for _, tc := range []struct{ number, token, primary string }{
					{"1.5foo", "1.5foo", "trailing junk after numeric literal"}, {".5foo", ".5foo", "trailing junk after numeric literal"}, {"1.5e+", "1.5e+", "trailing junk after numeric literal"}, {"1..2", "..", "syntax error"}, {".5e-", ".5e-", "trailing junk after numeric literal"}, {"1.5e", "1.5e", "trailing junk after numeric literal"}, {"1.5e+3foo", "1.5e+3foo", "trailing junk after numeric literal"}, {"1.5_", "1.5_", "trailing junk after numeric literal"}, {"1.5e+3_", "1.5e+3_", "trailing junk after numeric literal"}, {"1e+", "1e+", "trailing junk after numeric literal"}, {"1efoo", "1efoo", "trailing junk after numeric literal"}, {"1.5e3foo", "1.5e3foo", "trailing junk after numeric literal"},
				} {
					q := "/*é*/ CREATE TABLE bad(v " + head + "(" + tc.number + "))"
					position := strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: tc.primary + " at or near \"" + tc.token + "\"", Position: position})
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared {
							s.errorStage = "prepare"
						}
						run(s)
					}
					run(referenceExec("CREATE TABLE bad(v TIME)", 0))
					run(referenceExec("DROP TABLE bad", 0))
				}
				for _, number := range []string{".5", "1.5", "1e+2", "2147483648", "999999999999999999999999", "-2147483648"} {
					q := "CREATE TABLE bad(v " + head + "(" + number + "))"
					code, message, position := "22P02", "invalid input syntax for type integer: \""+number+"\"", "20"
					if head == "TIME" {
						code = "42601"
						token := number
						if strings.HasPrefix(number, "-") {
							token = "-"
						}
						message = "syntax error at or near \"" + token + "\""
						position = strconv.Itoa(strings.Index(q, number) + 1)
					} else if number == "2147483648" || number == "999999999999999999999999" {
						code = "22003"
						message = "value \"" + number + "\" is out of range for type integer"
					} else if number == "-2147483648" {
						code = "22023"
						message = "TIME(-2147483648) precision must not be negative"
					}
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: code, Message: message, Position: position})
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared && head == "TIME" {
							s.errorStage = "prepare"
						}
						run(s)
					}
					run(referenceExec("CREATE TABLE bad(v TIME)", 0))
					run(referenceExec("DROP TABLE bad", 0))
				}
				for _, number := range []string{"0x3", "0b11", "0o3", "0_3"} {
					run(referenceExec("CREATE TABLE radix(v "+head+"("+number+"))", 0))
					run(referenceExec("DROP TABLE radix", 0))
				}
			}
		})
	}
}
