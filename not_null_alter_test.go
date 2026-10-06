package stuntgres_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres"
	"testing"
	"unicode/utf8"
)

func TestNotNullAlterNoticeOwnershipAndTransaction(t *testing.T) {
	base, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	for _, prepared := range []bool{false, true} {
		notices = nil
		tableCoreExec(t, db, prepared, "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing", 0)
		want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `constraint "missing" of relation "t" does not exist, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal(notices)
		}
	}
	execSQL(t, db, "SET client_min_messages TO warning")
	notices = nil
	execSQL(t, db, "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing")
	if len(notices) != 0 {
		t.Fatal(notices)
	}
	execSQL(t, db, "RESET client_min_messages")
	execSQL(t, db, "CREATE USER reader")
	execSQL(t, db, "SET SESSION AUTHORIZATION reader")
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing"} {
		s, err := db.Prepare(q)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.Exec()
		s.Close()
		notNullDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "must be owner of table t"})
	}
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	for _, q := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ALTER a DROP NOT NULL", "ALTER TABLE t ADD NOT NULL a", "ALTER TABLE t DROP CONSTRAINT IF EXISTS missing"} {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.Exec(q)
		notNullDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: "statement is unsupported in an explicit transaction"})
		_, err = tx.Query("SELECT * FROM t")
		notNullDiagnostic(t, err, stuntgres.Error{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"})
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	alterDropMetadata(t, db, false, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{true}, 1)
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
}

func TestNotNullPublicExcludedSyntaxAndQuotedDiagnostics(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE "t""é"("a""b\\tail" INT,s TEXT)`)
	execSQL(t, db, `INSERT INTO "t""é" VALUES(NULL,'keep')`)
	_, err := db.Exec(`ALTER TABLE "t""é" ALTER "a""b\\tail" SET NOT NULL`)
	notNullDiagnostic(t, err, stuntgres.Error{Code: "23502", Message: `column "a"b\\tail" of relation "t"é" contains null values`, Schema: "public", Table: `t"é`, Column: `a"b\\tail`})
	execSQL(t, db, "CREATE TABLE t(a INT)")
	execSQL(t, db, "INSERT INTO t VALUES(7)")
	for _, tc := range []struct{ q, token, message string }{
		{"ALTER TABLE t ADD NOT NULL a NOT VALID", "NOT VALID", "NOT NULL modifiers are unsupported"},
		{"ALTER TABLE t VALIDATE CONSTRAINT nn", "VALIDATE", "ALTER TABLE action is unsupported"},
		{"CREATE TABLE child(a INT) INHERITS(t)", "INHERITS", "table inheritance is unsupported"},
		{"CREATE TABLE noinherit(a INT NOT NULL NO INHERIT)", "NO INHERIT", "NOT NULL modifiers are unsupported"},
		{"UPDATE t SET a=NULL", "UPDATE", "statement is unsupported"},
	} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				_, err = db.Prepare(tc.q)
			} else {
				_, err = db.Exec(tc.q)
			}
			notNullDiagnostic(t, err, stuntgres.Error{Code: "0A000", Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
		}
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(7)}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = db.ExecContext(ctx, "ALTER TABLE t ALTER a SET NOT NULL"); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestNotNullRetainedParameterizedStatements(t *testing.T) {
	for _, preparedUtility := range []bool{false, true} {
		for _, install := range []string{"ALTER TABLE t ALTER a SET NOT NULL", "ALTER TABLE t ADD CONSTRAINT nn NOT NULL a"} {
			t.Run(install+map[bool]string{false: "/direct", true: "/prepared"}[preparedUtility], func(t *testing.T) {
				db := openLocal(t, stuntgres.NewDriver(), t.Name())
				execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
				execSQL(t, db, "INSERT INTO t VALUES(7,'seed')")
				selectStmt, err := db.Prepare("SELECT * FROM t ORDER BY b")
				if err != nil {
					t.Fatal(err)
				}
				defer selectStmt.Close()
				insertStmt, err := db.Prepare("INSERT INTO t VALUES($1,$2)")
				if err != nil {
					t.Fatal(err)
				}
				defer insertStmt.Close()
				seed := [][]any{{int64(7), "seed"}}
				notNullRetainedRows(t, selectStmt, true, seed)
				tableCoreExec(t, db, preparedUtility, install, 0)
				want := stuntgres.Error{Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
				_, err = db.Exec("INSERT INTO t VALUES($1,$2)", nil, "bad")
				notNullDiagnostic(t, err, want)
				notNullRetainedRows(t, selectStmt, false, seed)
				_, err = insertStmt.Exec(nil, "bad")
				notNullDiagnostic(t, err, want)
				notNullRetainedRows(t, selectStmt, false, seed)
				insert := func(a any, b string) {
					result, err := insertStmt.Exec(a, b)
					if err != nil {
						t.Fatal(err)
					}
					count, err := result.RowsAffected()
					if err != nil || count != 1 {
						t.Fatal(count, err)
					}
				}
				insert(int64(8), "valid")
				notNullRetainedRows(t, selectStmt, false, [][]any{{int64(7), "seed"}, {int64(8), "valid"}})
				drop := "ALTER TABLE t ALTER a DROP NOT NULL"
				if strings.Contains(install, "ADD CONSTRAINT") {
					drop = "ALTER TABLE t DROP CONSTRAINT nn"
				}
				tableCoreExec(t, db, preparedUtility, drop, 0)
				insert(nil, "after")
				notNullRetainedRows(t, selectStmt, true, [][]any{{nil, "after"}, {int64(7), "seed"}, {int64(8), "valid"}})
			})
		}
	}
}

func notNullRetainedRows(t *testing.T, stmt *sql.Stmt, nullable bool, want [][]any) {
	t.Helper()
	rows, err := stmt.Query()
	if err != nil {
		t.Fatal("retained SELECT", err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	for i, typ := range types {
		wantType := "INT4"
		wantScan := reflect.TypeFor[int64]()
		wantNullable := nullable
		if i == 1 {
			wantType = "TEXT"
			wantScan = reflect.TypeFor[string]()
			wantNullable = true
		}
		gotNullable, known := typ.Nullable()
		if typ.DatabaseTypeName() != wantType || typ.ScanType() != wantScan || !known || gotNullable != wantNullable {
			t.Fatal(i, typ.DatabaseTypeName(), typ.ScanType(), gotNullable, known)
		}
	}
	var got [][]any
	for rows.Next() {
		var a, b any
		if err = rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{a, b})
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}

func TestNotNullInheritanceCompleteInputAndNoEffects(t *testing.T) {
	for _, tc := range []struct {
		q, token    string
		unsupported bool
	}{
		{"CREATE TABLE child(a INT) INHERITS(parent)", "INHERITS", true},
		{"CREATE TABLE IF NOT EXISTS child(a INT) INHERITS(parent)", "INHERITS", true},
		{`CREATE TABLE "échild"(a INT) INHERITS("s"."p",other,"q"."r")`, "INHERITS", true},
		{"CREATE TABLE IF NOT EXISTS existing(a INT) INHERITS(parent)", "INHERITS", true},
		{`CREATE TABLE IF NOT EXISTS "échild"(a INT) INHERITS("s"."p",other,"q"."r")`, "INHERITS", true},
		{"CREATE TABLE child(a INT) INHERITS()", ")", false},
		{"CREATE TABLE child(a INT) INHERITS(parent", "", false},
		{"CREATE TABLE child(a INT) INHERITS(parent,)", ")", false},
		{"CREATE TABLE child(a INT) INHERITS(parent other)", "other", false},
		{`CREATE TABLE child(a INT) INHERITS("s".)`, ")", false},
		{"CREATE TABLE child(a INT) INHERITS(parent) garbage", "garbage", false},
		{"CREATE TABLE child(a INT) INHERITS(parent) INHERITS(other)", "INHERITS", false},
	} {
		for _, prefix := range []string{"", "CREATE TABLE prefix(a INT); INSERT INTO keeper VALUES(92); "} {
			for _, prepared := range []bool{false, true} {
				t.Run(prefix+tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					db := openLocal(t, stuntgres.NewDriver(), t.Name())
					execSQL(t, db, "CREATE TABLE keeper(v INT)")
					execSQL(t, db, "INSERT INTO keeper VALUES(91)")
					execSQL(t, db, "CREATE TABLE existing(a INT)")
					execSQL(t, db, "INSERT INTO existing VALUES(51)")
					q := prefix + tc.q
					position := utf8.RuneCountInString(q) + 1
					message := "syntax error at end of input"
					if tc.token != "" {
						position = utf8.RuneCountInString(q[:strings.LastIndex(q, tc.token)]) + 1
						message = `syntax error at or near "` + tc.token + `"`
					}
					code := "42601"
					if tc.unsupported {
						code = "0A000"
						message = "table inheritance is unsupported"
					}
					var err error
					if prepared {
						stmt, prepareErr := db.Prepare(q)
						if stmt != nil {
							stmt.Close()
							t.Fatal("failed Prepare exposed a statement")
						}
						err = prepareErr
					} else {
						_, err = db.Exec(q)
					}
					notNullDiagnostic(t, err, stuntgres.Error{Code: code, Message: message, Position: strconv.Itoa(position)})
					wantTuples(t, db, "SELECT * FROM keeper", [][]any{{int64(91)}})
					wantTuples(t, db, "SELECT * FROM existing", [][]any{{int64(51)}})
					for _, name := range []string{"child", "échild", "prefix"} {
						rows, err := db.Query(`SELECT * FROM "` + name + `"`)
						if rows != nil {
							rows.Close()
						}
						notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "` + name + `" does not exist`})
					}
				})
			}
		}
	}
}
