//go:build reference

package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func stringCoreReviewResolutionReference(t *testing.T, p *referencePair, oracle bool) {
	run := func(t *testing.T, s referenceStep) {
		t.Helper()
		if oracle {
			referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, s)
		} else {
			p.step(t, s)
		}
	}
	run(t, referenceExec("CREATE TABLE sc_resolution(v TEXT,n INT)", 0))
	run(t, referenceExec("INSERT INTO sc_resolution VALUES('sentinel',7)", 1))
	char := "CASE WHEN false THEN relkind ELSE $1 END"
	for _, tc := range []struct{ left, right, leftType, rightType string }{
		{"'x'", char, "unknown", `"char"`}, {char, "'x'", `"char"`, "unknown"},
		{"NULL", char, "unknown", `"char"`}, {char, "NULL", `"char"`, "unknown"},
		{"$2", char, "unknown", `"char"`}, {char, "$2", `"char"`, "unknown"},
		{char, "relkind", `"char"`, `"char"`}, {"relkind", char, `"char"`, `"char"`},
		{"'x'::name", char, "name", `"char"`}, {char, "'x'::name", `"char"`, "name"},
		{"'x'::varchar", char, "character varying", `"char"`}, {char, "'x'::varchar", `"char"`, "character varying"},
		{"'x'::char(3)", char, "character", `"char"`}, {char, "'x'::char(3)", `"char"`, "character"},
	} {
		q := "SELECT " + tc.left + "||" + tc.right + " AS v FROM pg_catalog.pg_class WHERE relname='sc_resolution'"
		want := referenceDiagnostic{Code: "42725", Message: "operator is not unique: " + tc.leftType + " || " + tc.rightType, Position: strconv.Itoa(len([]rune("SELECT "+tc.left)) + 1), Hint: "Could not choose a best candidate operator. You might need to add explicit type casts."}
		for _, value := range []any{"A", nil} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("category/%s/value=%v/prepared=%t", tc.left+"||"+tc.right, value, prepared), func(t *testing.T) {
					args := []any{value}
					if strings.Contains(q, "$2") {
						args = append(args, "x")
					}
					s := referenceImplicitError(q, want, args...)
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					run(t, s)
				})
			}
		}
	}
	// Root pinned18 artifact110 independently distinguishes legal cast uses,
	// assignment incompatibility, operator lookup, and true42P08 conflicts.
	for _, tc := range []struct {
		q    string
		args []any
		want string
	}{
		{"SELECT $1||$2 AS v", []any{"x", "y"}, "xy"},
		{"SELECT $1||42 AS v", []any{"x"}, "x42"},
		{"SELECT $1::integer||$1::text AS v", []any{42}, "4242"},
		{"SELECT $1::text||$1::integer AS v", []any{"42"}, "4242"},
	} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("known/%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
				s := referenceSelect(tc.q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.args...)
				if prepared {
					s = referencePrepared(s)
				}
				run(t, s)
			})
		}
	}
	for _, tc := range []struct {
		q    string
		args []any
		want referenceDiagnostic
	}{
		{"INSERT INTO sc_resolution(v,n) VALUES($1,$1)", []any{"42"}, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "text versus integer", Position: "42"}},
		{"INSERT INTO sc_resolution(n,v) VALUES($1,$1)", []any{42}, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "42"}},
		{"INSERT INTO sc_resolution(v,n) VALUES($1||'x',$1)", []any{"42"}, referenceDiagnostic{Code: "42804", Message: `column "n" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "47"}},
		{"INSERT INTO sc_resolution(n,v) VALUES($1,$1||'x')", []any{42}, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "text versus integer", Position: "39"}},
		{"SELECT $1||'x' AS v FROM sc_resolution WHERE n=$1", []any{"42"}, referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "47"}},
		{"SELECT btrim($1)||'x' AS v FROM sc_resolution WHERE n=$1", []any{"42"}, referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = text", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "54"}},
		{"SELECT btrim($1,$1::varchar) AS v", []any{"x"}, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text", Position: "14"}},
		{"SELECT pg_catalog.normalize($1,$1::varchar) AS v", []any{"NFC"}, referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "character varying versus text", Position: "29"}},
		{"SELECT CASE WHEN false THEN $1::integer ELSE 42 END||$1 AS v", []any{42}, referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer || integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "52"}},
	} {
		// These independent PG controls are outside selected string inference;
		// native stringless assignment keeps its existing 0A000 boundary.
		if !oracle && (tc.q == "INSERT INTO sc_resolution(v,n) VALUES($1,$1)" || tc.q == "INSERT INTO sc_resolution(n,v) VALUES($1,$1)") {
			continue
		}
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("parameter/%s/prepared=%t", tc.q, prepared), func(t *testing.T) {
				run(t, referenceExec("DROP TABLE sc_resolution", 0))
				run(t, referenceExec("CREATE TABLE sc_resolution(v TEXT,n INT)", 0))
				run(t, referenceExec("INSERT INTO sc_resolution VALUES('sentinel',7)", 1))
				s := referenceImplicitError(tc.q, tc.want, tc.args...)
				if prepared {
					s = referencePrepared(s)
					s.errorStage = "prepare"
				}
				run(t, s)
				run(t, referenceSelect("SELECT v,n FROM sc_resolution", []string{"v", "n"}, []string{"TEXT", "INT4"}, [][]any{{"sentinel", int64(7)}}))
			})
		}
	}
}

func TestReferenceStringCoreReviewResolution(t *testing.T) {
	p := newReferencePair(t)
	stringCoreReviewResolutionReference(t, p, false)
	t.Run("binary_cutover", func(t *testing.T) { stringCoreReviewBinaryConsumptionReference(t, p, false) })
}
