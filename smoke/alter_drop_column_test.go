package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func alterDropSmokePool(t *testing.T, notices *[]stuntgres.Error) *sql.DB {
	t.Helper()
	base, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { *notices = append(*notices, n) }))
	t.Cleanup(func() { db.Close() })
	return db
}
func alterDropSmokeExec(t *testing.T, db *sql.DB, q string, args ...any) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatal(q, err)
	}
}
func alterDropSmokeError(t *testing.T, err error, code, message, position string) {
	t.Helper()
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if !errors.As(err, &got) || *got != want {
		t.Fatal(got, err, want)
	}
}
func alterDropSmokeRows(t *testing.T, rows *sql.Rows, names, types []string, want [][]any) {
	t.Helper()
	defer rows.Close()
	labels, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(labels, names) {
		t.Fatal(labels, err)
	}
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(types) {
		t.Fatal(metadata, err)
	}
	for i, c := range metadata {
		scan := reflect.TypeFor[int64]()
		switch types[i] {
		case "TEXT", "VARCHAR", "NUMERIC":
			scan = reflect.TypeFor[string]()
		case "BOOL":
			scan = reflect.TypeFor[bool]()
		case "FLOAT8":
			scan = reflect.TypeFor[float64]()
		}
		if c.DatabaseTypeName() != types[i] || c.ScanType() != scan {
			t.Fatal(c)
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, len(names))
		targets := make([]any, len(names))
		for i := range row {
			targets[i] = &row[i]
		}
		if err = rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		got = append(got, row)
	}
	if err = rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, want, err)
	}
}

func TestAlterDropColumnSmoke(t *testing.T) {
	var notices []stuntgres.Error
	db := alterDropSmokePool(t, &notices)
	ctx := context.Background()
	alterDropSmokeExec(t, db, "CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)")
	alterDropSmokeExec(t, db, "INSERT INTO t VALUES(1,'sentinel',9),(2,NULL,NULL)")
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
	utility, err := db.Prepare("ALTER TABLE t DROP COLUMN b")
	if err != nil {
		t.Fatal(err)
	}
	r, err := utility.Exec()
	utility.Close()
	if err != nil {
		t.Fatal(err)
	}
	if count, err := r.RowsAffected(); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	want := [][]any{{int64(1), int64(9)}, {int64(2), nil}}
	rows, err := db.Query("SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	rows, err = survivor.Query()
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	fresh, err := db.Prepare("SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	freshRows, err := fresh.Query()
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := freshRows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range metadata {
		nullable, known := c.Nullable()
		if !known || nullable != (i == 1) {
			t.Fatal(c, nullable, known)
		}
	}
	alterDropSmokeRows(t, freshRows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	_, err = star.Query()
	alterDropSmokeError(t, err, "0A000", "cached plan must not change result type", "")
	alterDropSmokeExec(t, db, "ALTER TABLE t DROP COLUMN IF EXISTS missing")
	expectedNotice := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
	if !reflect.DeepEqual(notices, expectedNotice) {
		t.Fatal(notices)
	}
	_, err = db.Exec("ALTER TABLE t DROP missing")
	alterDropSmokeError(t, err, "42703", `column "missing" of relation "t" does not exist`, "")
	alterDropSmokeExec(t, db, "INSERT INTO t VALUES(3,30)")
	insert, err := db.Prepare("INSERT INTO t(c,a) VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = insert.Exec(40, 4); err != nil {
		t.Fatal(err)
	}
	insert.Close()
	want = append(want, []any{int64(3), int64(30)}, []any{int64(4), int64(40)})
	alterDropSmokeExec(t, db, "CREATE TABLE only_table(a INT,b TEXT)")
	alterDropSmokeExec(t, db, "INSERT INTO only_table VALUES(7,'gone')")
	alterDropSmokeExec(t, db, "ALTER TABLE ONLY(only_table) DROP b CASCADE")
	rows, err = db.Query("SELECT * FROM only_table")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil || strings.Contains(dump.String(), "sentinel") {
		t.Fatal(err, dump.String())
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(canceled, "ALTER TABLE t DROP c")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var same bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &same); err != nil || same.String() != dump.String() {
		t.Fatal(err)
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	alterDropSmokeExec(t, db, `CREATE TABLE quoted_smoke("q""u\z" INT,a INT)`)
	alterDropSmokeExec(t, db, `INSERT INTO quoted_smoke VALUES(1,7)`)
	quoted, err := db.Prepare(`/*é*/ SELECT "q""u\z" FROM quoted_smoke`)
	if err != nil {
		t.Fatal(err)
	}
	defer quoted.Close()
	alterDropSmokeExec(t, db, `ALTER TABLE quoted_smoke DROP "q""u\z"`)
	_, err = quoted.Query()
	alterDropSmokeError(t, err, "42703", `column "q"u\z" does not exist`, "14")
	_, err = db.Prepare(`/*é*/ SELECT "q""u\z" FROM quoted_smoke`)
	alterDropSmokeError(t, err, "42703", `column "q"u\z" does not exist`, "14")
	rows, err = db.Query("SELECT * FROM quoted_smoke")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	db.SetMaxOpenConns(1)
	alterDropSmokeExec(t, db, "CREATE USER smoke_drop_owner")
	alterDropSmokeExec(t, db, "CREATE SCHEMA owner_smoke AUTHORIZATION smoke_drop_owner")
	alterDropSmokeExec(t, db, "SET SESSION AUTHORIZATION smoke_drop_owner")
	alterDropSmokeExec(t, db, "CREATE TABLE owner_smoke.t(a INT,b TEXT,c INT)")
	alterDropSmokeExec(t, db, "INSERT INTO owner_smoke.t VALUES(7,'gone',9)")
	alterDropSmokeExec(t, db, "ALTER TABLE owner_smoke.t DROP b")
	ownerDrop, err := db.Prepare("ALTER TABLE owner_smoke.t DROP c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ownerDrop.Exec(); err != nil {
		t.Fatal(err)
	}
	ownerDrop.Close()
	alterDropSmokeExec(t, db, "RESET SESSION AUTHORIZATION")
	var ownerDump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &ownerDump); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ownerDump.String(), `CREATE SCHEMA "owner_smoke" AUTHORIZATION "smoke_drop_owner";`) || !strings.Contains(ownerDump.String(), `SET SESSION AUTHORIZATION 'smoke_drop_owner';`) {
		t.Fatal(ownerDump.String())
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(ownerDump.Bytes())); err != nil {
		t.Fatal(err)
	}
	alterDropSmokeExec(t, db, "SET SESSION AUTHORIZATION smoke_drop_owner")
	rows, err = db.Query("SELECT * FROM owner_smoke.t")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	alterDropSmokeExec(t, db, "ALTER TABLE owner_smoke.t DROP IF EXISTS b")
	alterDropSmokeExec(t, db, "RESET SESSION AUTHORIZATION")
	var ownerAgain bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &ownerAgain); err != nil || ownerAgain.String() != ownerDump.String() {
		t.Fatal(err, ownerAgain.String())
	}
	alterDropSmokeExec(t, db, "CREATE TABLE comment_smoke(a INT,b TEXT,c BIGINT)")
	alterDropSmokeExec(t, db, "INSERT INTO comment_smoke VALUES(1,'removed',9)")
	for _, q := range []string{"COMMENT ON TABLE comment_smoke IS 'table keep'", "COMMENT ON COLUMN comment_smoke.b IS 'removed comment'", "COMMENT ON COLUMN comment_smoke.c IS 'survivor comment'"} {
		alterDropSmokeExec(t, db, q)
	}
	alterDropSmokeExec(t, db, "ALTER TABLE comment_smoke DROP b")
	var commentDump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &commentDump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(commentDump.String(), "removed comment") || !strings.Contains(commentDump.String(), `COMMENT ON TABLE "public"."comment_smoke" IS E'table keep';`) || !strings.Contains(commentDump.String(), `COMMENT ON COLUMN "public"."comment_smoke"."c" IS E'survivor comment';`) {
		t.Fatal(commentDump.String())
	}
	alterDropSmokeExec(t, db, "COMMENT ON COLUMN comment_smoke.c IS 'updated survivor'")
	commentDump.Reset()
	if err = stuntgres.Dump(ctx, db, &commentDump); err != nil || !strings.Contains(commentDump.String(), `COMMENT ON COLUMN "public"."comment_smoke"."c" IS E'updated survivor';`) {
		t.Fatal(err, commentDump.String())
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(commentDump.Bytes())); err != nil {
		t.Fatal(err)
	}
	var commentsAgain bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &commentsAgain); err != nil || commentsAgain.String() != commentDump.String() {
		t.Fatal(err, commentsAgain.String())
	}
	rows, err = db.Query("SELECT * FROM comment_smoke")
	if err != nil {
		t.Fatal(err)
	}
	alterDropSmokeRows(t, rows, []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}})
	frontierUtility := func(q string) {
		t.Helper()
		result, e := db.Exec(q)
		if e != nil {
			t.Fatal(q, e)
		}
		if n, e := result.RowsAffected(); e != nil || n != 0 {
			t.Fatal(q, n, e)
		}
	}
	frontierUtility("CREATE TABLE frontier_smoke(a INT,b TEXT,c BIGINT,d INT)")
	alterDropSmokeExec(t, db, "INSERT INTO frontier_smoke VALUES(1,'removed',9,4)")
	frontierUtility("ALTER TABLE frontier_smoke DROP b")
	frontierUtility("ALTER TABLE frontier_smoke DROP d")
	frontierUtility("COMMENT ON TABLE frontier_smoke IS 'frontier table'")
	frontierComment, err := db.Prepare("COMMENT ON COLUMN frontier_smoke.c IS 'frontier column'")
	if err != nil {
		t.Fatal(err)
	}
	frontierResult, err := frontierComment.Exec()
	frontierComment.Close()
	if err != nil {
		t.Fatal(err)
	}
	if n, e := frontierResult.RowsAffected(); e != nil || n != 0 {
		t.Fatal(n, e)
	}
	checkFrontier := func() {
		t.Helper()
		r, e := db.Query("SELECT * FROM frontier_smoke")
		if e != nil {
			t.Fatal(e)
		}
		alterDropSmokeRows(t, r, []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}})
	}
	checkFrontier()
	var frontierDump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &frontierDump); err != nil {
		t.Fatal(err)
	}
	for _, literal := range []string{`CREATE TABLE "public"."frontier_smoke" ("a" INTEGER, "c" BIGINT);`, `COMMENT ON TABLE "public"."frontier_smoke" IS E'frontier table';`, `COMMENT ON COLUMN "public"."frontier_smoke"."c" IS E'frontier column';`} {
		if !strings.Contains(frontierDump.String(), literal) {
			t.Fatal("frontier canonical metadata", literal, frontierDump.String())
		}
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(frontierDump.Bytes())); err != nil {
		t.Fatal(err)
	}
	checkFrontier()
	var frontierAgain bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &frontierAgain); err != nil || frontierAgain.String() != frontierDump.String() {
		t.Fatal(err, frontierAgain.String())
	}
}

func TestAlterDropColumnZeroColumnControlsSmoke(t *testing.T) {
	var notices []stuntgres.Error
	db := alterDropSmokePool(t, &notices)
	ctx := context.Background()
	alterDropSmokeExec(t, db, "CREATE TABLE z(v TEXT)")
	alterDropSmokeExec(t, db, "INSERT INTO z VALUES('one'),('two')")
	alterDropSmokeExec(t, db, "ALTER TABLE ONLY z DROP v")
	check := func() {
		t.Helper()
		rows, err := db.Query("SELECT * FROM z")
		if err != nil {
			t.Fatal(err)
		}
		alterDropSmokeRows(t, rows, []string{}, []string{}, [][]any{{}, {}})
		stmt, err := db.Prepare("SELECT * FROM z")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err = stmt.Query()
		if err != nil {
			t.Fatal(err)
		}
		alterDropSmokeRows(t, rows, []string{}, []string{}, [][]any{{}, {}})
	}
	check()
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Count(dump.String(), `INSERT INTO "public"."z" DEFAULT VALUES;`) != 2 || !strings.Contains(dump.String(), `CREATE TABLE "public"."z" ();`) || strings.Contains(dump.String(), "VALUES ()") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check()
	_, err := db.Exec("INSERT INTO z VALUES ()")
	alterDropSmokeError(t, err, "42601", `syntax error at or near ")"`, "23")
	check()
	if err = stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE bad(v INT NOT NULL); INSERT INTO bad DEFAULT VALUES;")); err == nil {
		t.Fatal("invalid not-null defaults accepted")
	}
	check()
	var same bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &same); err != nil || same.String() != dump.String() {
		t.Fatal(err, same.String())
	}
}
