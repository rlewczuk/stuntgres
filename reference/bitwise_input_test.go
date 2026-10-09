package reference_test

import "testing"

func TestReferenceBitwiseContextualInputWidths(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE one(v INTEGER)", 0))
	p.step(t, referenceExec("INSERT INTO one VALUES(1)", 1))
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
		s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.arg)
		s.selectRows = true
		s.errorStage = "execute"
		pg := *s.diagnostic
		pg.Context = "unnamed portal parameter $1 = '...'"
		s.diagnostic = &pg
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
	for _, tc := range []struct{ typ, meta string }{{"smallint", "INT2"}, {"integer", "INT4"}, {"bigint", "INT8"}} {
		s := referenceSelect("SELECT 1::"+tc.typ+" & '3' FROM one", []string{"?column?"}, []string{tc.meta}, [][]any{{int64(1)}})
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}
