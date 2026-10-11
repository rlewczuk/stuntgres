package reference_test

import (
	"bytes"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmokeCheckSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			m, db, ctx, _ := fixture.MigrationAlterFixture(t, p, backend)
			exec := func(q string, n int64) {
				s := fixture.Exec(q, n)
				s.NonContext = true
				fixture.RunStep(t, ctx, db, m.Recorder, s)
			}
			exec("CREATE TABLE t(a INT CONSTRAINT only_one CHECK(a=1) NO INHERIT)", 0)
			exec("INSERT INTO t VALUES(1)", 1)
			stmt, err := db.Prepare("INSERT INTO t VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: db, Statement: stmt, SQL: "INSERT INTO t VALUES($1)"}
			checkFailure := stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "only_one"`, Detail: "Failing row contains (2).", Schema: "public", Table: "t", Constraint: "only_one"}
			for _, v := range []any{nil, 2, 1} {
				r, e := held.Exec(held.SQL, v)
				if v == 2 {
					if r != nil {
						t.Fatal(r)
					}
					m.NullDiagnostic(e, checkFailure)
				} else {
					if e != nil {
						t.Fatal(e)
					}
					n, e := r.RowsAffected()
					if e != nil || n != 1 {
						t.Fatal(n, e)
					}
				}
			}
			want := [][]any{{nil}, {int64(1)}, {int64(1)}}
			for _, q := range []string{"INSERT INTO t VALUES(1),(2),(1)", "UPDATE t SET a=2"} {
				r, e := db.Exec(q)
				if r != nil {
					t.Fatal(r)
				}
				m.NullDiagnostic(e, checkFailure)
				m.NullQuery(db, "SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, []bool{true}, want)
			}
			exec("CREATE TABLE u(a INT CONSTRAINT c CHECK(a<3))", 0)
			exec("INSERT INTO u VALUES(1),(2)", 2)
			_, err = db.Exec("UPDATE u SET a=a+1")
			m.NullDiagnostic(err, stuntgres.Error{Code: "23514", Message: `new row for relation "u" violates check constraint "c"`, Detail: "Failing row contains (3).", Schema: "public", Table: "u", Constraint: "c"})
			m.NullQuery(db, "SELECT a FROM u ORDER BY a", []string{"a"}, []string{"INT4"}, []bool{true}, [][]any{{int64(1)}, {int64(2)}})
			_, err = db.Exec("CREATE TABLE bad(a INT CHECK(a))")
			m.NullDiagnostic(err, stuntgres.Error{Code: "42804", Message: "argument of CHECK must be type boolean, not type integer", Position: "30"})
			exec("CREATE TABLE bad(a INT)", 0)
			if !backend.PG {
				var dump bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(dump.String(), `CONSTRAINT "only_one" CHECK`) || !strings.Contains(dump.String(), "NO INHERIT") {
					t.Fatal(dump.String())
				}
				if err = stuntgres.Reset(ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err = stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				m.NullQuery(db, "SELECT a FROM t ORDER BY a NULLS FIRST", []string{"a"}, []string{"INT4"}, []bool{true}, want)
				_, err = db.Exec("INSERT INTO t VALUES(2)")
				m.NullDiagnostic(err, checkFailure)
				var again bytes.Buffer
				if err = stuntgres.Dump(ctx, p.Native, &again); err != nil || again.String() != dump.String() {
					t.Fatal(err, again.String())
				}
			}
		})
	}
}
