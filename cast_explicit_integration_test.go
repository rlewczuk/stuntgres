package stuntgres_test

import (
	"database/sql"
	"testing"

	"stuntgres"
)

func TestCastExplicitIntegratedExpressions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), "cast-integrated")
	execSQL(t, db, "CREATE TABLE t(id INT4,i INT4,b BOOL)")
	execSQL(t, db, "INSERT INTO t VALUES(1,2,TRUE),(2,0,FALSE),(3,NULL,NULL)")
	compareOpsDirectPrepared(t, db, "SELECT i::int8>1,NOT i::bool,(i::bool AND b) OR FALSE,num_nulls(i::text,b::int4) FROM t ORDER BY id", []string{"BOOL", "BOOL", "BOOL", "INT4"}, [][]any{{true, false, true, int64(0)}, {false, true, false, int64(0)}, {nil, nil, nil, int64(2)}})
	compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE NOT i::bool OR i::int8>1 ORDER BY id", []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
	compareOpsDirectPrepared(t, db, "SELECT num_nulls(NULL::int4),num_nonnulls(int4('42'::text))", []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}})
	compareOpsDirectPrepared(t, db, "SELECT i::text IS NULL,i::bool IS TRUE,i::int8 IS DISTINCT FROM 0,0 IS NOT DISTINCT FROM i::int8 FROM t ORDER BY id", []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, false}, {false, false, false, true}, {true, false, true, false}})
	compareOpsDirectPrepared(t, db, "SELECT NULL::int4 IS DISTINCT FROM i::int4,i::int4 IS DISTINCT FROM NULL FROM t ORDER BY id", []string{"BOOL", "BOOL"}, [][]any{{true, true}, {true, true}, {false, false}})
	for _, arg := range []any{"42", nil, "0"} {
		compareOpsDirectPrepared(t, db, "SELECT int4($1::text) IS NULL,int4($1::text)::bool IS TRUE,int4($1::text) IS DISTINCT FROM 0 FROM t WHERE id=1", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{arg == nil, arg == "42", arg != "0"}}, arg)
	}
	for _, q := range []string{"SELECT int8(TRUE) IS NULL FROM t", "SELECT int8(TRUE) IS DISTINCT FROM i FROM t"} {
		want := stuntgres.Error{Code: "42883", Message: "function int8(boolean) does not exist", Position: "8", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}
		_, err := db.Query(q)
		typeCoreDiagnostic(t, err, want)
		_, err = db.Prepare(q)
		typeCoreDiagnostic(t, err, want)
	}
	compareOpsDirectPrepared(t, db, "SELECT b::BOOL AND b,i::INT<id,(i)<(id) FROM t ORDER BY id", []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}, {false, true, true}, {nil, nil, nil}})
	compareOpsDirectPrepared(t, db, "SELECT id FROM t WHERE (id)=1", []string{"INT4"}, [][]any{{int64(1)}})
	execSQL(t, db, "CREATE TABLE e(i INT4)")
	for _, q := range []string{
		"SELECT i>int4('oops'::text) FROM e",
		"SELECT TRUE AND bool(int4('oops'::text)) FROM e",
		"SELECT num_nulls(int4('oops'::text)) FROM e",
		"SELECT num_nulls(int4('oops'::text))",
		"SELECT int4('oops'::text) IS NULL FROM e",
		"SELECT bool(int4('oops'::text)) IS TRUE FROM e",
		"SELECT int4('oops'::text) IS DISTINCT FROM i FROM e",
		"SELECT i IS NOT DISTINCT FROM int4('oops'::text) FROM e",
		"SELECT int4('oops'::text) IS DISTINCT FROM NULL FROM e",
	} {
		t.Run(q, func(t *testing.T) {
			rows, err := db.Query(q)
			if rows != nil {
				rows.Close()
				t.Fatal("failed preflight exposed cursor")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
			stmt, err := db.Prepare(q)
			if err != nil {
				t.Fatal("execution-owned failure moved into Prepare", err)
			}
			defer stmt.Close()
			rows, err = stmt.Query()
			if rows != nil {
				rows.Close()
				t.Fatal("failed prepared preflight exposed cursor")
			}
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
		})
	}
	for _, q := range []string{
		"SELECT int4($1::text) IS NULL FROM e",
		"SELECT bool(int4($1::text)) IS TRUE FROM e",
		"SELECT int4($1::text) IS DISTINCT FROM i FROM e",
		"SELECT i IS NOT DISTINCT FROM int4($1::text) FROM e",
	} {
		stmt, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range []any{"oops", "42", nil, "oops"} {
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = stmt.Query(arg)
				} else {
					rows, err = db.Query(q, arg)
				}
				if arg == "oops" {
					if rows != nil {
						rows.Close()
						t.Fatal("predicate preflight exposed cursor")
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`})
				} else {
					compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{})
				}
			}
		}
		stmt.Close()
	}
}
