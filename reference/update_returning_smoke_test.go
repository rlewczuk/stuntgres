package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceUpdateReturningMigratedSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			db := backend.db
			var err error
			check := func(rows *sql.Rows, err error, names, types []string, want [][]any) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range want {
					for i, value := range row {
						row[i] = referenceNormalize(value)
					}
				}
				updateCoreRetained(t, backend.pg, rows, names, types, want, true)
			}
			for _, q := range []string{"CREATE TABLE t(id INT NOT NULL,a INT,b INT,v TEXT)", "INSERT INTO t VALUES(1,10,20,'one'),(2,30,40,NULL),(2,30,40,NULL)", "CREATE SEQUENCE s START 101 CACHE 1"} {
				if _, err = db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := db.Query("UPDATE t SET a=b,b=a WHERE id=2 RETURNING id,a,b,old.a,new.a,new.a-old.a AS delta,v")
			check(rows, err, []string{"id", "a", "b", "a", "a", "delta", "v"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "TEXT"}, [][]any{{int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}, {int64(2), int64(40), int64(30), int64(30), int64(40), int64(10), nil}})
			held, err := db.Prepare("UPDATE t SET a=a+$1 WHERE id=$2 RETURNING WITH (OLD AS o,NEW AS n) o.a,n.a")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			rows, err = held.Query(1, 1)
			check(rows, err, []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(11)}})
			rows, err = held.Query(1, 99)
			check(rows, err, []string{"a", "a"}, []string{"INT4", "INT4"}, [][]any{})
			result, err := db.Exec("UPDATE t SET a=a RETURNING nextval('s')")
			if err != nil {
				t.Fatal(err)
			}
			if n, e := result.RowsAffected(); e != nil || n != 3 {
				t.Fatal(n, e)
			}
			var next int64
			if err = db.QueryRow("SELECT nextval('s')").Scan(&next); err != nil || next != 104 {
				t.Fatal(next, err)
			}
			for _, q := range []string{"CREATE TABLE bad(id INT,a INT,s TEXT)", "INSERT INTO bad VALUES(1,10,'7'),(2,20,'bad')"} {
				if _, err = db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			rows, err = db.Query("UPDATE bad SET a=a+1 RETURNING CAST(s AS INT)")
			if rows != nil {
				if !backend.pg {
					rows.Close()
					t.Fatal("failed output exposed cursor")
				}
				for rows.Next() {
					var value any
					if scanErr := rows.Scan(&value); scanErr != nil {
						rows.Close()
						t.Fatal(scanErr)
					}
				}
				err = rows.Err()
				rows.Close()
			}
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
			rows, err = db.Query("SELECT id,a FROM bad ORDER BY id")
			check(rows, err, []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}})
			for _, tc := range []struct {
				q string
				d referenceDiagnostic
			}{
				{"UPDATE t SET id=$1 RETURNING $1", referenceDiagnostic{Code: "42804", Message: `column "id" is of type integer but expression is of type text`, Hint: "You will need to rewrite or cast the expression.", Position: "17"}},
				{"UPDATE t SET id=1 RETURNING $1,$1::int4", referenceDiagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "integer versus text", Position: "29"}},
				{"UPDATE t SET id=1 RETURNING sum(id)", referenceDiagnostic{Code: "42803", Message: "aggregate functions are not allowed in RETURNING", Position: "29"}},
				{"UPDATE t SET id=1 RETURNING sum(zzzzz)", referenceDiagnostic{Code: "42703", Message: `column "zzzzz" does not exist`, Position: "33"}},
				{"UPDATE t SET id=1 RETURNING sum(v)", referenceDiagnostic{Code: "42883", Message: "function sum(text) does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: "29"}},
			} {
				tc.d.Severity, tc.d.SeverityUnlocalized = "ERROR", "ERROR"
				stmt, e := db.Prepare(tc.q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("invalid prepared handle")
				}
				referenceAssertDiagnostic(t, e, &tc.d)
			}
			insert := "INSERT INTO t VALUES(9,1,2,NULL) RETURNING id"
			if backend.pg {
				referenceRunStep(t, p.ctx, db, backend.recorder, referenceSelect(insert, []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
			} else if result, err := db.ExecContext(p.ctx, insert); err == nil || result != nil {
				t.Fatal("excluded INSERT RETURNING", result, err)
			}
			for _, q := range []string{"UPDATE t x SET a=1 RETURNING x.a", "UPDATE t AS x SET a=1 RETURNING x.a"} {
				count := 3
				if backend.pg {
					count = 4
				}
				want := make([][]any, count)
				for i := range want {
					want[i] = []any{int64(1)}
				}
				referenceRunStep(t, p.ctx, db, backend.recorder, func() referenceStep {
					s := referenceSelect(q, []string{"a"}, []string{"INT4"}, want)
					s.bag = true
					return s
				}())
			}
		})
	}
}
