package reference_test

import (
	"bytes"
	"stuntgres"
	"testing"
)

func TestReferenceCheckVolatileEffects(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE SEQUENCE s START 1", 0))
			run(referenceExec("CREATE TABLE t(a INT CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))", 0))
			if !b.pg {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
					t.Fatal(err)
				}
			}
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			run(referenceSelect("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceSelect("SELECT last_value FROM s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(referenceExec("CREATE TABLE u(a INT CHECK(10/a>0))", 0))
			run(referenceExec("INSERT INTO u VALUES(1)", 1))
			run(referenceImplicitError("INSERT INTO u VALUES(2),(0)", referenceDiagnostic{Code: "22012", Message: "division by zero"}))
			run(referenceSelect("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceCheckNames(t *testing.T) {
	for _, tc := range []struct{ ddl, name, values string }{
		{"CREATE TABLE t(a INT CONSTRAINT z CHECK(FALSE),CONSTRAINT a CHECK(FALSE))", "a", "1"},
		{"CREATE TABLE t(a INT,b INT CHECK(a+a>0))", "t_a_check", "0, 1"},
		{"CREATE TABLE t(a INT,b INT,CHECK(a<b))", "t_check", "2, 1"},
		{"CREATE TABLE t(a INT CONSTRAINT t_a_check CHECK(a>0),CHECK(a>1))", "t_a_check1", "1"},
		{"CREATE TABLE t(a INT CHECK(a>0),CHECK(a>1),CHECK(a>2))", "t_a_check2", "2"},
		{"CREATE TABLE t(a INT NOT NULL,CHECK(FALSE))", "", "NULL"},
	} {
		t.Run(tc.ddl, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec(tc.ddl, 0))
					want := referenceCheckViolation("t", tc.name, tc.values)
					if tc.name == "" {
						want = referenceDiagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}
					}
					run(referenceImplicitError("INSERT INTO t VALUES("+tc.values+")", want))
				})
			}
		})
	}
}
