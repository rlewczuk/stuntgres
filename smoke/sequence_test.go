package smoke_test

import (
	"context"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestSequenceTemporaryPhysicalEndSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector("sequence-temp-end-smoke")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxIdleConns(0)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	for _, q := range []string{"CREATE SEQUENCE s START 40", "CREATE TEMP SEQUENCE s START 7", "BEGIN", "COMMIT"} {
		if _, err = a.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var v int64
	if err = a.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
	if err = b.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 40 {
		t.Fatal(v, err)
	}
	if err = a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err = fresh.QueryRowContext(ctx, "SELECT currval('s')").Scan(&v); err == nil {
		t.Fatal("physical session inherited memory")
	}
	if err = fresh.QueryRowContext(ctx, "SELECT nextval('s')").Scan(&v); err != nil || v != 41 {
		t.Fatal("temporary shadow survived physical end", v, err)
	}
}
