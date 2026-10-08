package stuntgres_test

import (
	"database/sql"
	"testing"
)

func insertSelectCount(t *testing.T, result sql.Result, err error, want int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != want {
		t.Fatalf("RowsAffected = %d, %v; want %d", count, err, want)
	}
	id, err := result.LastInsertId()
	if id != 0 || err == nil || err.Error() != "LastInsertId is not supported by this driver" {
		t.Fatalf("LastInsertId = %d, %v; want existing unsupported boundary", id, err)
	}
}
