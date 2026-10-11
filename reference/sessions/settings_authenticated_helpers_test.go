package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"testing"
)

func settingsAuthenticatedPool(t *testing.T, d *stuntgres.Driver, dsn, name string) *sql.DB {
	t.Helper()
	c, err := d.OpenConnectorAs(dsn, name)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(c)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func settingsAuthenticatedCommand(t *testing.T, c *sql.Conn, query string, count int64, prepared bool) {
	t.Helper()
	var result sql.Result
	var err error
	if prepared {
		s, prepareErr := c.PrepareContext(context.Background(), query)
		if prepareErr != nil {
			t.Fatal(query, prepareErr)
		}
		defer s.Close()
		result, err = s.ExecContext(context.Background())
	} else {
		result, err = c.ExecContext(context.Background(), query)
	}
	if err != nil {
		t.Fatal(query, err)
	}
	got, err := result.RowsAffected()
	if err != nil || got != count {
		t.Fatal(query, got, err)
	}
}
func settingsAuthenticatedIdentity(t *testing.T, c *sql.Conn, want string, held *sql.Stmt) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if held == nil {
		rows, err = c.QueryContext(context.Background(), "SELECT session_user,current_user,session_user")
	} else {
		rows, err = held.QueryContext(context.Background())
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(cols, []string{"session_user", "current_user", "session_user"}) {
		t.Fatal(cols, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != reflect.TypeFor[string]() {
			t.Fatal(typ)
		}
	}
	if !rows.Next() {
		t.Fatal("missing identity", rows.Err())
	}
	var a, b, cName string
	if err := rows.Scan(&a, &b, &cName); err != nil || a != want || b != want || cName != want {
		t.Fatal(a, b, cName, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra/error identity", rows.Err())
	}
}
