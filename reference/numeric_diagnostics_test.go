//go:build reference

package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceNumericDiagnosticsPhasesAndAtomicity(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE n(v NUMERIC(3,1))", 0))
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"SELECT v FROM n WHERE v=TRUE", referenceDiagnostic{Code: "42883", Message: "operator does not exist: numeric = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"}},
		{"CREATE TABLE bad(v NUMERIC(0))", referenceDiagnostic{Code: "22023", Message: "NUMERIC precision 0 must be between 1 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(1001))", referenceDiagnostic{Code: "22023", Message: "NUMERIC precision 1001 must be between 1 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,1001))", referenceDiagnostic{Code: "22023", Message: "NUMERIC scale 1001 must be between -1000 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,-1001))", referenceDiagnostic{Code: "22023", Message: "NUMERIC scale -1001 must be between -1000 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,1,0))", referenceDiagnostic{Code: "22023", Message: "invalid NUMERIC type modifier", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(+2))", referenceDiagnostic{Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2147483648))", referenceDiagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "20"}},
		{"INSERT INTO n VALUES ('oops')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "23"}},
		{"SELECT v FROM n WHERE v='oops'", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "25"}},
		{"SELECT 1e131072 FROM n", referenceDiagnostic{Code: "22003", Message: "value overflows numeric format", Position: "8"}},
		{"SELECT 1e-16384 FROM n", referenceDiagnostic{Code: "22003", Message: "value overflows numeric format", Position: "8"}},
		{"SELECT 1e+ FROM n", referenceDiagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1e+"`, Position: "8"}},
		{"SELECT 1.2__3 FROM n", referenceDiagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1.2__3"`, Position: "8"}},
		{"SELECT 0x FROM n", referenceDiagnostic{Code: "42601", Message: `invalid hexadecimal integer at or near "0x"`, Position: "8"}},
		{"SELECT 1..2 FROM n", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ".."`, Position: "9"}},
		{`CREATE TABLE bad(v "decimal")`, referenceDiagnostic{Code: "42704", Message: `type "decimal" does not exist`, Position: "20"}},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, tc.want)
			s.prepared = prepared
			s.selectRows = strings.HasPrefix(tc.q, "SELECT")
			s.errorStage = "execute"
			if prepared && !strings.HasPrefix(tc.q, "CREATE TABLE") {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
	for _, literal := range []string{"'99.95'", "99.95", "'Infinity'", "'-Infinity'"} {
		detail := "A field with precision 3, scale 1 must round to an absolute value less than 10^2."
		if strings.Contains(literal, "Infinity") {
			detail = "A field with precision 3, scale 1 cannot hold an infinite value."
		}
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("INSERT INTO n VALUES ("+literal+")", referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: detail})
			s.prepared = prepared
			s.errorStage = "execute"
			p.step(t, s)
		}
	}
	for _, arg := range []string{"oops", "1e-16384", "9e-16384", "1e-20000"} {
		code, message := "22P02", `invalid input syntax for type numeric: "oops"`
		if arg != "oops" {
			code, message = "22003", "value overflows numeric format"
		}
		s := referenceImplicitError("INSERT INTO n VALUES($1)", referenceDiagnostic{Code: code, Message: message}, arg)
		s.errorStage = "execute"
		pg := *s.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		s.postgresDiagnostic = &pg
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, literal := range []string{"+NaN", "-NaN", "Infinityx"} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("INSERT INTO n VALUES ('"+literal+"')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "` + literal + `"`, Position: "23"})
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
	q := "/*😀é*/ SELECT 1.2__3 FROM n"
	s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1.2__3"`, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "1.2")]) + 1)})
	s.selectRows = true
	p.step(t, s)
	p.step(t, referenceExec("CREATE TABLE atomic(id INT,v NUMERIC(3,1) NOT NULL,b BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO atomic VALUES(99,9.9,99)", 1))
	unchanged := referenceSelect("SELECT * FROM atomic", []string{"id", "v", "b"}, []string{"INT4", "NUMERIC", "INT8"}, [][]any{{int64(99), "9.9", int64(99)}})
	for _, tc := range []struct {
		q    string
		want referenceDiagnostic
	}{
		{"INSERT INTO atomic VALUES(1,1.2,1),(2,'oops',2)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "39"}},
		{"INSERT INTO atomic VALUES(1,1.2,1),(2,99.95,2)", referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}},
		{"INSERT INTO atomic VALUES(1,1.2,1),(2,1.2,9223372036854775808)", referenceDiagnostic{Code: "22003", Message: "bigint out of range"}},
		{"INSERT INTO atomic VALUES(1,1.2,1),(2,NULL,2)", referenceDiagnostic{Code: "23502", Message: `null value in column "v" of relation "atomic" violates not-null constraint`, Schema: "public", Table: "atomic", Column: "v", Detail: "Failing row contains (2, null, 2)."}},
	} {
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError(tc.q, tc.want)
			s.prepared = prepared
			s.errorStage = "execute"
			if prepared && tc.want.Code == "22P02" {
				s.errorStage = "prepare"
			}
			p.step(t, s)
			p.step(t, unchanged)
		}
	}
	for _, tc := range []struct{ decl, in, detail string }{{"NUMERIC(2,-3)", "99500", "A field with precision 2, scale -3 must round to an absolute value less than 10^5."}, {"NUMERIC(3,5)", ".009995", "A field with precision 3, scale 5 must round to an absolute value less than 10^-2."}, {"NUMERIC(2,2)", "1.00", "A field with precision 2, scale 2 must round to an absolute value less than 1."}} {
		p.step(t, referenceExec("CREATE TABLE sized(v "+tc.decl+")", 0))
		s := referenceImplicitError("INSERT INTO sized VALUES("+tc.in+")", referenceDiagnostic{Code: "22003", Message: "numeric field overflow", Detail: tc.detail})
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referencePrepared(s))
		p.step(t, referenceExec("DROP TABLE sized", 0))
	}
}
