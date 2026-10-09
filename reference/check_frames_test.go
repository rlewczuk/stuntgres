package reference_test

import (
	"fmt"
	"reflect"
	"testing"
)

func TestReferenceCheckCorePreparedFrames(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) { referenceCheckRun(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE TABLE t(a INT CONSTRAINT only_one CHECK(a=1) NO INHERIT)", 0))
			for _, q := range []string{"INSERT INTO t VALUES($1)", "UPDATE t SET a=$1"} {
				stmt, err := conn.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
				for _, v := range []any{1, 2, nil, 1} {
					s := referenceExec(q, 1, v)
					if q == "UPDATE t SET a=$1" {
						s.affected = 3
					}
					if v == 2 {
						s = referenceImplicitError(q, referenceCheckViolation("t", "only_one", "2"), v)
					}
					referenceCheckRun(t, p.ctx, held, b.recorder, s)
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				if q == "INSERT INTO t VALUES($1)" {
					s := referenceSelect("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}, {int64(1)}})
					s.prepared = true
					run(s)
					rows, closeRows, err := transactionQuery(p.ctx, conn, s.query, true)
					if err != nil {
						t.Fatal(err)
					}
					defer closeRows()
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 {
						t.Fatal(types, err)
					}
					wantScan := reflect.TypeFor[int64]()
					if b.pg {
						wantScan = reflect.TypeFor[int32]()
					}
					if types[0].ScanType() != wantScan {
						t.Fatal(types[0].ScanType(), wantScan)
					}
					nullable, known := types[0].Nullable()
					if known == b.pg || !b.pg && !nullable {
						t.Fatal(nullable, known)
					}
					n := 0
					for rows.Next() {
						var v any
						if err := rows.Scan(&v); err != nil {
							t.Fatal(err)
						}
						n++
					}
					if n != 3 || rows.Err() != nil {
						t.Fatal(n, rows.Err())
					}
					if err := closeRows(); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}

func TestReferenceCheckCompleteRowAndAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					run := func(s referenceStep) { s.prepared = prepared; referenceCheckRun(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE t(CONSTRAINT c CHECK(public.t.a<t.b),a INT DEFAULT 1,b INT)", 0))
					run(referenceExec("INSERT INTO t(b) VALUES(2)", 1))
					run(referenceExec("INSERT INTO t VALUES(DEFAULT,2)", 1))
					run(referenceExec("CREATE TABLE src(a INT,b INT)", 0))
					run(referenceExec("INSERT INTO src VALUES(1,2),(3,2),(1,2)", 3))
					for _, q := range []string{"INSERT INTO t VALUES(1,2),(3,2),(1,2)", "INSERT INTO t SELECT a,b FROM src", "INSERT INTO t VALUES(1,2),(3,2),(1,2) ORDER BY 1", "UPDATE t SET a=b,b=a"} {
						detail := "3, 2"
						if q == "UPDATE t SET a=b,b=a" {
							detail = "2, 1"
						}
						run(referenceImplicitError(q, referenceCheckViolation("t", "c", detail)))
						s := referenceSelect("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}})
						s.bag = true
						run(s)
					}
					run(referenceExec("UPDATE t SET a=2,b=3", 2))
					run(referenceExec("CREATE TABLE u(a INT CONSTRAINT c CHECK(a<3))", 0))
					run(referenceExec("INSERT INTO u VALUES(1),(2)", 2))
					run(referenceImplicitError("UPDATE u SET a=a+1", referenceCheckViolation("u", "c", "3")))
					run(referenceSelect("SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			}
		})
	}
}
