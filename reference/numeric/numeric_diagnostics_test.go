package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceNumericDiagnosticsPhasesAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE n(v NUMERIC(3,1))", 0))
	for _, tc := range []struct {
		q    string
		want fixture.Diagnostic
	}{
		{"SELECT v FROM n WHERE v=TRUE", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: numeric = boolean", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: "24"}},
		{"CREATE TABLE bad(v NUMERIC(0))", fixture.Diagnostic{Code: "22023", Message: "NUMERIC precision 0 must be between 1 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(1001))", fixture.Diagnostic{Code: "22023", Message: "NUMERIC precision 1001 must be between 1 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,1001))", fixture.Diagnostic{Code: "22023", Message: "NUMERIC scale 1001 must be between -1000 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,-1001))", fixture.Diagnostic{Code: "22023", Message: "NUMERIC scale -1001 must be between -1000 and 1000", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2,1,0))", fixture.Diagnostic{Code: "22023", Message: "invalid NUMERIC type modifier", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(+2))", fixture.Diagnostic{Code: "42601", Message: "type modifiers must be simple constants or identifiers", Position: "20"}},
		{"CREATE TABLE bad(v NUMERIC(2147483648))", fixture.Diagnostic{Code: "22003", Message: `value "2147483648" is out of range for type integer`, Position: "20"}},
		{"INSERT INTO n VALUES ('oops')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "23"}},
		{"SELECT v FROM n WHERE v='oops'", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "oops"`, Position: "25"}},
		{"SELECT 1e131072 FROM n", fixture.Diagnostic{Code: "22003", Message: "value overflows numeric format", Position: "8"}},
		{"SELECT 1e-16384 FROM n", fixture.Diagnostic{Code: "22003", Message: "value overflows numeric format", Position: "8"}},
		{"SELECT 1e+ FROM n", fixture.Diagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1e+"`, Position: "8"}},
		{"SELECT 1.2__3 FROM n", fixture.Diagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1.2__3"`, Position: "8"}},
		{"SELECT 0x FROM n", fixture.Diagnostic{Code: "42601", Message: `invalid hexadecimal integer at or near "0x"`, Position: "8"}},
		{"SELECT 1..2 FROM n", fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ".."`, Position: "9"}},
		{`CREATE TABLE bad(v "decimal")`, fixture.Diagnostic{Code: "42704", Message: `type "decimal" does not exist`, Position: "20"}},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.q, tc.want)
			s.Prepared = prepared
			s.SelectRows = strings.HasPrefix(tc.q, "SELECT")
			s.ErrorStage = "execute"
			if prepared && !strings.HasPrefix(tc.q, "CREATE TABLE") {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
			if s.SelectRows && !prepared {
				s.SelectRows = false
				p.Step(t, s)
			}
		}
	}
	for _, literal := range []string{"'99.95'", "99.95", "'Infinity'", "'-Infinity'"} {
		detail := "A field with precision 3, scale 1 must round to an absolute value less than 10^2."
		if strings.Contains(literal, "Infinity") {
			detail = "A field with precision 3, scale 1 cannot hold an infinite value."
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("INSERT INTO n VALUES ("+literal+")", fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: detail})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			p.Step(t, s)
		}
	}
	for _, arg := range []string{"oops", "1e-16384", "9e-16384", "1e-20000"} {
		code, message := "22P02", `invalid input syntax for type numeric: "oops"`
		if arg != "oops" {
			code, message = "22003", "value overflows numeric format"
		}
		s := fixture.ImplicitError("INSERT INTO n VALUES($1)", fixture.Diagnostic{Code: code, Message: message}, arg)
		s.ErrorStage = "execute"
		pg := *s.Diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		s.Diagnostic = &pg
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, literal := range []string{"+NaN", "-NaN", "Infinityx"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("INSERT INTO n VALUES ('"+literal+"')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type numeric: "` + literal + `"`, Position: "23"})
			s.Prepared = prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			p.Step(t, s)
		}
	}
	q := "/*😀é*/ SELECT 1.2__3 FROM n"
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `trailing junk after numeric literal at or near "1.2__3"`, Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "1.2")]) + 1)})
	s.SelectRows = true
	p.Step(t, s)
	for _, tc := range []struct{ decl, in, detail string }{{"NUMERIC(2,-3)", "99500", "A field with precision 2, scale -3 must round to an absolute value less than 10^5."}, {"NUMERIC(3,5)", ".009995", "A field with precision 3, scale 5 must round to an absolute value less than 10^-2."}, {"NUMERIC(2,2)", "1.00", "A field with precision 2, scale 2 must round to an absolute value less than 1."}} {
		p.Step(t, fixture.Exec("CREATE TABLE sized(v "+tc.decl+")", 0))
		s := fixture.ImplicitError("INSERT INTO sized VALUES("+tc.in+")", fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: tc.detail})
		s.ErrorStage = "execute"
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
		p.Step(t, fixture.Exec("DROP TABLE sized", 0))
	}
	for _, backend := range p.Backends() {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := backend.DB.QueryContext(ctx, "SELECT v FROM n")
		if rows != nil {
			rows.Close()
			t.Fatal("canceled Query exposed cursor")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}
