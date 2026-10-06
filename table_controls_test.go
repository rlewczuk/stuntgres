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

func TestTableCoreControlsExpandedLikeAndZeroShape(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, `CREATE TABLE "source.dot"("i.dot" INT NOT NULL,b BIGINT,s TEXT,f BOOL)`)
	execSQL(t, db, `INSERT INTO "source.dot" VALUES(1,2,'source',FALSE)`)
	execSQL(t, db, `CREATE TABLE "clone.dot"(LIKE "source.dot")`)
	text := "O'Brien\n\\tail"
	execSQL(t, db, `INSERT INTO "clone.dot" VALUES($1,$2,$3,$4)`, 7, int64(9223372036854775807), text, true)
	execSQL(t, db, `INSERT INTO "clone.dot" VALUES(8,NULL,NULL,NULL)`)
	execSQL(t, db, `CREATE TABLE z()`)
	execSQL(t, db, `CREATE TABLE zl(LIKE z)`)
	execSQL(t, db, `DROP TABLE "source.dot" RESTRICT`)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "LIKE") {
		t.Fatal(dump.String())
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	_, err := db.Query(`SELECT * FROM "clone.dot"`)
	sqlState(t, err, "42P01")
	if err = stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	want := [][]any{{int64(7), int64(9223372036854775807), text, true}, {int64(8), nil, nil, nil}}
	wantTuples(t, db, `SELECT * FROM "clone.dot" ORDER BY "i.dot"`, want)
	alterDropMetadata(t, db, false, `SELECT * FROM "clone.dot"`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{false, true, true, true}, 2)
	_, err = db.Exec(`INSERT INTO "clone.dot" VALUES(NULL,NULL,NULL,NULL)`)
	sqlState(t, err, "23502")
	for _, z := range []string{"z", "zl"} {
		tableCoreMetadata(t, db, true, "SELECT * FROM "+z, []string{}, []string{}, []bool{})
	}
	var redump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
		t.Fatal(redump.String(), err)
	}
	for _, script := range []string{"CREATE TABLE replacement(LIKE missing)", "CREATE TABLE replacement(id INT,id TEXT)", "CREATE TABLE replacement(id no_such_type)", "CREATE TABLE replacement(LIKE z INCLUDING ALL)", `CREATE TABLE replacement(id INT); DROP TABLE replacement`} {
		if err = stuntgres.Restore(ctx, db, strings.NewReader(script)); err == nil {
			t.Fatal("invalid restore accepted", script)
		}
		wantTuples(t, db, `SELECT * FROM "clone.dot" ORDER BY "i.dot"`, want)
		redump.Reset()
		if err = stuntgres.Dump(ctx, db, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("failed restore changed target", err, redump.String())
		}
	}
	// Observe identity and source absence through public behavior, not dump bytes.
	tableCoreExec(t, db, true, `ALTER TABLE "clone.dot" DROP CONSTRAINT "source.dot_i.dot_not_null"`, 0)
	alterDropMetadata(t, db, true, `SELECT * FROM "clone.dot"`, []string{"i.dot", "b", "s", "f"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, []bool{true, true, true, true}, 2)
	wantTuples(t, db, `SELECT * FROM "clone.dot" ORDER BY "i.dot"`, want)
	tableCoreExec(t, db, true, `INSERT INTO "clone.dot" VALUES(NULL,99,'released',FALSE)`, 1)
	wantTuples(t, db, `SELECT * FROM "clone.dot" WHERE b=99`, [][]any{{nil, int64(99), "released", false}})
	_, err = db.Query(`SELECT * FROM "source.dot"`)
	notNullDiagnostic(t, err, stuntgres.Error{Code: "42P01", Message: `relation "source.dot" does not exist`, Position: "15"})
}
func TestTableCorePublicNoticesSemanticSkipAndRoleCleanup(t *testing.T) {
	d := stuntgres.NewDriver()
	base, err := d.OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db := sql.OpenDB(base.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { notices = append(notices, n) }))
	defer db.Close()
	db.SetMaxOpenConns(1)
	execSQL(t, db, "CREATE TABLE t(id INT)")
	execSQL(t, db, "INSERT INTO t VALUES(11)")
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"CREATE TABLE IF NOT EXISTS t(id no_such_type)", "CREATE TABLE IF NOT EXISTS t(id SMALLINT)", "CREATE TABLE IF NOT EXISTS t(id NAME)", "CREATE TABLE IF NOT EXISTS t(LIKE missing)", "CREATE TABLE IF NOT EXISTS t(id INT,id TEXT)", "CREATE TABLE IF NOT EXISTS t(LIKE missing EXCLUDING CONSTRAINTS)"} {
			notices = nil
			tableCoreExec(t, db, prepared, q, 0)
			want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(11)}})
		}
	}
	notices = nil
	_, err = db.Exec("CREATE TABLE IF NOT EXISTS t(id no_such_type,)")
	sqlState(t, err, "42601")
	if len(notices) != 0 {
		t.Fatal("malformed skip emitted notice", notices)
	}
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(11)}})
	notices = nil
	execSQL(t, db, "SET client_min_messages TO warning")
	tableCoreExec(t, db, true, "CREATE TABLE IF NOT EXISTS t(id unknown_type)", 0)
	if len(notices) != 0 {
		t.Fatal(notices)
	}
	execSQL(t, db, "RESET client_min_messages")
	notices = nil
	tableCoreExec(t, db, true, "DROP TABLE IF EXISTS missing,absent_schema.t", 0)
	want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "absent_schema" does not exist, skipping`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices, want)
	}
	execSQL(t, db, "CREATE USER table_owner")
	execSQL(t, db, "SET SESSION AUTHORIZATION table_owner")
	execSQL(t, db, "CREATE TABLE owned(id INT)")
	execSQL(t, db, "RESET SESSION AUTHORIZATION")
	_, err = db.Exec("DROP ROLE table_owner")
	sqlState(t, err, "2BP01")
	execSQL(t, db, "DROP TABLE owned CASCADE")
	execSQL(t, db, "DROP ROLE table_owner")
	wantTuples(t, db, "SELECT * FROM t", [][]any{{int64(11)}})
}
