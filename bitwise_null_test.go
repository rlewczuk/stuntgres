package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestBitwiseTypedNullBothSides(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE one(v INTEGER)", 0)
	arithmeticExec(t, db, "INSERT INTO one VALUES(1)", 1)
	for _, tc := range []struct{ q, typ string }{
		{"SELECT NULL::smallint & 1::smallint,1::smallint & NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint | 1::smallint,1::smallint | NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint # 1::smallint,1::smallint # NULL::smallint FROM one", "INT2"},
		{"SELECT NULL::smallint << 1::integer,1::smallint << NULL::integer FROM one", "INT2"},
		{"SELECT NULL::smallint >> 1::integer,1::smallint >> NULL::integer FROM one", "INT2"},
		{"SELECT NULL::integer & 1::integer,1::integer & NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer | 1::integer,1::integer | NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer # 1::integer,1::integer # NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer << 1::integer,1::integer << NULL::integer FROM one", "INT4"},
		{"SELECT NULL::integer >> 1::integer,1::integer >> NULL::integer FROM one", "INT4"},
		{"SELECT NULL::bigint & 1::bigint,1::bigint & NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint | 1::bigint,1::bigint | NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint # 1::bigint,1::bigint # NULL::bigint FROM one", "INT8"},
		{"SELECT NULL::bigint << 1::integer,1::bigint << NULL::integer FROM one", "INT8"},
		{"SELECT NULL::bigint >> 1::integer,1::bigint >> NULL::integer FROM one", "INT8"},
	} {
		arithmeticRows(t, db, tc.q, []string{tc.typ, tc.typ}, [][]any{{nil, nil}})
	}
}

// A constant NULL eliminates the strict operator after analysis, including a
// row-dependent conversion that would fail if evaluated. A row-owned NULL is
// deliberately not the same planning fact.
func TestBitwiseConstantNullRowConversion(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE null_input(id INTEGER,x TEXT)", 0)
	arithmeticExec(t, db, "INSERT INTO null_input VALUES(1,'bad'),(2,'7')", 2)
	for _, width := range []struct{ sql, typ, cast string }{
		{"smallint", "INT2", "CAST(CAST(x AS integer) AS smallint)"},
		{"integer", "INT4", "CAST(x AS integer)"},
		{"bigint", "INT8", "CAST(x AS bigint)"},
	} {
		for _, op := range []string{"&", "|", "#", "<<", ">>"} {
			leftPeer, rightNull := width.cast, "NULL::"+width.sql
			if op == "<<" || op == ">>" {
				leftPeer, rightNull = "CAST(x AS integer)", "NULL::integer"
			}
			q := "SELECT NULL::" + width.sql + " " + op + " " + leftPeer + "," + width.cast + " " + op + " " + rightNull + " FROM null_input ORDER BY id"
			for _, prepared := range []bool{false, true} {
				mode := "direct"
				if prepared {
					mode = "prepared"
				}
				t.Run(width.sql+"/"+op+"/"+mode, func(t *testing.T) {
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, prepareErr := db.Prepare(q)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						defer stmt.Close()
						rows, err = stmt.Query()
					} else {
						rows, err = db.Query(q)
					}
					if err != nil {
						t.Fatal(q, err)
					}
					defer rows.Close()
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 2 {
						t.Fatal(meta, err)
					}
					for _, column := range meta {
						if column.Name() != "?column?" || column.DatabaseTypeName() != width.typ || column.ScanType() != reflect.TypeOf(int64(0)) {
							t.Fatal(column)
						}
						if _, known := column.Nullable(); known {
							t.Fatal("computed NULL must retain unknown nullability")
						}
					}
					var got [][]any
					for rows.Next() {
						var left, right any
						if err := rows.Scan(&left, &right); err != nil {
							t.Fatal(err)
						}
						got = append(got, []any{left, right})
					}
					if err := rows.Err(); err != nil || !reflect.DeepEqual(got, [][]any{{nil, nil}, {nil, nil}}) {
						t.Fatal(got, err)
					}
				})
			}
			t.Run(width.sql+"/"+op+"/empty", func(t *testing.T) {
				arithmeticRows(t, db, strings.Replace(q, " ORDER BY id", " WHERE FALSE ORDER BY id", 1), []string{width.typ, width.typ}, [][]any{})
			})
		}
	}
}

func TestBitwiseConstantNullConsumers(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE null_source(id INTEGER,x TEXT,i INTEGER,b BIGINT)", 0)
	arithmeticExec(t, db, "INSERT INTO null_source VALUES(1,'bad',9,9),(2,'7',9,9)", 2)
	arithmeticRows(t, db, "SELECT COALESCE(NULL::integer & CAST(x AS integer),5),CASE WHEN id=1 THEN CAST(x AS bigint) >> NULL::integer ELSE NULL::bigint END FROM null_source ORDER BY id", []string{"INT4", "INT8"}, [][]any{{int64(5), nil}, {int64(5), nil}})
	arithmeticRows(t, db, "SELECT id FROM null_source WHERE (CAST(x AS integer) # NULL::integer) IS NULL ORDER BY id", []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	arithmeticExec(t, db, "CREATE VIEW null_view AS SELECT id,NULL::integer | CAST(x AS integer) AS mask FROM null_source", 0)
	arithmeticRows(t, db, "SELECT mask FROM null_view ORDER BY id", []string{"INT4"}, [][]any{{nil}, {nil}})
	arithmeticExec(t, db, "CREATE TABLE null_dest(id INTEGER,s SMALLINT,b BIGINT)", 0)
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{
			"INSERT INTO null_dest SELECT id,NULL::smallint << CAST(x AS integer),CAST(x AS bigint) & NULL::bigint FROM null_source ORDER BY id",
			"UPDATE null_source SET i=CAST(x AS integer) | NULL::integer,b=NULL::bigint >> CAST(x AS integer)",
		} {
			if prepared {
				stmt, err := db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				result, err := stmt.Exec()
				stmt.Close()
				if err != nil {
					t.Fatal(q, err)
				}
				if n, err := result.RowsAffected(); err != nil || n != 2 {
					t.Fatal(n, err)
				}
			} else {
				arithmeticExec(t, db, q, 2)
			}
		}
		arithmeticRows(t, db, "SELECT i,b FROM null_source ORDER BY id", []string{"INT4", "INT8"}, [][]any{{nil, nil}, {nil, nil}})
	}
	arithmeticRows(t, db, "SELECT s,b FROM null_dest ORDER BY id", []string{"INT2", "INT8"}, [][]any{{nil, nil}, {nil, nil}, {nil, nil}, {nil, nil}})
	_, err := db.Exec("ALTER TABLE null_source DROP COLUMN x")
	wantDependency := stuntgres.Error{Code: "2BP01", Message: "cannot drop column x of table null_source because other objects depend on it", Detail: "view null_view depends on column x of table null_source", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
	arithmeticError(t, err, wantDependency)
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	arithmeticRows(t, db, "SELECT mask FROM null_view ORDER BY id", []string{"INT4"}, [][]any{{nil}, {nil}})
	_, err = db.Exec("ALTER TABLE null_source DROP COLUMN x")
	arithmeticError(t, err, wantDependency)
}

func TestBitwiseRuntimeNullStillEvaluatesRows(t *testing.T) {
	_, db := arithmeticDB(t)
	arithmeticExec(t, db, "CREATE TABLE runtime_null(x TEXT,i INTEGER)", 0)
	arithmeticExec(t, db, "INSERT INTO runtime_null VALUES('bad',NULL)", 1)
	for _, q := range []string{
		"SELECT i & CAST(x AS integer) FROM runtime_null",
		"SELECT CAST(x AS integer) & i FROM runtime_null",
	} {
		for _, prepared := range []bool{false, true} {
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, prepareErr := db.Prepare(q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				rows, err = stmt.Query()
				stmt.Close()
			} else {
				rows, err = db.Query(q)
			}
			if rows != nil {
				rows.Close()
			}
			arithmeticError(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`})
		}
	}
}
