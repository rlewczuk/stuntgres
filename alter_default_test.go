package stuntgres_test

import (
	"bytes"
	"context"
	"fmt"
	"stuntgres"
	"testing"
)

func TestAlterDefaultLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
			exec("CREATE TABLE d(id INT,i INT DEFAULT 11,b BOOL DEFAULT FALSE)", 0)
			exec("INSERT INTO d(id) VALUES(0)", 1)
			held, err := db.Prepare("INSERT INTO d(id) VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			defaults, err := db.Prepare("INSERT INTO d VALUES($1,DEFAULT,DEFAULT)")
			if err != nil {
				t.Fatal(err)
			}
			defer defaults.Close()
			exec("ALTER TABLE d ALTER COLUMN i SET DEFAULT 7", 0)
			r, err := held.Exec(1)
			if err != nil {
				t.Fatal(err)
			}
			if n, e := r.RowsAffected(); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			r, err = defaults.Exec(2)
			if err != nil {
				t.Fatal(err)
			}
			if n, e := r.RowsAffected(); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			exec("INSERT INTO d(id) VALUES(3)", 1)
			exec("INSERT INTO d VALUES(4,DEFAULT,DEFAULT),(5,99,TRUE),(6,NULL,NULL)", 3)
			exec("ALTER TABLE ONLY(d) ALTER i SET DEFAULT 9", 0)
			r, err = held.Exec(7)
			if err != nil {
				t.Fatal(err)
			}
			if n, e := r.RowsAffected(); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			exec("ALTER TABLE d* ALTER i DROP DEFAULT", 0)
			exec("ALTER TABLE ONLY d ALTER i DROP DEFAULT", 0)
			r, err = defaults.Exec(8)
			if err != nil {
				t.Fatal(err)
			}
			if n, e := r.RowsAffected(); e != nil || n != 1 {
				t.Fatal(n, e)
			}
			defaultConsumerRows(t, db, prepared, []string{"id", "i", "b"}, []string{"INT4", "INT4", "BOOL"}, [][]any{{int64(0), int64(11), false}, {int64(1), int64(7), false}, {int64(2), int64(7), false}, {int64(3), int64(7), false}, {int64(4), int64(7), false}, {int64(5), int64(99), true}, {int64(6), nil, nil}, {int64(7), int64(9), false}, {int64(8), nil, false}})
			exec("CREATE TABLE q(i INT DEFAULT 1)", 0)
			exec("ALTER TABLE public.q ALTER i SET DEFAULT 7", 0)
			exec("INSERT INTO q DEFAULT VALUES", 1)
			exec("ALTER TABLE q ALTER i DROP DEFAULT", 0)
			exec("INSERT INTO q DEFAULT VALUES", 1)
			wantTuples(t, db, "SELECT * FROM q ORDER BY i", [][]any{{int64(7)}, {nil}})
			exec("CREATE TABLE source(id INT)", 0)
			exec("INSERT INTO source VALUES(10)", 1)
			exec("ALTER TABLE d ALTER i SET DEFAULT 7", 0)
			exec("INSERT INTO d(id) SELECT id FROM source", 1)
			exec("INSERT INTO d(id) SELECT id FROM source WHERE id=999", 0)
			wantTuples(t, db, "SELECT i FROM d WHERE id=10", [][]any{{int64(7)}})
		})
	}
}

func TestAlterDefaultOwnership(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			db.SetMaxOpenConns(1)
			exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
			// Native public already permits namespace USAGE/CREATE; no ACL SQL is needed.
			exec("CREATE USER alter_default_owner", 0)
			exec("CREATE USER alter_default_reader", 0)
			exec("SET SESSION AUTHORIZATION alter_default_owner", 0)
			defer db.Exec("RESET SESSION AUTHORIZATION")
			exec("CREATE TABLE t(id INT,i INT DEFAULT 7)", 0)
			exec("INSERT INTO t VALUES(0,11)", 1)
			for index, q := range []string{"ALTER TABLE t ALTER i SET DEFAULT 'oops'", "ALTER TABLE t ALTER i DROP DEFAULT"} {
				exec("RESET SESSION AUTHORIZATION", 0)
				exec("SET SESSION AUTHORIZATION alter_default_reader", 0)
				var err error
				if prepared {
					stmt, e := db.Prepare(q)
					if e != nil {
						t.Fatal("ownership checked at Prepare", q, e)
					}
					_, err = stmt.Exec()
					stmt.Close()
				} else {
					_, err = db.Exec(q)
				}
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "must be owner of table t"})
				exec("RESET SESSION AUTHORIZATION", 0)
				exec("SET SESSION AUTHORIZATION alter_default_owner", 0)
				// Both omission and explicit DEFAULT still use the original definition.
				exec(fmt.Sprintf("INSERT INTO t(id) VALUES(%d)", index*2+1), 1)
				exec(fmt.Sprintf("INSERT INTO t VALUES(%d,DEFAULT)", index*2+2), 1)
				want := [][]any{{int64(0), int64(11)}}
				for id := 1; id <= index*2+2; id++ {
					want = append(want, []any{int64(id), int64(7)})
				}
				wantTuples(t, db, "SELECT * FROM t ORDER BY id", want)
			}
			// Positive authority is an ordinary owner, not the bootstrap superuser.
			exec("ALTER TABLE t ALTER i SET DEFAULT 9", 0)
			exec("INSERT INTO t(id) VALUES(5)", 1)
			exec("ALTER TABLE t ALTER i DROP DEFAULT", 0)
			exec("INSERT INTO t VALUES(6,DEFAULT)", 1)
			wantTuples(t, db, "SELECT * FROM t ORDER BY id", [][]any{{int64(0), int64(11)}, {int64(1), int64(7)}, {int64(2), int64(7)}, {int64(3), int64(7)}, {int64(4), int64(7)}, {int64(5), int64(9)}, {int64(6), nil}})
		})
	}
}

func TestAlterDefaultDiagnostics(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			tableCoreExec(t, db, prepared, "CREATE TABLE t(id INT,i INT DEFAULT 7,b BOOL DEFAULT FALSE,v VARCHAR(2))", 0)
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(0,11,FALSE,'ok')", 1)
			cases := []struct {
				q    string
				want stuntgres.Error
			}{
				{"ALTER TABLE absent ALTER i SET DEFAULT 7", stuntgres.Error{Code: "42P01", Message: `relation "absent" does not exist`}},
				{"ALTER TABLE absent ALTER i DROP DEFAULT", stuntgres.Error{Code: "42P01", Message: `relation "absent" does not exist`}},
				{"ALTER TABLE t ALTER missing SET DEFAULT 'oops'", stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
				{"ALTER TABLE t ALTER missing DROP DEFAULT", stuntgres.Error{Code: "42703", Message: `column "missing" of relation "t" does not exist`}},
				{"ALTER TABLE t ALTER i SET DEFAULT TRUE", stuntgres.Error{Code: "42804", Message: `column "i" is of type integer but default expression is of type boolean`, Hint: "You will need to rewrite or cast the expression."}},
				{"ALTER TABLE t ALTER i SET DEFAULT 'oops'", stuntgres.Error{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}},
				{"ALTER TABLE t ALTER i SET DEFAULT '2147483648'", stuntgres.Error{Code: "22003", Message: `value "2147483648" is out of range for type integer`}},
			}
			for _, expr := range []string{"i", "b", "CAST(i AS INT)", "CASE WHEN TRUE THEN 1 ELSE i END"} {
				cases = append(cases, struct {
					q    string
					want stuntgres.Error
				}{"ALTER TABLE t ALTER i SET DEFAULT " + expr, stuntgres.Error{Code: "0A000", Message: "cannot use column reference in DEFAULT expression"}})
			}
			for _, expr := range []string{"$1", "CAST($1 AS INT)", "CASE WHEN TRUE THEN 1 ELSE $1 END"} {
				cases = append(cases, struct {
					q    string
					want stuntgres.Error
				}{"ALTER TABLE t ALTER i SET DEFAULT " + expr, stuntgres.Error{Code: "42P02", Message: "there is no parameter $1"}})
			}
			for _, col := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
				for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
					cases = append(cases, struct {
						q    string
						want stuntgres.Error
					}{"ALTER TABLE t ALTER " + col + " " + action, stuntgres.Error{Code: "0A000", Message: `cannot alter system column "` + col + `"`}})
				}
			}
			for _, tc := range cases {
				var err error
				if prepared {
					s, e := db.Prepare(tc.q)
					if e != nil {
						t.Fatal("semantic error at Prepare", tc.q, e)
					}
					_, err = s.Exec()
					s.Close()
				} else {
					_, err = db.Exec(tc.q)
				}
				typeCoreDiagnostic(t, err, tc.want)
				tableCoreExec(t, db, prepared, "INSERT INTO t(id) VALUES(1)", 1)
				wantTuples(t, db, "SELECT i,b FROM t WHERE id=1", [][]any{{int64(7), false}})
				tableCoreExec(t, db, prepared, "DELETE FROM t WHERE id=1", 1)
				wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(0), int64(11), false, "ok"}})
			}
			tableCoreExec(t, db, prepared, "CREATE SEQUENCE s", 0)
			for _, action := range []string{"SET DEFAULT 7", "DROP DEFAULT"} {
				_, err := db.Exec("ALTER TABLE s ALTER i " + action)
				typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42809", Message: `ALTER action ALTER COLUMN ... SET DEFAULT cannot be performed on relation "s"`, Detail: "This operation is not supported for sequences."})
			}
		})
	}
}

func TestAlterDefaultDeferred(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ typ, expr, code, message string }{{"INT", "2147483648", "22003", "integer out of range"}, {"INT", "1/0", "22012", "division by zero"}, {"VARCHAR(2)", "'long'::text", "22001", "value too long for type character varying(2)"}} {
			t.Run(fmt.Sprint(prepared)+tc.expr, func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				exec := func(q string, n int64) { tableCoreExec(t, db, prepared, q, n) }
				exec("CREATE TABLE t(id INT,i "+tc.typ+")", 0)
				exec("INSERT INTO t VALUES(0,NULL)", 1)
				exec("ALTER TABLE t ALTER i SET DEFAULT "+tc.expr, 0)
				var dump bytes.Buffer
				ctx := context.Background()
				if err := stuntgres.Dump(ctx, db, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Reset(ctx, db); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				exec("INSERT INTO t VALUES(1,NULL)", 1)
				for _, q := range []string{"INSERT INTO t(id) VALUES(2)", "INSERT INTO t VALUES(2,NULL),(3,DEFAULT)"} {
					var err error
					if prepared {
						s, e := db.Prepare(q)
						if e != nil {
							t.Fatal(e)
						}
						_, err = s.Exec()
						s.Close()
					} else {
						_, err = db.Exec(q)
					}
					typeCoreDiagnostic(t, err, stuntgres.Error{Code: tc.code, Message: tc.message})
					wantTuples(t, db, "SELECT * FROM t ORDER BY id", [][]any{{int64(0), nil}, {int64(1), nil}})
				}
			})
		}
	}
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE nn(id INT,i INT NOT NULL DEFAULT 7)")
	execSQL(t, db, "INSERT INTO nn VALUES(0,7)")
	for _, action := range []string{"SET DEFAULT NULL", "DROP DEFAULT"} {
		execSQL(t, db, "ALTER TABLE nn ALTER i "+action)
		for _, q := range []string{"INSERT INTO nn VALUES(3,NULL)", "INSERT INTO nn(id) VALUES(3)", "INSERT INTO nn VALUES(3,DEFAULT)"} {
			_, err := db.Exec(q)
			typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `null value in column "i" of relation "nn" violates not-null constraint`, Detail: "Failing row contains (3, null).", Schema: "public", Table: "nn", Column: "i"})
		}
	}
	wantTuples(t, db, "SELECT * FROM nn", [][]any{{int64(0), int64(7)}})
}
