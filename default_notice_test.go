package stuntgres_test

import (
	"reflect"
	"stuntgres"
	"testing"
)

func TestDefaultExistingTableSkipNotice(t *testing.T) {
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	db, c := noticePool(t, connector.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
	noticeExec(t, c, `CREATE TABLE d(i INT DEFAULT 7)`)
	noticeExec(t, c, `CREATE TABLE IF NOT EXISTS d(i INT DEFAULT 'oops')`)
	want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "d" already exists, skipping`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal(notices, want)
	}
	execSQL(t, db, `INSERT INTO d DEFAULT VALUES`)
	wantTuples(t, db, `SELECT * FROM d`, [][]any{{int64(7)}})
}
