package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckVolatileEffects(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE SEQUENCE s START 1", 0))
			run(fixture.Exec("CREATE TABLE t(a INT CHECK(nextval('s')>0),CHECK(random()>=0 AND random()<1))", 0))
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("INSERT INTO t VALUES(1),(2)", 2))
			run(fixture.Select("SELECT currval('s')", []string{"currval"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Select("SELECT last_value FROM s", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Exec("CREATE TABLE u(a INT CHECK(10/a>0))", 0))
			run(fixture.Exec("INSERT INTO u VALUES(1)", 1))
			run(fixture.ImplicitError("INSERT INTO u VALUES(2),(0)", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
			run(fixture.Select("SELECT a FROM u", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
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
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec(tc.ddl, 0))
					want := fixture.CheckViolation("t", tc.name, tc.values)
					if tc.name == "" {
						want = fixture.Diagnostic{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "t", Column: "a"}
					}
					run(fixture.ImplicitError("INSERT INTO t VALUES("+tc.values+")", want))
				})
			}
		})
	}
}
