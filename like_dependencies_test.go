package stuntgres_test

import (
	"bytes"
	"context"
	"stuntgres"
	"testing"
)

func TestLikePatternEscapeStoredDependencies(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,p TEXT,e TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'a%','!'),(2,NULL,NULL)")
	execSQL(t, db, "CREATE VIEW matched AS SELECT id,'ab' LIKE p ESCAPE e AS matched FROM t")
	stmt, err := db.Prepare("SELECT matched FROM matched ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, q := range []string{"ALTER TABLE t RENAME COLUMN p TO pattern", "ALTER TABLE t RENAME COLUMN e TO escape_value"} {
		execSQL(t, db, q)
		rows, err := stmt.Query()
		compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}, {nil}})
	}
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
	rows, err := db.Query("SELECT matched FROM matched ORDER BY id")
	compareOpsRows(t, rows, err, []string{"BOOL"}, [][]any{{true}, {nil}})
}

func TestLikeStoredPatternEscapeDependencyRestrictions(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(id INT,p TEXT,e TEXT,CONSTRAINT pattern_only CHECK('ab' LIKE p),CONSTRAINT escape_only CHECK('a%b' LIKE 'a!%b' ESCAPE e))")
	execSQL(t, db, "INSERT INTO t VALUES(1,'a%','!'),(2,NULL,NULL)")
	execSQL(t, db, "CREATE VIEW pattern_view AS SELECT id,'ab' LIKE p AS matched FROM t")
	execSQL(t, db, "CREATE VIEW escape_view AS SELECT id,'a%b' LIKE 'a!%b' ESCAPE e AS matched FROM t")
	pattern, err := db.Prepare("SELECT id,matched FROM pattern_view ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer pattern.Close()
	escape, err := db.Prepare("SELECT id,matched FROM escape_view ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer escape.Close()
	views := func(t *testing.T) {
		t.Helper()
		for _, q := range []string{"SELECT id,matched FROM pattern_view ORDER BY id", "SELECT id,matched FROM escape_view ORDER BY id"} {
			rows, err := db.Query(q)
			compareOpsRows(t, rows, err, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}})
		}
		rows, err := pattern.Query()
		compareOpsRows(t, rows, err, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}})
		rows, err = escape.Query()
		compareOpsRows(t, rows, err, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), nil}})
	}
	execSQL(t, db, "ALTER TABLE t RENAME COLUMN p TO pattern")
	views(t)
	execSQL(t, db, "ALTER TABLE t RENAME COLUMN e TO escape_value")
	views(t)
	checks := func(t *testing.T) {
		t.Helper()
		for _, tc := range []struct{ q, name, detail string }{
			{"INSERT INTO t VALUES(3,'b%','!')", "pattern_only", "3, b%, !"},
			{"INSERT INTO t VALUES(3,'a%','?')", "escape_only", "3, a%, ?"},
			{"UPDATE t SET pattern='b%' WHERE id=1", "pattern_only", "1, b%, !"},
			{"UPDATE t SET escape_value='?' WHERE id=1", "escape_only", "1, a%, ?"},
		} {
			want := stuntgres.Error{Code: "23514", Message: `new row for relation "t" violates check constraint "` + tc.name + `"`, Detail: "Failing row contains (" + tc.detail + ").", Schema: "public", Table: "t", Constraint: tc.name}
			_, err := db.Exec(tc.q)
			typeCoreDiagnostic(t, err, want)
			held, err := db.Prepare(tc.q)
			if err != nil {
				t.Fatal(err)
			}
			_, err = held.Exec()
			held.Close()
			typeCoreDiagnostic(t, err, want)
		}
		rows, err := db.Query("SELECT id,pattern,escape_value FROM t ORDER BY id")
		compareOpsRows(t, rows, err, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "a%", "!"}, {int64(2), nil, nil}})
	}
	dump := func(t *testing.T) []byte {
		t.Helper()
		var out bytes.Buffer
		if err := stuntgres.Dump(context.Background(), db, &out); err != nil {
			t.Fatal(err)
		}
		return out.Bytes()
	}
	views(t)
	checks(t)
	before := dump(t)
	for _, tc := range []struct{ column, view string }{{"pattern", "pattern_view"}, {"escape_value", "escape_view"}} {
		t.Run(tc.column, func(t *testing.T) {
			q := "ALTER TABLE t DROP COLUMN " + tc.column + " RESTRICT"
			held, err := db.Prepare(q)
			if err != nil {
				t.Fatal("dependency restriction rejected Prepare", err)
			}
			defer held.Close()
			want := stuntgres.Error{Code: "2BP01", Message: "cannot drop column " + tc.column + " of table t because other objects depend on it", Detail: "view " + tc.view + " depends on column " + tc.column + " of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}
			_, err = db.Exec(q)
			typeCoreDiagnostic(t, err, want)
			if !bytes.Equal(before, dump(t)) {
				t.Fatal("failed direct DDL changed stored snapshot")
			}
			views(t)
			checks(t)
			_, err = held.Exec()
			typeCoreDiagnostic(t, err, want)
			if !bytes.Equal(before, dump(t)) {
				t.Fatal("failed held DDL changed stored snapshot")
			}
			views(t)
			checks(t)
		})
	}
	// Own-table CHECK dependencies are automatic, unlike normal view edges.
	// Isolate them without adding unsupported DROP VIEW to the native surface.
	execSQL(t, db, "CREATE TABLE check_t(id INT,p TEXT,e TEXT,CONSTRAINT pattern_only CHECK('ab' LIKE p),CONSTRAINT escape_only CHECK('a%b' LIKE 'a!%b' ESCAPE e))")
	execSQL(t, db, "INSERT INTO check_t VALUES(1,'a%','!'),(2,NULL,NULL)")
	execSQL(t, db, "ALTER TABLE check_t RENAME COLUMN p TO pattern")
	execSQL(t, db, "ALTER TABLE check_t RENAME COLUMN e TO escape_value")
	execSQL(t, db, "ALTER TABLE check_t DROP COLUMN pattern RESTRICT")
	_, err = db.Exec("INSERT INTO check_t VALUES(3,'?')")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "23514", Message: `new row for relation "check_t" violates check constraint "escape_only"`, Detail: "Failing row contains (3, ?).", Schema: "public", Table: "check_t", Constraint: "escape_only"})
	execSQL(t, db, "ALTER TABLE check_t DROP COLUMN escape_value RESTRICT")
	execSQL(t, db, "INSERT INTO check_t VALUES(3)")
	rows, err := db.Query("SELECT id FROM check_t ORDER BY id")
	compareOpsRows(t, rows, err, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}})
}
