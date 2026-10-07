//go:build reference

package reference_test

import "testing"

func conditionalReferenceSetup(t *testing.T) *referencePair {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE t(id INTEGER,v INTEGER,b BOOLEAN,s TEXT)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES(1,0,TRUE,'x'),(2,2,FALSE,NULL),(3,NULL,NULL,'y')", 3))
	return p
}
func TestReferenceConditionalResults(t *testing.T) {
	p := conditionalReferenceSetup(t)
	cases := []referenceStep{
		referenceSelect("SELECT id FROM t WHERE CASE WHEN b THEN TRUE ELSE FALSE END", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
		referenceSelect("SELECT id,CASE WHEN b THEN 10 WHEN v=2 THEN 20 ELSE 30 END,CASE v WHEN 0 THEN 'zero' WHEN 2 THEN 'two' ELSE 'other' END,CASE WHEN b THEN 1 END FROM t ORDER BY id", []string{"id", "case", "case", "case"}, []string{"INT4", "INT4", "TEXT", "INT4"}, [][]any{{int64(1), int64(10), "zero", int64(1)}, {int64(2), int64(20), "two", nil}, {int64(3), int64(30), "other", nil}}),
		referenceSelect("SELECT CASE v WHEN NULL THEN 1 ELSE 2 END,CASE NULL WHEN NULL THEN 'match' ELSE 'miss' END FROM t ORDER BY id", []string{"case", "case"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), "miss"}, {int64(2), "miss"}, {int64(2), "miss"}}),
		referenceSelect("SELECT CASE WHEN b THEN 1 ELSE v END,CASE WHEN b THEN TRUE WHEN v=2 THEN TRUE ELSE FALSE END FROM t ORDER BY id", []string{"v", "case"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), true}, {nil, false}}),
		referenceSelect("SELECT COALESCE(s,'fallback'),COALESCE(NULL,NULL),GREATEST(v,NULL,2),LEAST(v,NULL,2) FROM t ORDER BY id", []string{"coalesce", "coalesce", "greatest", "least"}, []string{"TEXT", "TEXT", "INT4", "INT4"}, [][]any{{"x", nil, int64(2), int64(0)}, {"fallback", nil, int64(2), int64(2)}, {"y", nil, int64(2), int64(2)}}),
		referenceSelect("SELECT NULLIF(v,0),NULLIF(1,2147483648),NULLIF(1,2.2),NULLIF(NULL,1),NULLIF(1,NULL) FROM t ORDER BY id", []string{"nullif", "nullif", "nullif", "nullif", "nullif"}, []string{"INT4", "INT4", "NUMERIC", "INT4", "INT4"}, [][]any{{nil, int64(1), "1", nil, int64(1)}, {int64(2), int64(1), "1", nil, int64(1)}, {nil, int64(1), "1", nil, int64(1)}}),
		referenceSelect("SELECT CASE WHEN b THEN 1 ELSE 2147483648 END,COALESCE(NULL,1,2.2),GREATEST(NULL,NULL),LEAST(NULL,NULL),NULLIF(NULL,NULL) FROM t ORDER BY id", []string{"case", "coalesce", "greatest", "least", "nullif"}, []string{"INT8", "NUMERIC", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "1", nil, nil, nil}, {int64(2147483648), "1", nil, nil, nil}, {int64(2147483648), "1", nil, nil, nil}}),
		referenceSelect("SELECT CASE WHEN v=0 THEN 0 ELSE 10/v END,COALESCE(v,10/v) FROM t ORDER BY id", []string{"case", "coalesce"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}, {int64(5), int64(2)}, {nil, nil}}),
		referenceSelect("SELECT CASE WHEN TRUE THEN 1 ELSE 1/0 END,CASE WHEN FALSE THEN 1/0 ELSE 1 END,COALESCE(1,1/0) FROM t WHERE id=1", []string{"case", "case", "coalesce"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}),
		referenceSelect("SELECT GREATEST(FALSE,TRUE,NULL),LEAST(FALSE,TRUE,NULL),COALESCE(1),GREATEST(2),LEAST(3),NULLIF(1,1) FROM t WHERE id=1", []string{"greatest", "least", "coalesce", "greatest", "least", "nullif"}, []string{"BOOL", "BOOL", "INT4", "INT4", "INT4", "INT4"}, [][]any{{true, false, int64(1), int64(2), int64(3), nil}}),
	}
	for _, step := range cases {
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
func TestReferenceConditionalParameters(t *testing.T) {
	p := conditionalReferenceSetup(t)
	for _, tc := range []struct {
		q         string
		typ, name string
		arg, want any
	}{
		{"COALESCE($1,1)", "INT4", "coalesce", 2, int64(2)},
		{"COALESCE($1,1)", "INT4", "coalesce", nil, int64(1)},
		{"CASE WHEN $1 THEN 1 ELSE 2 END", "INT4", "case", true, int64(1)},
		{"CASE WHEN $1 THEN 1 ELSE 2 END", "INT4", "case", false, int64(2)},
		{"CASE WHEN $1 THEN 1 ELSE 2 END", "INT4", "case", nil, int64(2)},
		{"NULLIF($1,1)", "INT4", "nullif", 1, nil},
		{"NULLIF($1,1)", "INT4", "nullif", 2, int64(2)},
		{"NULLIF($1,1)", "INT4", "nullif", nil, nil},
		{"COALESCE($1,NULL)", "TEXT", "coalesce", "x", "x"},
		{"COALESCE($1,NULL)", "TEXT", "coalesce", nil, nil},
	} {
		step := referenceSelect("SELECT "+tc.q+" FROM t WHERE id=1", []string{tc.name}, []string{tc.typ}, [][]any{{tc.want}}, tc.arg)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, arg := range []any{"x", nil} {
		want := int64(2)
		if arg != nil {
			want = 1
		}
		step := referenceSelect("SELECT CASE $1 WHEN 'x' THEN 1 ELSE 2 END FROM t WHERE id=1", []string{"case"}, []string{"INT4"}, [][]any{{want}}, arg)
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
	for _, tc := range []struct {
		q string
		d referenceDiagnostic
	}{
		{"SELECT COALESCE($1,1),COALESCE($1,TRUE) FROM t", referenceDiagnostic{Code: "42804", Message: "COALESCE types integer and boolean cannot be matched", Position: "35"}},
		{"SELECT $1,COALESCE($1,1) FROM t", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "8"}},
		{"SELECT CASE $1 WHEN 1 THEN 2 END FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: text = integer", Position: "16", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
	} {
		step := referenceImplicitError(tc.q, tc.d, 1)
		step.selectRows = true
		step.errorStage = "execute"
		p.step(t, step)
		step.errorStage = "prepare"
		p.step(t, referencePrepared(step))
	}
	// Text input remains valid until a demanded int4 cast. PostgreSQL custom
	// planning can fold actual parameters, so this demand witness is native-only
	// in the public smoke and engine tests rather than a false parity promise.
}
func TestReferenceConditionalDiagnostics(t *testing.T) {
	p := conditionalReferenceSetup(t)
	for _, empty := range []bool{false, true} {
		if empty {
			p.step(t, referenceExec("DELETE FROM t", 3))
		}
		for _, tc := range []struct {
			q string
			d referenceDiagnostic
		}{
			{"SELECT CASE WHEN 1 THEN 2 END FROM t", referenceDiagnostic{Code: "42804", Message: "argument of CASE/WHEN must be type boolean, not type integer", Position: "18"}},
			{"SELECT CASE WHEN 'truth' THEN 1 END FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Position: "18"}},
			{"SELECT COALESCE(1,TRUE) FROM t", referenceDiagnostic{Code: "42804", Message: "COALESCE types integer and boolean cannot be matched", Position: "19"}},
			{"SELECT CASE WHEN TRUE THEN 1 ELSE FALSE END FROM t", referenceDiagnostic{Code: "42804", Message: "CASE types boolean and integer cannot be matched", Position: "28"}},
			{"SELECT NULLIF(1,TRUE) FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: integer = boolean", Position: "8", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{"SELECT CASE '1' WHEN 1 THEN 2 END FROM t", referenceDiagnostic{Code: "42883", Message: "operator does not exist: text = integer", Position: "17", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}},
			{"SELECT COALESCE(1,'bad') FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "19"}},
			{"SELECT /*é*/ COALESCE(1,'bad') FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "25"}},
			{"SELECT CASE WHEN TRUE THEN 1 ELSE 'bad' END FROM t", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Position: "35"}},
			{"SELECT COALESCE() FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "17"}},
			{"SELECT GREATEST() FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "17"}},
			{"SELECT LEAST() FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "14"}},
			{"SELECT NULLIF(1) FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: "16"}},
			{"SELECT NULLIF(1,2,3) FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near ","`, Position: "18"}},
			{"SELECT CASE END FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "END"`, Position: "13"}},
			{"SELECT CASE WHEN TRUE 1 END FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "1"`, Position: "23"}},
			{"SELECT CASE WHEN TRUE THEN 1 FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "FROM"`, Position: "30"}},
			{"SELECT CASE WHEN TRUE THEN 1", referenceDiagnostic{Code: "42601", Message: `syntax error at end of input`, Position: "29"}},
			{"SELECT COALESCE(VARIADIC v) FROM t", referenceDiagnostic{Code: "42601", Message: `syntax error at or near "VARIADIC"`, Position: "17"}},
		} {
			step := referenceImplicitError(tc.q, tc.d)
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			step.errorStage = "prepare"
			p.step(t, referencePrepared(step))
		}
		for _, q := range []string{"SELECT CASE WHEN v=0 THEN 0 ELSE 1/0 END FROM t", "SELECT COALESCE(v,1/0) FROM t", "SELECT GREATEST(100,1/0) FROM t", "SELECT NULLIF(NULL,1/0) FROM t"} {
			step := referenceImplicitError(q, referenceDiagnostic{Code: "22012", Message: "division by zero"})
			step.selectRows = true
			step.errorStage = "execute"
			p.step(t, step)
			p.step(t, referencePrepared(step))
		}
	}
}
func TestReferenceConditionalConsumers(t *testing.T) {
	p := conditionalReferenceSetup(t)
	p.step(t, referenceExec("CREATE TABLE d(a INTEGER DEFAULT CASE WHEN TRUE THEN COALESCE(NULL,7) ELSE 1/0 END,b INTEGER DEFAULT NULLIF(2,3),c INTEGER DEFAULT GREATEST(NULL,4),e INTEGER DEFAULT LEAST(NULL,5))", 0))
	p.step(t, referenceExec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1))
	p.step(t, referenceSelect("SELECT a,b,c,e FROM d", []string{"a", "b", "c", "e"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(2), int64(4), int64(5)}}))
	p.step(t, referenceExec("CREATE TABLE out(v INTEGER)", 0))
	p.step(t, referenceExec("CREATE TABLE decorated(v BIGINT)", 0))
	p.step(t, referenceExec("INSERT INTO decorated VALUES(CASE WHEN TRUE THEN 1 ELSE 2147483648 END),(COALESCE(NULL,2147483648)) ORDER BY column1", 2))
	p.step(t, referenceExec("INSERT INTO decorated SELECT GREATEST(v,9) FROM t", 3))
	p.step(t, referenceSelect("SELECT v FROM decorated ORDER BY v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}, {int64(9)}, {int64(9)}, {int64(9)}, {int64(2147483648)}}))
	p.step(t, referenceExec("INSERT INTO out VALUES(COALESCE(NULL,1)),(NULLIF(2,3)),(GREATEST(NULL,4)),(LEAST(NULL,5)),(CASE WHEN FALSE THEN 1/0 ELSE 6 END)", 5))
	p.step(t, referenceExec("INSERT INTO out SELECT CASE WHEN b THEN 10 ELSE 20 END FROM t", 3))
	p.step(t, referenceExec("UPDATE t SET v=CASE WHEN b THEN 10 WHEN id=2 THEN 20 ELSE 30 END WHERE COALESCE(TRUE,FALSE)", 3))
	p.step(t, referenceSelect("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}, {int64(30)}}))
	p.step(t, referenceImplicitError("UPDATE t SET v=CASE WHEN id=1 THEN 9 ELSE 10/(id-2) END", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
	p.step(t, referenceSelect("SELECT v FROM t ORDER BY id", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}, {int64(20)}, {int64(30)}}))
	p.step(t, referenceExec("DELETE FROM t WHERE CASE t.b WHEN TRUE THEN TRUE ELSE FALSE END", 1))
	p.step(t, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
}

func TestReferenceConditionalSequenceDemand(t *testing.T) {
	p := conditionalReferenceSetup(t)
	p.step(t, referenceExec("CREATE SEQUENCE seq", 0))
	p.step(t, referencePrepared(referenceSelect("SELECT CASE nextval('seq') WHEN 1 THEN 10 WHEN 2 THEN 20 ELSE 30 END FROM t WHERE id=1", []string{"case"}, []string{"INT4"}, [][]any{{int64(10)}})))
	p.step(t, referenceSelect("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(2)}}))
	p.step(t, referenceSelect("SELECT CASE WHEN b THEN 7 ELSE nextval('seq') END,COALESCE(v,nextval('seq')) FROM t WHERE id=1", []string{"nextval", "coalesce"}, []string{"INT8", "INT8"}, [][]any{{int64(7), int64(0)}}))
	p.step(t, referenceSelect("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(3)}}))
	p.step(t, referenceSelect("SELECT CASE nextval('seq') WHEN 4 THEN CASE nextval('seq') WHEN 5 THEN 9 ELSE 0 END ELSE 0 END FROM t WHERE id=1", []string{"case"}, []string{"INT4"}, [][]any{{int64(9)}}))
	p.step(t, referenceSelect("SELECT nextval('seq')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(6)}}))
}

func TestReferenceConditionalDefaultDecisivePolicy(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE d(a BOOL DEFAULT CASE WHEN TRUE THEN FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,b BOOL DEFAULT CASE WHEN TRUE THEN TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1 ELSE FALSE END,c BOOL DEFAULT COALESCE(FALSE AND CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE),e BOOL DEFAULT COALESCE(TRUE OR CAST(CAST('oops' AS TEXT) AS INTEGER)=1,FALSE))", 0))
	insert := referenceExec("INSERT INTO d VALUES(DEFAULT,DEFAULT,DEFAULT,DEFAULT)", 1)
	p.step(t, insert)
	p.step(t, referencePrepared(insert))
	rows := referenceSelect("SELECT a,b,c,e FROM d", []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {false, true, false, true}})
	p.step(t, rows)
	p.step(t, referencePrepared(rows))
	p.step(t, referenceExec("CREATE TABLE src(i INT)", 0))
	empty := referenceExec("INSERT INTO d(a) SELECT FALSE FROM src", 0)
	p.step(t, empty)
	p.step(t, referencePrepared(empty))
	p.step(t, referenceExec("INSERT INTO src VALUES(1)", 1))
	consumed := referenceExec("INSERT INTO d(a) SELECT FALSE FROM src", 1)
	p.step(t, consumed)
	p.step(t, referencePrepared(consumed))
	p.step(t, referenceSelect("SELECT a,b,c,e FROM d", []string{"a", "b", "c", "e"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, false, true}, {false, true, false, true}, {false, true, false, true}, {false, true, false, true}}))
}
