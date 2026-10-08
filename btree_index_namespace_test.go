package stuntgres_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres"
)

func TestBtreeIndexPreparedNamespaces(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	db.SetMaxOpenConns(1)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE a.t(n INT)", "CREATE TABLE b.t(n INT)", "SET search_path=a", "INSERT INTO a.t VALUES(1)", "INSERT INTO b.t VALUES(2)"} {
		execSQL(t, db, q)
	}
	stmt, err := db.Prepare("CREATE INDEX idx ON t(n)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	execSQL(t, db, "SET search_path=b,a")
	if _, err := stmt.Exec(); err != nil {
		t.Fatal(err)
	}
	wantTuples(t, db, "SELECT relname FROM pg_catalog.pg_class WHERE relname='idx'", [][]any{{"idx"}})
	// Qualified keys/parents are resolved at execution, not in the Prepare path.
	qualified, err := db.Prepare("CREATE INDEX fixed ON a.t(n)")
	if err != nil {
		t.Fatal(err)
	}
	defer qualified.Close()
	if _, err := qualified.Exec(); err != nil {
		t.Fatal(err)
	}
	execSQL(t, db, "DROP TABLE b.t")
	execSQL(t, db, "CREATE TABLE b.t(n INT)")
	if _, err := stmt.Exec(); err != nil {
		t.Fatal("recreated target was not rebound", err)
	}
	execSQL(t, db, "CREATE USER reader")
	denied, err := db.Prepare("CREATE INDEX denied ON a.t(n)")
	if err != nil {
		t.Fatal(err)
	}
	defer denied.Close()
	execSQL(t, db, "SET ROLE reader")
	_, err = denied.Exec()
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42501", Message: "permission denied for schema a"})
	execSQL(t, db, "RESET ROLE")
	if _, err = denied.Exec(); err != nil {
		t.Fatal("prepared utility retained failed authority", err)
	}
}
func TestBtreeIndexIfNotExistsNotices(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	_, conn := noticePool(t, connector.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
	for _, q := range []string{"CREATE TABLE t(n INT)", "CREATE TABLE collision(n INT)", "CREATE INDEX idx ON t(n)"} {
		noticeExec(t, conn, q)
	}
	for _, name := range []string{"collision", "idx"} {
		noticeExec(t, conn, "CREATE INDEX IF NOT EXISTS "+name+" ON t(n)")
		want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "` + name + `" already exists, skipping`}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal(notices, want)
		}
		notices = nil
	}
	stmt, err := conn.PrepareContext(context.Background(), "CREATE INDEX IF NOT EXISTS idx ON t(missing)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.Exec()
	typeCoreDiagnostic(t, err, stuntgres.Error{Code: "42703", Message: `column "missing" does not exist`})
	if len(notices) != 0 {
		t.Fatal("bad key incorrectly skipped", notices)
	}
}
