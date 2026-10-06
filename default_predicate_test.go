package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestDefaultPredicateControls(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			expression string
			want       bool
		}{
			{"NULL IS NULL", true}, {"1 IS NULL", false}, {"NULL IS NOT NULL", false}, {"1 IS NOT NULL", true},
			{"NULL IS TRUE", false}, {"TRUE IS TRUE", true}, {"FALSE IS NOT TRUE", true},
			{"NULL IS FALSE", false}, {"FALSE IS FALSE", true}, {"TRUE IS NOT FALSE", true},
			{"NULL IS UNKNOWN", true}, {"TRUE IS NOT UNKNOWN", true},
			{"NULL IS NOT TRUE", true}, {"NULL IS NOT FALSE", true}, {"NULL IS NOT UNKNOWN", false},
			{"NULL ISNULL", true}, {"NULL NOTNULL", false},
			{"1 IS DISTINCT FROM NULL", true}, {"NULL IS NOT DISTINCT FROM NULL", true},
			{"1 IS NOT DISTINCT FROM 1", true}, {"1 IS DISTINCT FROM 1", false}, {"1 IS NOT DISTINCT FROM '1'", true},
			{"(NULL IS NULL) IS NOT DISTINCT FROM TRUE", true},
			{"(NULL IS NULL) AND NOT ((FALSE IS TRUE) OR (1 IS DISTINCT FROM 1))", true},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.expression), func(t *testing.T) {
				ctx := context.Background()
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				tableCoreExec(t, db, prepared, "CREATE TABLE pd(id INT,b BOOL DEFAULT ("+tc.expression+"))", 0)
				want := [][]any{}
				check := func() {
					t.Helper()
					q := `SELECT * FROM pd ORDER BY id`
					var rows *sql.Rows
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						defer s.Close()
						rows, err = s.Query()
					} else {
						rows, err = db.Query(q)
					}
					if err != nil {
						t.Fatal(err)
					}
					metadata, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					nullable, known := metadata[1].Nullable()
					if metadata[1].ScanType() != reflect.TypeFor[bool]() || !known || !nullable {
						t.Fatal("predicate default metadata", metadata[1])
					}
					timestampCheckRows(t, rows, []string{"id", "b"}, []string{"INT4", "BOOL"}, want)
				}
				roundtrip := func() {
					t.Helper()
					var dump bytes.Buffer
					if err := stuntgres.Dump(ctx, db, &dump); err != nil {
						t.Fatal(err)
					}
					if !strings.Contains(dump.String(), " DEFAULT (") {
						t.Fatal("predicate default structure lost", dump.String())
					}
					if err := stuntgres.Reset(ctx, db); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					check()
					var again bytes.Buffer
					if err := stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
						t.Fatal("predicate redump", err, again.String())
					}
				}
				check()
				roundtrip()
				tableCoreExec(t, db, prepared, `INSERT INTO pd(id) VALUES(1),(2)`, 2)
				tableCoreExec(t, db, prepared, `INSERT INTO pd VALUES(3,DEFAULT),(4,NULL)`, 2)
				want = [][]any{{int64(1), tc.want}, {int64(2), tc.want}, {int64(3), tc.want}, {int64(4), nil}}
				check()
				roundtrip()
				var before bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &before); err != nil {
					t.Fatal(err)
				}
				err := stuntgres.Restore(ctx, db, strings.NewReader(`CREATE TABLE failed(v BOOL DEFAULT (NULL IS NULL) NOT NULL); INSERT INTO failed VALUES(NULL);`))
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "v" of relation "failed" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "failed", Column: "v"})
				check()
				var after bytes.Buffer
				if err := stuntgres.Dump(ctx, db, &after); err != nil || after.String() != before.String() {
					t.Fatal("failed restore changed predicate definitions/data", err, after.String())
				}
				tableCoreExec(t, db, prepared, `INSERT INTO pd(id) VALUES(5)`, 1)
				tableCoreExec(t, db, prepared, `INSERT INTO pd VALUES(6,DEFAULT)`, 1)
				want = append(want, []any{int64(5), tc.want}, []any{int64(6), tc.want})
				check()
			})
		}
	}
}

func TestDefaultPredicateProhibitions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ expression, token, code, message string }{
			{"b IS NULL", "b IS", "0A000", "cannot use column reference in DEFAULT expression"},
			{"$1 IS NULL", "$1", "42P02", "there is no parameter $1"},
			{"TRUE OR (b IS NOT DISTINCT FROM NULL)", "b IS", "0A000", "cannot use column reference in DEFAULT expression"},
			{"FALSE AND ($1 IS TRUE)", "$1", "42P02", "there is no parameter $1"},
			{"(1 IS TRUE) AND b", "1 IS", "42804", "argument of IS TRUE must be type boolean, not type integer"},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.expression), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				q := "CREATE TABLE bad(b BOOL DEFAULT (" + tc.expression + "))"
				position := fmt.Sprint(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)
				var err error
				if prepared {
					s, e := db.Prepare(q)
					if e != nil {
						t.Fatal("utility Prepare", e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message, Position: position})
				_, err = db.Query(`SELECT * FROM bad`)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			})
		}
	}
}

func TestDefaultPredicateOuterSyntax(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ expression, token, position string }{
			{"NULL IS NULL", "NULL", "41"}, {"TRUE IS TRUE", "TRUE", "41"}, {"NULL ISNULL", "ISNULL", "38"}, {"NULL NOTNULL", "NOTNULL", "38"},
		} {
			t.Run(fmt.Sprintf("prepared=%v/%s", prepared, tc.expression), func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				q := "CREATE TABLE bad(b BOOL DEFAULT " + tc.expression + ")"
				var err error
				if prepared {
					s, e := db.Prepare(q)
					err = e
					if s != nil {
						s.Close()
					}
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42601", Message: `syntax error at or near "` + tc.token + `"`, Position: tc.position})
				_, err = db.Query(`SELECT * FROM bad`)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"})
			})
		}
		t.Run(fmt.Sprintf("prepared=%v/outer-distinct", prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, `CREATE TABLE outer_d(b BOOL DEFAULT 1 IS DISTINCT FROM 2,n BOOL DEFAULT NULL IS NOT DISTINCT FROM NULL)`, 0)
			tableCoreExec(t, db, prepared, `INSERT INTO outer_d DEFAULT VALUES`, 1)
			wantTuples(t, db, `SELECT * FROM outer_d`, [][]any{{true, true}})
		})
	}
}
