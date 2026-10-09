package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDeparseConstraintsReparse(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) referenceObserved { return referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
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
	deparseReviewStreams(t, func(t *testing.T, conn *sql.Conn, pg bool, run func(referenceStep) referenceObserved) {
		run(referenceExec("CREATE TABLE original(prefix TEXT,a INT,b INT DEFAULT 7,CONSTRAINT c CHECK(b>0))", 0))
		cid := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='c'")
		did := deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef")
		var saved *sql.Rows
		if !pg {
			var err error
			saved, err = conn.QueryContext(t.Context(), "SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'")
			if err != nil {
				t.Fatal(err)
			}
			defer saved.Close()
		} else {
			run(referenceSelect("SELECT pg_get_constraintdef(oid,true) FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{"CHECK (b > 0)"}}))
		}
		run(referenceExec("ALTER TABLE original DROP prefix", 0))
		run(referenceSelect("SELECT adnum FROM pg_catalog.pg_attrdef", []string{"adnum"}, []string{"INT2"}, [][]any{{int64(3)}}))
		if deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_attrdef") != did || deparseDefinitionID(t, conn, "SELECT oid FROM pg_catalog.pg_constraint WHERE conname='c'") != cid {
			t.Fatal("remap changed definition identity")
		}
		run(referenceExec("CREATE TABLE supplied(x TEXT,y TEXT,z TEXT)", 0))
		run(referenceSelect("SELECT pg_get_expr(conbin,'supplied'::regclass) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", []string{"d"}, []string{"TEXT"}, [][]any{{"(z > 0)"}}))
		run(referenceExec("CREATE TABLE wrongshape(q TEXT)", 0))
		failure := referenceImplicitError("SELECT pg_get_expr(conbin,'wrongshape'::regclass) FROM pg_catalog.pg_constraint WHERE conname='c'", referenceDiagnostic{Code: "XX000", Message: `invalid attnum 3 for relation "wrongshape"`})
		failure.selectRows = true
		run(failure)
		run(referenceExec("ALTER TABLE original RENAME b TO renamed", 0))
		run(referenceSelect("SELECT pg_get_constraintdef($1,true) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{"CHECK (renamed > 0)"}}, cid))
		run(referenceExec("DROP TABLE original", 0))
		run(referenceSelect("SELECT pg_get_constraintdef($1) AS d", []string{"d"}, []string{"TEXT"}, [][]any{{nil}}, cid))
		if saved != nil {
			if !saved.Next() {
				t.Fatal(saved.Err())
			}
			var old string
			if err := saved.Scan(&old); err != nil || old != "CHECK (b > 0)" {
				t.Fatal(old, err)
			}
			if saved.Next() || saved.Err() != nil {
				t.Fatal(saved.Err())
			}
			if err := saved.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
}
