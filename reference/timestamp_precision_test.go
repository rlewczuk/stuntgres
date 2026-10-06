//go:build reference

package reference_test

import (
	"database/sql"
	"strconv"
	"testing"
	"time"
)

func TestReferenceTimestampPrecisionScannerDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		name   string
		db     *sql.DB
		record *referenceRecorder
	}{
		{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord},
	} {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.record, s) }
			fail := func(q string, want referenceDiagnostic) {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(q, want)
					s.prepared = prepared
					s.errorStage = "execute"
					if prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
			}
			epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
			run(referenceExec("CREATE TABLE anchor(v TIMESTAMP)", 0))
			run(referenceExec("INSERT INTO anchor VALUES('epoch')", 1))
			for _, head := range []struct {
				sql      string
				position int
			}{
				{"TIMESTAMP", 30}, {`"timestamp"`, 32}, {`U&"t\0069mestamp"`, 38},
			} {
				for _, tc := range []struct {
					spelling, token, primary string
					delta                    int
				}{
					{"1.5foo", "1.5foo", "trailing junk after numeric literal", 0},
					{".5foo", ".5foo", "trailing junk after numeric literal", 0},
					{"1.5e+", "1.5e+", "trailing junk after numeric literal", 0},
					{"1..2", "..", "syntax error", 1},
					{".5e-", ".5e-", "trailing junk after numeric literal", 0},
					{"1.5e", "1.5e", "trailing junk after numeric literal", 0},
					{"1.5e+3foo", "1.5e+3foo", "trailing junk after numeric literal", 0},
					{"1.5_", "1.5_", "trailing junk after numeric literal", 0},
					{"1.5e+3_", "1.5e+3_", "trailing junk after numeric literal", 0},
					{".5", ".5", "syntax error", 0},
					{"1.5e+3", "1.5e+3", "syntax error", 0},
					{"1e+2", "1e+2", "syntax error", 0},
					{"1e+", "1e+", "trailing junk after numeric literal", 0},
					{"1efoo", "1efoo", "trailing junk after numeric literal", 0},
					{"1.5e3foo", "1.5e3foo", "trailing junk after numeric literal", 0},
					{"1.", "1.", "syntax error", 0},
				} {
					if head.sql != "TIMESTAMP" && tc.primary == "syntax error" && tc.delta == 0 {
						continue
					}
					for _, table := range []string{"bad", `"é"`} {
						fail("CREATE TABLE "+table+"(v "+head.sql+"("+tc.spelling+"))", referenceDiagnostic{Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(head.position + tc.delta)})
						// Same-name control distinguishes rejection from partial catalog publication.
						run(referenceExec("CREATE TABLE "+table+"(v TIMESTAMP)", 0))
						run(referenceSelect("SELECT v FROM "+table, []string{"v"}, []string{"TIMESTAMP"}, [][]any{}))
						run(referenceExec("DROP TABLE "+table, 0))
					}
					if head.sql == "TIMESTAMP" {
						fail("INSERT INTO anchor VALUES('2000-01-01'),(TIMESTAMP("+tc.spelling+") 'epoch')", referenceDiagnostic{Code: "42601", Message: tc.primary + ` at or near "` + tc.token + `"`, Position: strconv.Itoa(52 + tc.delta)})
						run(referenceSelect("SELECT v FROM anchor", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{epoch}}))
					}
				}
			}
			for _, sign := range []string{"+", "-"} {
				fail("CREATE TABLE bad(v TIMESTAMP("+sign+"1))", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "` + sign + `"`, Position: "30"})
			}
			for _, tc := range []struct {
				head     string
				position int
			}{{`"timestamp"`, 33}, {`U&"t\0069mestamp"`, 39}} {
				for _, number := range []string{"+1.5foo", "-1..2"} {
					want := referenceDiagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1.5foo"`, Position: strconv.Itoa(tc.position)}
					if number == "-1..2" {
						want.Message = `syntax error at or near ".."`
						want.Position = strconv.Itoa(tc.position + 1)
					}
					fail("CREATE TABLE bad(v "+tc.head+"("+number+"))", want)
				}
			}
			for _, q := range []string{`CREATE TABLE signed(v "timestamp"(3))`, `CREATE TABLE unicode_signed(v U&"t\0069mestamp"(3))`} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("INSERT INTO anchor VALUES(TIMESTAMP(3) '2000-01-01 00:00:00.1236')", 1))
			run(referenceSelect("SELECT v FROM anchor ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{epoch}, {time.Date(2000, 1, 1, 0, 0, 0, 124000000, time.UTC)}}))
		})
	}
}
