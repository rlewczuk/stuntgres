package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
	"time"
)

func alterDropDiagnostic(t *testing.T, err error, code, message, position string) {
	t.Helper()
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v (%v), want %#v", got, err, want)
	}
}

func TestAlterDropColumnPublicShapeAndPreparedLifetime(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)")
			execSQL(t, db, "INSERT INTO t VALUES(1,'drop',9),(2,NULL,NULL)")
			survivor, err := db.Prepare("SELECT a,c FROM t ORDER BY a")
			if err != nil {
				t.Fatal(err)
			}
			defer survivor.Close()
			star, err := db.Prepare("SELECT * FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer star.Close()
			dropped, err := db.Prepare("SELECT b FROM t")
			if err != nil {
				t.Fatal(err)
			}
			defer dropped.Close()
			held, err := db.Query("SELECT * FROM t ORDER BY a")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			tableCoreExec(t, db, prepared, "ALTER TABLE t DROP COLUMN b RESTRICT", 0)
			labels, _ := held.Columns()
			if !reflect.DeepEqual(labels, []string{"a", "b", "c"}) {
				t.Fatal(labels)
			}
			if !held.Next() {
				t.Fatal(held.Err())
			}
			var a, c int64
			var b string
			if err = held.Scan(&a, &b, &c); err != nil || a != 1 || b != "drop" || c != 9 {
				t.Fatal(a, b, c, err)
			}
			held.Close()
			for _, mode := range []bool{false, true} {
				alterDropMetadata(t, db, mode, "SELECT * FROM t", []string{"a", "c"}, []string{"INT4", "INT8"}, []bool{false, true}, 2)
			}
			wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(1), int64(9)}, {int64(2), nil}})
			rows, err := survivor.Query()
			if err != nil {
				t.Fatal(err)
			}
			rows.Close()
			_, err = star.Query()
			alterDropDiagnostic(t, err, "0A000", "cached plan must not change result type", "")
			_, err = dropped.Query()
			alterDropDiagnostic(t, err, "42703", `column "b" does not exist`, "8")
			_, err = db.Prepare("SELECT b FROM t")
			alterDropDiagnostic(t, err, "42703", `column "b" does not exist`, "8")
			tableCoreExec(t, db, prepared, "INSERT INTO t VALUES(3,30)", 1)
			tableCoreExec(t, db, true, "INSERT INTO t(c,a) VALUES($1,$2)", 1, 40, 4)
			wantTuples(t, db, "SELECT * FROM t ORDER BY a", [][]any{{int64(1), int64(9)}, {int64(2), nil}, {int64(3), int64(30)}, {int64(4), int64(40)}})
			for _, q := range []string{"ALTER TABLE t DROP COLUMN missing", "ALTER TABLE t DROP IF EXISTS ctid"} {
				_, err = db.Exec(q)
				if q == "ALTER TABLE t DROP COLUMN missing" {
					alterDropDiagnostic(t, err, "42703", `column "missing" of relation "t" does not exist`, "")
				} else {
					alterDropDiagnostic(t, err, "0A000", `cannot drop system column "ctid"`, "")
				}
			}
			execSQL(t, db, "ALTER TABLE ONLY(t) DROP c CASCADE")
			alterDropMetadata(t, db, true, "SELECT * FROM t", []string{"a"}, []string{"INT4"}, []bool{false}, 4)
		})
	}
}

func TestAlterDropColumnNoticesOwnershipAndTransaction(t *testing.T) {
	d := stuntgres.NewDriver()
	base, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'keep')")
	for _, prepared := range []bool{false, true} {
		notices = nil
		tableCoreExec(t, db, prepared, "ALTER TABLE t DROP COLUMN IF EXISTS missing", 0)
		want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal(notices)
		}
	}
	execSQL(t, db, "SET client_min_messages TO warning")
	notices = nil
	execSQL(t, db, "ALTER TABLE t DROP IF EXISTS missing")
	if len(notices) != 0 {
		t.Fatal(notices)
	}
	execSQL(t, db, "RESET client_min_messages")
	execSQL(t, db, "CREATE USER reader")
	execSQL(t, db, "SET SESSION AUTHORIZATION reader")
	stmt, err := db.Prepare("ALTER TABLE t DROP IF EXISTS missing")
	if err != nil {
		t.Fatal(err)
	}
	_, err = stmt.Exec()
	stmt.Close()
	alterDropDiagnostic(t, err, "42501", "must be owner of table t", "")
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("ALTER TABLE t DROP b"); err != nil {
		t.Fatal(err)
	}
	rows, err := tx.Query("SELECT * FROM t")
	if err != nil {
		t.Fatal(err)
	}
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"a"}) {
		t.Fatal(names, err)
	}
	rows.Close()
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(1), "keep"}})
}

func TestAlterDropColumnWaitCancellationAndCommittedInsert(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(4)
	execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec("INSERT INTO t VALUES(1,'committed')"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := db.ExecContext(ctx, "ALTER TABLE t DROP b"); done <- err }()
	select {
	case err := <-done:
		t.Fatal("AX did not wait", err)
	case <-time.After(25 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled waiter stuck")
	}
	done = make(chan error, 1)
	go func() { _, err := db.Exec("ALTER TABLE t DROP b"); done <- err }()
	select {
	case err := <-done:
		t.Fatal("AX did not wait", err)
	case <-time.After(25 * time.Millisecond):
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ALTER did not wake")
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(1)}})
	execSQL(t, db, "INSERT INTO t VALUES(2)")
	var dump bytes.Buffer
	if err = stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	_, err = db.ExecContext(canceled, "ALTER TABLE t DROP a")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var after bytes.Buffer
	if err = stuntgres.Dump(context.Background(), db, &after); err != nil || dump.String() != after.String() {
		t.Fatal(err)
	}
}

func TestAlterDropColumnShapesAndAtomicParserBoundaries(t *testing.T) {
	for _, column := range []string{"a", "b", "c"} {
		t.Run(column, func(t *testing.T) {
			db := openLocal(t, stuntgres.NewDriver(), t.Name())
			execSQL(t, db, "CREATE TABLE t(a INT,b TEXT,c BIGINT)")
			execSQL(t, db, "INSERT INTO t VALUES(1,'two',3)")
			execSQL(t, db, "CREATE TABLE clone(LIKE t)")
			execSQL(t, db, "ALTER TABLE t DROP "+column)
			expected := map[string][][]any{"a": {{"two", int64(3)}}, "b": {{int64(1), int64(3)}}, "c": {{int64(1), "two"}}}
			wantTuples(t, db, "SELECT * FROM t", expected[column])
			tableCoreMetadata(t, db, false, "SELECT * FROM clone", []string{"a", "b", "c"}, []string{"INT4", "TEXT", "INT8"}, []bool{true, true, true})
		})
	}
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT NOT NULL,b TEXT,oid INT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'keep',7)")
	var before bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ q, code, message, position string }{
		{"ALTER TABLE t ADD COLUMN c INT", "0A000", "ALTER TABLE action is unsupported", "15"},
		{"ALTER TABLE t DROP COLUMN )", "42601", `syntax error at or near ")"`, "27"},
		{`ALTER TABLE t DROP COLUMN "quo""te\\tail"`, "42703", `column "quo"te\\tail" of relation "t" does not exist`, ""},
	} {
		for _, prepared := range []bool{false, true} {
			var err error
			if prepared {
				var s *sql.Stmt
				s, err = db.Prepare(tc.q)
				if err == nil {
					_, err = s.Exec()
					s.Close()
				}
			} else {
				_, err = db.Exec(tc.q)
			}
			alterDropDiagnostic(t, err, tc.code, tc.message, tc.position)
			var after bytes.Buffer
			if err = stuntgres.Dump(context.Background(), db, &after); err != nil || before.String() != after.String() {
				t.Fatal(err, after.String())
			}
		}
	}
	for _, system := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
		_, err := db.Exec("ALTER TABLE t DROP IF EXISTS " + system)
		alterDropDiagnostic(t, err, "0A000", `cannot drop system column "`+system+`"`, "")
	}
	execSQL(t, db, "ALTER TABLE t DROP oid")
	_, err := db.Exec("INSERT INTO t VALUES(NULL,'bad')")
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "a" of relation "t" violates not-null constraint`, Detail: "Failing row contains (null, bad).", Schema: "public", Table: "t", Column: "a"}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(err, got)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(1), "keep"}})
}

func TestAlterDropColumnNativeDependencyCapabilityBoundary(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE t(a INT,b TEXT)")
	execSQL(t, db, "INSERT INTO t VALUES(1,'keep')")
	var before bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &before); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"CREATE INDEX idx ON t(b)"} {
		_, err := db.Exec(q)
		alterDropDiagnostic(t, err, "0A000", "CREATE syntax is unsupported", "8")
		var after bytes.Buffer
		if err = stuntgres.Dump(context.Background(), db, &after); err != nil || before.String() != after.String() {
			t.Fatal(err)
		}
	}
	execSQL(t, db, "CREATE VIEW v AS SELECT b FROM t")
	var withView bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &withView); err != nil {
		t.Fatal(err)
	}
	_, err := db.Exec("ALTER TABLE t DROP b RESTRICT")
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "2BP01", Message: "cannot drop column b of table t because other objects depend on it", Detail: "view v depends on column b of table t", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	var unchanged bytes.Buffer
	if err = stuntgres.Dump(context.Background(), db, &unchanged); err != nil || withView.String() != unchanged.String() {
		t.Fatal("failed restrict changed catalog", err)
	}
	wantTuples(t, db, "SELECT * FROM v", [][]any{{"keep"}})
	execSQL(t, db, "ALTER TABLE t DROP b CASCADE")
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(1)}})
	_, err = db.Query("SELECT * FROM v")
	alterDropDiagnostic(t, err, "42P01", `relation "v" does not exist`, "15")
}

// Unlike tableCoreMetadata's empty-table contract, these results retain rows.
func alterDropMetadata(t *testing.T, db *sql.DB, prepared bool, q string, names, types []string, nullable []bool, count int) {
	t.Helper()
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
		t.Fatal(q, err)
	}
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, err, names)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, c := range metadata {
		scan := reflect.TypeFor[int64]()
		if types[i] == "TEXT" {
			scan = reflect.TypeFor[string]()
		} else if types[i] == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		value, known := c.Nullable()
		if c.DatabaseTypeName() != types[i] || c.ScanType() != scan || !known || value != nullable[i] {
			t.Fatal(c, value, known)
		}
	}
	observed := 0
	for rows.Next() {
		values := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range values {
			targets[i] = &values[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		observed++
	}
	if err = rows.Err(); err != nil || observed != count {
		t.Fatal(observed, count, err)
	}
}
