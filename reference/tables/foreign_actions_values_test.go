package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceForeignActionsValues(t *testing.T) {
	for _, direction := range []string{"DELETE", "UPDATE"} {
		for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/prepared=%t", direction, action, prepared), func(t *testing.T) {
					foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
						run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", 0))
						run(fixture.Exec("CREATE TABLE c(id INT PRIMARY KEY,pid INT DEFAULT 1 CONSTRAINT fk REFERENCES p ON "+direction+" "+action+",note TEXT)", 0))
						run(fixture.Exec("INSERT INTO p VALUES(1,'other'),(2,'parent'),(9,'no child')", 3))
						run(fixture.Exec("INSERT INTO c VALUES(10,2,'kept'),(11,2,'second'),(12,NULL,'null'),(13,1,'other')", 4))
						run(foreignActionsExec("UPDATE p SET id=id,note=note WHERE id=2", 1, prepared))
						run(foreignActionsExec("UPDATE p SET note='changed' WHERE id=2", 1, prepared))
						q := "DELETE FROM p WHERE id=2"
						free := "DELETE FROM p WHERE id=9"
						if direction == "UPDATE" {
							q = "UPDATE p SET id=3 WHERE id=2"
							free = "UPDATE p SET id=19 WHERE id=9"
						}
						parent := [][]any{{int64(1), "other"}, {int64(2), "changed"}}
						child := [][]any{{int64(10), int64(2), "kept"}, {int64(11), int64(2), "second"}, {int64(12), nil, "null"}, {int64(13), int64(1), "other"}}
						if action == "NO ACTION" || action == "RESTRICT" {
							d := fixture.ForeignKeyParent("p", "c", "fk", `Key (id)=(2) is still referenced from table "c".`)
							if action == "RESTRICT" {
								d = foreignActionsRestrict("p", "c", "fk", `Key (id)=(2) is referenced from table "c".`)
							}
							run(fixture.ForeignKeyFail(q, d, prepared))
						} else {
							run(foreignActionsExec(q, 1, prepared))
							parent = parent[:1]
							if direction == "UPDATE" {
								parent = append(parent, []any{int64(3), "changed"})
							}
							switch action {
							case "CASCADE":
								if direction == "DELETE" {
									child = child[2:]
								} else {
									child[0][1], child[1][1] = int64(3), int64(3)
								}
							case "SET NULL":
								child[0][1], child[1][1] = nil, nil
							case "SET DEFAULT":
								child[0][1], child[1][1] = int64(1), int64(1)
							}
						}
						run(foreignActionsExec(free, 1, prepared))
						if direction == "UPDATE" {
							parent = append(parent, []any{int64(19), "no child"})
						}
						run(fixture.Select("SELECT id,note FROM p ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, parent))
						run(fixture.Select("SELECT id,pid,note FROM c ORDER BY id", []string{"id", "pid", "note"}, []string{"INT4", "INT4", "TEXT"}, child))
					})
				})
			}
		}
	}
}

func TestReferenceForeignActionsValuesComposite(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(a INT,b TEXT,PRIMARY KEY(a,b))", 0))
				run(fixture.Exec("CREATE TABLE c(b TEXT,a INT,note TEXT,CONSTRAINT fk FOREIGN KEY(b,a) REFERENCES p(b,a) ON UPDATE CASCADE ON DELETE CASCADE)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(1,'x'),(2,'y')", 2))
				run(fixture.Exec("INSERT INTO c VALUES('x',1,'pair'),('y',2,'other'),('x',NULL,'null')", 3))
				run(foreignActionsExec("UPDATE p SET a=3,b='z' WHERE a=1", 1, prepared))
				run(fixture.Select("SELECT b,a,note FROM c ORDER BY note", []string{"b", "a", "note"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"x", nil, "null"}, {"y", int64(2), "other"}, {"z", int64(3), "pair"}}))
				run(foreignActionsExec("DELETE FROM p WHERE a=3", 1, prepared))
				run(fixture.Select("SELECT b,a,note FROM c ORDER BY note", []string{"b", "a", "note"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"x", nil, "null"}, {"y", int64(2), "other"}}))
			})
		})
	}
}

func TestReferenceForeignActionsValuesProviders(t *testing.T) {
	for _, tc := range []struct{ parent, child, old, next, expected, typ string }{
		{"TEXT", "VARCHAR(8)", "'x'", "'next'", "next", "VARCHAR"},
		{"CHAR(4)", "CHAR(4)", "'x'", "'y'", "y   ", "BPCHAR"},
		{"BIGINT", "INT", "1", "2", "2", "INT4"},
		{"NUMERIC", "NUMERIC", "1.0", "2.00", "2.00", "NUMERIC"},
	} {
		t.Run(tc.parent+"/"+tc.child, func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id "+tc.parent+" PRIMARY KEY)", 0))
				run(fixture.Exec("CREATE TABLE c(pid "+tc.child+" CONSTRAINT fk REFERENCES p ON UPDATE CASCADE)", 0))
				run(fixture.Exec("INSERT INTO p VALUES("+tc.old+")", 1))
				run(fixture.Exec("INSERT INTO c VALUES("+tc.old+")", 1))
				run(fixture.Prepared(fixture.Exec("UPDATE p SET id="+tc.next, 1)))
				var value any = tc.expected
				if tc.typ == "INT4" {
					value = int64(2)
				}
				run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{tc.typ}, [][]any{{value}}))
			})
		})
	}
	foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
		run(fixture.Exec("CREATE DOMAIN positive AS INT", 0))
		run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE c(pid positive REFERENCES p ON UPDATE CASCADE)", 0))
		run(fixture.Exec("INSERT INTO p VALUES(1)", 1))
		run(fixture.Exec("INSERT INTO c VALUES(1)", 1))
		run(fixture.Exec("UPDATE p SET id=2", 1))
		run(foreignKeyIntRows("SELECT pid::int AS pid FROM c", "pid", 2))
	})
}

func TestReferenceForeignActionsEquality(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT", "CASCADE", "SET NULL", "SET DEFAULT"} {
		for _, prepared := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/prepared=%t", action, prepared), func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id NUMERIC PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid NUMERIC DEFAULT 2.00 CONSTRAINT fk REFERENCES p ON UPDATE "+action+")", 0))
					run(fixture.Exec("INSERT INTO p VALUES(1.0),(2.00)", 2))
					run(fixture.Exec("INSERT INTO c VALUES(1.0)", 1))
					parent, child := "1.00", any("1.0")
					q := "UPDATE p SET id=1.00 WHERE id=1"
					if action == "RESTRICT" {
						run(fixture.ForeignKeyFail(q, foreignActionsRestrict("p", "c", "fk", `Key (id)=(1.0) is referenced from table "c".`), prepared))
						parent = "1.0"
					} else {
						run(foreignActionsExec(q, 1, prepared))
						if action == "CASCADE" {
							child = "1.00"
						}
						if action == "SET NULL" {
							child = nil
						}
						if action == "SET DEFAULT" {
							child = "2.00"
						}
					}
					run(foreignActionsExec("UPDATE p SET id="+parent+" WHERE id=1", 1, prepared))
					run(fixture.Select("SELECT id FROM p ORDER BY id", []string{"id"}, []string{"NUMERIC"}, [][]any{{parent}, {"2.00"}}))
					run(fixture.Select("SELECT pid FROM c", []string{"pid"}, []string{"NUMERIC"}, [][]any{{child}}))
				})
			})
		}
	}
}

func TestReferenceForeignActionsEqualityReplacement(t *testing.T) {
	for _, action := range []string{"NO ACTION", "RESTRICT"} {
		foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
			run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY)", 0))
			run(fixture.Exec("CREATE TABLE c(pid INT CONSTRAINT fk REFERENCES p ON UPDATE "+action+")", 0))
			// Release foreign_key.sql inserts descending physical keys so ordinary
			// immediate uniqueness is legal, without introducing DEFERRABLE UNIQUE.
			run(fixture.Exec("INSERT INTO p VALUES(12),(11)", 2))
			run(fixture.Exec("UPDATE p SET id=id+1", 2))
			run(fixture.Exec("INSERT INTO c VALUES(13)", 1))
			if action == "NO ACTION" {
				run(fixture.Prepared(fixture.Exec("UPDATE p SET id=id+1", 2)))
				run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 13, 14))
				run(fixture.ForeignKeyFail("UPDATE p SET id=id+1", fixture.ForeignKeyParent("p", "c", "fk", `Key (id)=(13) is still referenced from table "c".`), false))
			} else {
				run(fixture.ForeignKeyFail("UPDATE p SET id=id+1", foreignActionsRestrict("p", "c", "fk", `Key (id)=(13) is referenced from table "c".`), true))
				run(foreignKeyIntRows("SELECT id FROM p ORDER BY id", "id", 12, 13))
			}
			run(foreignKeyIntRows("SELECT pid FROM c", "pid", 13))
		})
	}
}

func TestReferenceForeignActionsEqualityImages(t *testing.T) {
	for _, tc := range []struct{ typ, old, next, output string }{
		{"DOUBLE PRECISION", "'0'", "'-0'", "-0"},
		{"INTERVAL", "'1 day'", "'24 hours'", "24:00:00"},
	} {
		for _, action := range []string{"CASCADE", "RESTRICT"} {
			t.Run(tc.typ+"/"+action, func(t *testing.T) {
				foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
					run(fixture.Exec("CREATE TABLE p(id "+tc.typ+" PRIMARY KEY)", 0))
					run(fixture.Exec("CREATE TABLE c(pid "+tc.typ+" CONSTRAINT fk REFERENCES p ON UPDATE "+action+")", 0))
					run(fixture.Exec("INSERT INTO p VALUES("+tc.old+")", 1))
					run(fixture.Exec("INSERT INTO c VALUES("+tc.old+")", 1))
					q := "UPDATE p SET id=" + tc.next
					if action == "RESTRICT" {
						oldText := "0"
						if tc.typ == "INTERVAL" {
							oldText = "1 day"
						}
						run(fixture.ForeignKeyFail(q, foreignActionsRestrict("p", "c", "fk", "Key (id)=("+oldText+") is referenced from table \"c\"."), true))
					} else {
						run(fixture.Prepared(fixture.Exec(q, 1)))
						run(fixture.Select("SELECT pid::text AS image FROM c", []string{"image"}, []string{"TEXT"}, [][]any{{tc.output}}))
					}
				})
			})
		}
	}
}

func foreignActionsMetadata(t *testing.T, pg bool, rows *sql.Rows) {
	t.Helper()
	cols, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cols) != 2 {
		t.Fatal(cols)
	}
	for i, c := range cols {
		if pg {
			if _, ok := c.Nullable(); ok {
				t.Fatal("pq nullability unexpectedly known")
			}
		} else {
			if v, ok := c.Nullable(); !ok || v != (i == 1) {
				t.Fatal("target nullability", i, v, ok)
			}
		}
		length, known := c.Length()
		wantLength := int64(0)
		if i == 1 {
			wantLength = 9223372036854775807
		}
		if length != wantLength || known != (i == 1) {
			t.Fatal("target length metadata", i, length, known, wantLength)
		}
		want := reflect.TypeOf(int64(0))
		if i == 1 {
			want = reflect.TypeOf("")
		}
		if pg && i == 0 {
			want = reflect.TypeOf(int32(0))
		}
		if c.ScanType() != want {
			t.Fatal("ScanType", c.ScanType(), want)
		}
	}
}

func TestReferenceForeignActionsMetadata(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			foreignActionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
				run(fixture.Exec("CREATE TABLE p(id INT PRIMARY KEY,note TEXT)", 0))
				run(fixture.Exec("CREATE TABLE c(pid INT REFERENCES p ON UPDATE CASCADE ON DELETE CASCADE,note TEXT)", 0))
				run(fixture.Exec("INSERT INTO p VALUES(2,'parent')", 1))
				run(fixture.Exec("INSERT INTO c VALUES(2,'first'),(2,'second')", 2))
				for _, q := range []string{"UPDATE p SET id=3 WHERE id=2 RETURNING id,note", "DELETE FROM p WHERE id=3 RETURNING id,note", "DELETE FROM p WHERE id=99 RETURNING id,note"} {
					var want [][]any
					if q != "DELETE FROM p WHERE id=99 RETURNING id,note" {
						want = [][]any{{int64(3), "parent"}}
					}
					s := fixture.Select(q, []string{"id", "note"}, []string{"INT4", "TEXT"}, want)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) { foreignActionsMetadata(t, b.PG, r) }
					s.CheckScan = func(t *testing.T, r *sql.Rows, row int) {
						var id sql.NullInt64
						var note sql.NullString
						if err := r.Scan(&id, &note); err != nil || id != (sql.NullInt64{Int64: 3, Valid: true}) || note != (sql.NullString{String: "parent", Valid: true}) {
							t.Fatal(id, note, err)
						}
					}
					run(s)
					if q[0] == 'U' {
						run(fixture.Select("SELECT pid,note FROM c ORDER BY note", []string{"pid", "note"}, []string{"INT4", "TEXT"}, [][]any{{int64(3), "first"}, {int64(3), "second"}}))
					}
				}
				run(fixture.Select("SELECT pid,note FROM c", []string{"pid", "note"}, []string{"INT4", "TEXT"}, nil))
			})
		})
	}
}
