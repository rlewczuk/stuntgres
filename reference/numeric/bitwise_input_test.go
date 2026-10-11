package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitwiseContextualInputWidths(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE one(v INTEGER)", 0))
	p.Step(t, fixture.Exec("INSERT INTO one VALUES(1)", 1))
	for _, tc := range []struct{ q, arg, code, message string }{
		{"SELECT 1::smallint & $1 FROM one", "bad", "22P02", "invalid input syntax for type smallint: \"bad\""},
		{"SELECT 1::smallint & $1 FROM one", "32768", "22003", "value \"32768\" is out of range for type smallint"},
		{"SELECT 1::integer & $1 FROM one", "bad", "22P02", "invalid input syntax for type integer: \"bad\""},
		{"SELECT 1::integer & $1 FROM one", "2147483648", "22003", "value \"2147483648\" is out of range for type integer"},
		{"SELECT 1::bigint & $1 FROM one", "bad", "22P02", "invalid input syntax for type bigint: \"bad\""},
		{"SELECT 1::bigint & $1 FROM one", "9223372036854775808", "22003", "value \"9223372036854775808\" is out of range for type bigint"},
		{"SELECT 1::smallint << $1 FROM one", "bad", "22P02", "invalid input syntax for type integer: \"bad\""},
		{"SELECT 1::bigint >> $1 FROM one", "2147483648", "22003", "value \"2147483648\" is out of range for type integer"},
	} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.arg)
		s.SelectRows = true
		s.ErrorStage = "execute"
		pg := *s.Diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		s.Diagnostic = &pg
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
	for _, tc := range []struct{ typ, meta string }{{"smallint", "INT2"}, {"integer", "INT4"}, {"bigint", "INT8"}} {
		s := fixture.Select("SELECT 1::"+tc.typ+" & '3' FROM one", []string{"?column?"}, []string{tc.meta}, [][]any{{int64(1)}})
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}
