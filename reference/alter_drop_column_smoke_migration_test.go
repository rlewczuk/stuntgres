package reference_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceAlterDropColumnSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { migrationAlterDropColumnSmoke(t, p, backend) })
	}
}
func TestReferenceAlterDropColumnZeroColumnControlsSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { migrationAlterDropColumnZeroColumnControlsSmoke(t, p, backend) })
	}
}

func migrationAlterDropColumnSmoke(t *testing.T, p *referencePair, backend referenceBackend) {
	m, db, ctx, noticeRecords := migrationAlterFixture(t, p, backend)
	m.alterExec(db, "CREATE TABLE t(a INT NOT NULL,b TEXT,c BIGINT)")
	m.alterExec(db, "INSERT INTO t VALUES(1,'sentinel',9),(2,NULL,NULL)")
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
	m.dropRows(rows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	m.recordQuery(db, "SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	rows, err = survivor.Query()
	if err != nil {
		t.Fatal(err)
	}
	m.dropRows(rows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
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
		if !backend.pg && (!known || nullable != (i == 1)) {
			t.Fatal(c, nullable, known)
		}
	}
	m.dropRows(freshRows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	_, err = star.Query()
	m.alterError(err, "0A000", "cached plan must not change result type", "")
	m.alterExec(db, "ALTER TABLE t DROP COLUMN IF EXISTS missing")
	expectedNotice := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `column "missing" of relation "t" does not exist, skipping`}}
	if !reflect.DeepEqual(*noticeRecords, expectedNotice) {
		t.Fatal(*noticeRecords)
	}
	_, err = db.Exec("ALTER TABLE t DROP missing")
	m.alterError(err, "42703", `column "missing" of relation "t" does not exist`, "")
	m.alterExec(db, "INSERT INTO t VALUES(3,30)")
	insert, err := db.Prepare("INSERT INTO t(c,a) VALUES($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = insert.Exec(40, 4); err != nil {
		t.Fatal(err)
	}
	insert.Close()
	want = append(want, []any{int64(3), int64(30)}, []any{int64(4), int64(40)})
	m.alterExec(db, "CREATE TABLE only_table(a INT,b TEXT)")
	m.alterExec(db, "INSERT INTO only_table VALUES(7,'gone')")
	m.alterExec(db, "ALTER TABLE ONLY(only_table) DROP b CASCADE")
	rows, err = db.Query("SELECT * FROM only_table")
	if err != nil {
		t.Fatal(err)
	}
	m.dropRows(rows, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	m.recordQuery(db, "SELECT * FROM only_table", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	var dump bytes.Buffer
	if !backend.pg {
		if err = stuntgres.Dump(ctx, p.native, &dump); err != nil || strings.Contains(dump.String(), "sentinel") {
			t.Fatal(err, dump.String())
		}

	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = db.ExecContext(canceled, "ALTER TABLE t DROP c")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !backend.pg {
		var same bytes.Buffer
		if err = stuntgres.Dump(ctx, p.native, &same); err != nil || same.String() != dump.String() {
			t.Fatal(err)
		}
		if err = stuntgres.Reset(ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}

	}
	rows, err = db.Query("SELECT * FROM t ORDER BY a")
	if err != nil {
		t.Fatal(err)
	}
	m.dropRows(rows, []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	m.recordQuery(db, "SELECT * FROM t ORDER BY a", []string{"a", "c"}, []string{"INT4", "INT8"}, want)
	m.alterExec(db, `CREATE TABLE quoted_smoke("q""u\z" INT,a INT)`)
	m.alterExec(db, `INSERT INTO quoted_smoke VALUES(1,7)`)
	quoted, err := db.Prepare(`/*é*/ SELECT "q""u\z" FROM quoted_smoke`)
	if err != nil {
		t.Fatal(err)
	}
	defer quoted.Close()
	m.alterExec(db, `ALTER TABLE quoted_smoke DROP "q""u\z"`)
	_, err = quoted.Query()
	m.alterError(err, "42703", `column "q"u\z" does not exist`, "14")
	_, err = db.Prepare(`/*é*/ SELECT "q""u\z" FROM quoted_smoke`)
	m.alterError(err, "42703", `column "q"u\z" does not exist`, "14")
	rows, err = db.Query("SELECT * FROM quoted_smoke")
	if err != nil {
		t.Fatal(err)
	}
	m.dropRows(rows, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	m.recordQuery(db, "SELECT * FROM quoted_smoke", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	db.SetMaxOpenConns(1)
	if backend.pg {
		t.Cleanup(func() {
			cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			if _, e := referenceAdmin.ExecContext(cleanup, "DROP SCHEMA IF EXISTS migration_owner_smoke CASCADE; DROP ROLE IF EXISTS migration_drop_owner"); e != nil {
				t.Error(e)
			}
		})
	}
	m.alterExec(db, "CREATE USER migration_drop_owner")
	m.alterExec(db, "CREATE SCHEMA migration_owner_smoke AUTHORIZATION migration_drop_owner")
	m.alterExec(db, "SET SESSION AUTHORIZATION migration_drop_owner")
	m.alterExec(db, "CREATE TABLE migration_owner_smoke.t(a INT,b TEXT,c INT)")
	m.alterExec(db, "INSERT INTO migration_owner_smoke.t VALUES(7,'gone',9)")
	m.alterExec(db, "ALTER TABLE migration_owner_smoke.t DROP b")
	ownerDrop, err := db.Prepare("ALTER TABLE migration_owner_smoke.t DROP c")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ownerDrop.Exec(); err != nil {
		t.Fatal(err)
	}
	ownerDrop.Close()
	m.alterExec(db, "RESET SESSION AUTHORIZATION")
	var ownerDump bytes.Buffer
	if !backend.pg {
		if err = stuntgres.Dump(ctx, p.native, &ownerDump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(ownerDump.String(), `CREATE SCHEMA "migration_owner_smoke" AUTHORIZATION "migration_drop_owner";`) || !strings.Contains(ownerDump.String(), `SET SESSION AUTHORIZATION 'migration_drop_owner';`) {
			t.Fatal(ownerDump.String())
		}
		if err = stuntgres.Reset(ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, p.native, bytes.NewReader(ownerDump.Bytes())); err != nil {
			t.Fatal(err)
		}

	}
	m.alterExec(db, "SET SESSION AUTHORIZATION migration_drop_owner")
	rows, err = db.Query("SELECT * FROM migration_owner_smoke.t")
	if err != nil {
		t.Fatal(err)
	}
	m.dropRows(rows, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	m.recordQuery(db, "SELECT * FROM migration_owner_smoke.t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
	m.alterExec(db, "ALTER TABLE migration_owner_smoke.t DROP IF EXISTS b")
	m.alterExec(db, "RESET SESSION AUTHORIZATION")
	if !backend.pg {
		var ownerAgain bytes.Buffer
		if err = stuntgres.Dump(ctx, p.native, &ownerAgain); err != nil || ownerAgain.String() != ownerDump.String() {
			t.Fatal(err, ownerAgain.String())
		}

	}
	m.alterExec(db, "CREATE TABLE comment_smoke(a INT,b TEXT,c BIGINT)")
	m.alterExec(db, "INSERT INTO comment_smoke VALUES(1,'removed',9)")
	for _, q := range []string{"COMMENT ON TABLE comment_smoke IS 'table keep'", "COMMENT ON COLUMN comment_smoke.b IS 'removed comment'", "COMMENT ON COLUMN comment_smoke.c IS 'survivor comment'"} {
		m.alterExec(db, q)
	}
	m.alterExec(db, "ALTER TABLE comment_smoke DROP b")
	var commentDump bytes.Buffer
	if !backend.pg {

		if err = stuntgres.Dump(ctx, p.native, &commentDump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(commentDump.String(), "removed comment") || !strings.Contains(commentDump.String(), `COMMENT ON TABLE "public"."comment_smoke" IS E'table keep';`) || !strings.Contains(commentDump.String(), `COMMENT ON COLUMN "public"."comment_smoke"."c" IS E'survivor comment';`) {
			t.Fatal(commentDump.String())
		}

	}
	m.alterExec(db, "COMMENT ON COLUMN comment_smoke.c IS 'updated survivor'")
	if !backend.pg {
		commentDump.Reset()
		if err = stuntgres.Dump(ctx, p.native, &commentDump); err != nil || !strings.Contains(commentDump.String(), `COMMENT ON COLUMN "public"."comment_smoke"."c" IS E'updated survivor';`) {
			t.Fatal(err, commentDump.String())
		}
		if err = stuntgres.Reset(ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, p.native, bytes.NewReader(commentDump.Bytes())); err != nil {
			t.Fatal(err)
		}
		var commentsAgain bytes.Buffer
		if err = stuntgres.Dump(ctx, p.native, &commentsAgain); err != nil || commentsAgain.String() != commentDump.String() {
			t.Fatal(err, commentsAgain.String())
		}

	}
	rows, err = db.Query("SELECT * FROM comment_smoke")
	if err != nil {
		t.Fatal(err)
	}
	m.dropRows(rows, []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}})
	m.recordQuery(db, "SELECT * FROM comment_smoke", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}})
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
	m.alterExec(db, "INSERT INTO frontier_smoke VALUES(1,'removed',9,4)")
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
		m.dropRows(r, []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}})
		m.recordQuery(db, "SELECT * FROM frontier_smoke", []string{"a", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(9)}})
	}
	checkFrontier()
	var frontierDump bytes.Buffer
	if !backend.pg {
		if err = stuntgres.Dump(ctx, p.native, &frontierDump); err != nil {
			t.Fatal(err)
		}
		for _, literal := range []string{`CREATE TABLE "public"."frontier_smoke" ("a" INTEGER, "c" BIGINT);`, `COMMENT ON TABLE "public"."frontier_smoke" IS E'frontier table';`, `COMMENT ON COLUMN "public"."frontier_smoke"."c" IS E'frontier column';`} {
			if !strings.Contains(frontierDump.String(), literal) {
				t.Fatal("frontier canonical metadata", literal, frontierDump.String())
			}
		}
		if err = stuntgres.Reset(ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(ctx, p.native, bytes.NewReader(frontierDump.Bytes())); err != nil {
			t.Fatal(err)
		}

	}
	checkFrontier()
	if !backend.pg {
		var frontierAgain bytes.Buffer
		if err = stuntgres.Dump(ctx, p.native, &frontierAgain); err != nil || frontierAgain.String() != frontierDump.String() {
			t.Fatal(err, frontierAgain.String())
		}
	}

}

func migrationAlterDropColumnZeroColumnControlsSmoke(t *testing.T, p *referencePair, backend referenceBackend) {
	m, db, ctx, _ := migrationAlterFixture(t, p, backend)
	m.alterExec(db, "CREATE TABLE z(v TEXT)")
	m.alterExec(db, "INSERT INTO z VALUES('one'),('two')")
	m.alterExec(db, "ALTER TABLE ONLY z DROP v")
	check := func() {
		t.Helper()
		rows, err := db.Query("SELECT * FROM z")
		if err != nil {
			t.Fatal(err)
		}
		m.dropRows(rows, []string{}, []string{}, [][]any{{}, {}})
		stmt, err := db.Prepare("SELECT * FROM z")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err = stmt.Query()
		if err != nil {
			t.Fatal(err)
		}
		m.dropRows(rows, []string{}, []string{}, [][]any{{}, {}})
		m.recordQuery(db, "SELECT * FROM z", []string{}, []string{}, [][]any{{}, {}})
	}
	check()
	var dump bytes.Buffer
	if !backend.pg {

		if err := stuntgres.Dump(ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Count(dump.String(), `INSERT INTO "public"."z" DEFAULT VALUES;`) != 2 || !strings.Contains(dump.String(), `CREATE TABLE "public"."z" ();`) || strings.Contains(dump.String(), "VALUES ()") {
			t.Fatal(dump.String())
		}
		if err := stuntgres.Reset(ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}

	}
	check()
	_, err := db.Exec("INSERT INTO z VALUES ()")
	m.alterError(err, "42601", `syntax error at or near ")"`, "23")
	check()
	if !backend.pg {
		if err = stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE bad(v INT NOT NULL); INSERT INTO bad DEFAULT VALUES;")); err == nil {
			t.Fatal("invalid not-null defaults accepted")
		}
		check()
		var same bytes.Buffer
		if err = stuntgres.Dump(ctx, p.native, &same); err != nil || same.String() != dump.String() {
			t.Fatal(err, same.String())
		}
	}
}
