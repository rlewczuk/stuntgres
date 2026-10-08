//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseConstraintsReparse(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name string
		db   *sql.DB
		rec  *referenceRecorder
	}{{"postgres", p.postgres, p.postgresRecord}, {"memory", p.memory, p.memoryRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) referenceObserved { return referenceRunStep(t, p.ctx, b.db, b.rec, s) }
			run(referenceExec("CREATE TABLE original(a INT,CONSTRAINT ck CHECK(a::bigint>0))", 0))
			for _, mode := range []struct {
				pretty bool
				want   string
			}{{false, "CHECK (((a)::bigint > 0))"}, {true, "CHECK (a::bigint > 0)"}} {
				observed := run(referencePrepared(referenceSelect("SELECT pg_get_constraintdef(oid,$1) AS d FROM pg_catalog.pg_constraint WHERE conname='ck'", []string{"d"}, []string{"TEXT"}, [][]any{{mode.want}}, mode.pretty)))
				// Consume the observed return, after independently checking its golden.
				suffix := observed.rows[0][0].(string)
				name := fmt.Sprintf("copy_%t", mode.pretty)
				run(referenceExec("CREATE TABLE "+name+"(a INT,CONSTRAINT copied "+suffix+")", 0))
				for _, prepared := range []bool{false, true} {
					good := referenceExec("INSERT INTO "+name+" VALUES($1)", 1, 1)
					good.prepared = prepared
					run(good)
					null := referenceExec("INSERT INTO "+name+" VALUES($1)", 1, nil)
					null.prepared = prepared
					run(null)
					for _, value := range []int{0, -1} {
						bad := referenceImplicitError("INSERT INTO "+name+" VALUES($1)", referenceCheckViolation(name, "copied", fmt.Sprint(value)), value)
						bad.prepared = prepared
						run(bad)
					}
				}
				run(referenceImplicitError("INSERT INTO "+name+" VALUES(1),(0),(1)", referenceCheckViolation(name, "copied", "0")))
				run(referenceImplicitError("UPDATE "+name+" SET a=-1", referenceCheckViolation(name, "copied", "-1")))
				run(referenceSelect("SELECT a FROM "+name+" ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {nil}, {int64(1)}, {int64(1)}}))
			}
		})
	}
}

func TestReferenceDeparseConstraintsStableAttributes(t *testing.T) {
	referenceCheckBackends(t, func(t *testing.T, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE original(prefix TEXT,a INT,b INT,CONSTRAINT c CHECK(b>0))", 0))
		run(referenceExec("ALTER TABLE original DROP prefix", 0))
		run(referenceExec("CREATE TABLE supplied(x TEXT,y TEXT,z TEXT)", 0))
		run(referenceSelect("SELECT pg_get_expr(conbin,'supplied'::regclass) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"(z > 0)"}}))
		run(referenceExec("CREATE TABLE wrongshape(q TEXT)", 0))
		run(referenceImplicitError("SELECT pg_get_expr(conbin,'wrongshape'::regclass) FROM pg_catalog.pg_constraint WHERE conname='c'", referenceDiagnostic{Code: "XX000", Message: `invalid attnum 3 for relation "wrongshape"`}))
		run(referenceExec("ALTER TABLE original RENAME b TO renamed", 0))
		run(referenceSelect("SELECT pg_get_constraintdef(oid,true) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (renamed > 0)"}}))
	})
}
