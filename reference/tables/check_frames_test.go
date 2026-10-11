package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCheckCorePreparedFrames(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s fixture.Step) { fixture.CheckRun(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(a INT CONSTRAINT only_one CHECK(a=1) NO INHERIT)", 0))
			for _, q := range []string{"INSERT INTO t VALUES($1)", "UPDATE t SET a=$1"} {
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
				for _, v := range []any{1, 2, nil, 1} {
					s := fixture.Exec(q, 1, v)
					if q == "UPDATE t SET a=$1" {
						s.Affected = 3
					}
					if v == 2 {
						s = fixture.ImplicitError(q, fixture.CheckViolation("t", "only_one", "2"), v)
					}
					fixture.CheckRun(t, p.Ctx, held, b.Recorder, s)
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
				if q == "INSERT INTO t VALUES($1)" {
					s := fixture.Select("SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}, {int64(1)}})
					s.Prepared = true
					run(s)
					rows, closeRows, err := fixture.TransactionQuery(p.Ctx, conn, s.Query, true)
					if err != nil {
						t.Fatal(err)
					}
					defer closeRows()
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 {
						t.Fatal(types, err)
					}
					wantScan := reflect.TypeFor[int64]()
					if b.PG {
						wantScan = reflect.TypeFor[int32]()
					}
					if types[0].ScanType() != wantScan {
						t.Fatal(types[0].ScanType(), wantScan)
					}
					nullable, known := types[0].Nullable()
					if known == b.PG || !b.PG && !nullable {
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
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.CheckRun(t, p.Ctx, b.DB, b.Recorder, s) }
					run(fixture.Exec("CREATE TABLE t(CONSTRAINT c CHECK(public.t.a<t.b),a INT DEFAULT 1,b INT)", 0))
					run(fixture.Exec("INSERT INTO t(b) VALUES(2)", 1))
					run(fixture.Exec("INSERT INTO t VALUES(DEFAULT,2)", 1))
					run(fixture.Exec("CREATE TABLE src(a INT,b INT)", 0))
					run(fixture.Exec("INSERT INTO src VALUES(1,2),(3,2),(1,2)", 3))
					for _, q := range []string{"INSERT INTO t VALUES(1,2),(3,2),(1,2)", "INSERT INTO t SELECT a,b FROM src", "INSERT INTO t VALUES(1,2),(3,2),(1,2) ORDER BY 1", "UPDATE t SET a=b,b=a"} {
						detail := "3, 2"
						if q == "UPDATE t SET a=b,b=a" {
							detail = "2, 1"
						}
						run(fixture.ImplicitError(q, fixture.CheckViolation("t", "c", detail)))
						s := fixture.Select("SELECT * FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}})
						s.Bag = true
						run(s)
					}
					run(fixture.Exec("UPDATE t SET a=2,b=3", 2))
					run(fixture.Exec("CREATE TABLE u(a INT CONSTRAINT c CHECK(a<3))", 0))
					run(fixture.Exec("INSERT INTO u VALUES(1),(2)", 2))
					run(fixture.ImplicitError("UPDATE u SET a=a+1", fixture.CheckViolation("u", "c", "3")))
					run(fixture.Select("SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				})
			}
		})
	}
}
