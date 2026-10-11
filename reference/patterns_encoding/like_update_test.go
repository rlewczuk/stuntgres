package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLikeUpdate(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			db := backend.DB
			execute := func(q string, affected int64) {
				fixture.RunStep(t, p.Ctx, db, backend.Recorder, fixture.Exec(q, affected))
			}
			check := func(rows *sql.Rows, err error, name string, want [][]any) {
				if err != nil {
					t.Fatal(err)
				}
				fixture.UpdateCoreRetained(t, backend.PG, rows, []string{name}, []string{"INT4"}, want, false)
			}

			execute("CREATE TABLE t(id INT,v TEXT,p TEXT,e TEXT,mark INT)", 0)
			execute("INSERT INTO t VALUES(1,'a%b','a!%b','!',0),(2,'Ab','a%','!',0),(3,NULL,NULL,NULL,0)", 3)
			for _, tc := range []struct {
				predicate string
				args      []any
				count     int64
				marks     [][]any
			}{
				{"v LIKE 'a%'", nil, 1, [][]any{{int64(1)}, {int64(0)}, {int64(0)}}},
				{"v ILIKE $1", []any{"a%"}, 2, [][]any{{int64(1)}, {int64(1)}, {int64(0)}}},
				{"v NOT LIKE 'a%'", nil, 1, [][]any{{int64(0)}, {int64(1)}, {int64(0)}}},
				{"v NOT ILIKE 'a%'", nil, 0, [][]any{{int64(0)}, {int64(0)}, {int64(0)}}},
				{"v LIKE p ESCAPE e", nil, 1, [][]any{{int64(1)}, {int64(0)}, {int64(0)}}},
				{"v ILIKE p ESCAPE e", nil, 2, [][]any{{int64(1)}, {int64(1)}, {int64(0)}}},
			} {
				t.Run(tc.predicate, func(t *testing.T) {
					q := "UPDATE t SET mark=1 WHERE " + tc.predicate
					stmt, err := db.Prepare(q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					for _, prepared := range []bool{false, true} {
						execute("UPDATE t SET mark=0", 3)
						var count int64
						if prepared {
							r, err := stmt.Exec(tc.args...)
							if err != nil {
								t.Fatal(err)
							}
							if r == nil {
								t.Fatal("successful held UPDATE exposed no result")
							}
							if _, err := r.LastInsertId(); err == nil {
								t.Fatal("held UPDATE supported LastInsertId")
							}
							count, err = r.RowsAffected()
							if err != nil {
								t.Fatal(err)
							}
						} else {
							r, err := db.Exec(q, tc.args...)
							if err != nil {
								t.Fatal(err)
							}
							if r == nil {
								t.Fatal("successful direct UPDATE exposed no result")
							}
							if _, err := r.LastInsertId(); err == nil {
								t.Fatal("direct UPDATE supported LastInsertId")
							}
							count, err = r.RowsAffected()
							if err != nil {
								t.Fatal(err)
							}
						}
						if count != tc.count {
							t.Fatal(count, tc.count)
						}
						rows, err := db.Query("SELECT mark FROM t ORDER BY id")
						check(rows, err, "mark", tc.marks)
						selected := make([][]any, 0)
						for i, row := range tc.marks {
							if row[0] == int64(1) {
								selected = append(selected, []any{int64(i + 1)})
							}
						}
						query := "SELECT id FROM t WHERE " + tc.predicate + " ORDER BY id"
						rows, err = db.Query(query, tc.args...)
						check(rows, err, "id", selected)
						held, err := db.Prepare(query)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						rows, err = held.Query(tc.args...)
						check(rows, err, "id", selected)
					}
				})
			}
			execute("UPDATE t SET mark=0", 3)
			execute("UPDATE t SET e='ab' WHERE id=2", 1)
			query := "UPDATE t SET mark=1 WHERE v ILIKE p ESCAPE e"
			stmt, err := db.Prepare(query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := db.Exec(query)
			if result != nil {
				t.Fatal("failed direct UPDATE exposed a result")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
			rows, err := db.Query("SELECT mark FROM t ORDER BY id")
			check(rows, err, "mark", [][]any{{int64(0)}, {int64(0)}, {int64(0)}})
			result, err = stmt.Exec()
			if result != nil {
				t.Fatal("failed held UPDATE exposed a result")
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22025", Message: "invalid escape string", Hint: "Escape string must be empty or one character."})
			rows, err = db.Query("SELECT mark FROM t ORDER BY id")
			check(rows, err, "mark", [][]any{{int64(0)}, {int64(0)}, {int64(0)}})
		})
	}
}
