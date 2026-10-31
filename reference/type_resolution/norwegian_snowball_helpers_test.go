package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func norwegianSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	stmt, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, b.Recorder, s)
	}
}

func norwegianStockCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	return func() {
		if !b.PG {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.norwegian_stem (language=norwegian,stopwords=norwegian)"); err != nil {
			t.Error("restore stock Norwegian options", err)
		}
	}
}

func norwegianLexizeMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows) {
	t.Helper()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 {
		t.Fatal(types, err)
	}
	ct := types[0]
	scan := reflect.TypeFor[string]()
	if b.PG {
		scan = reflect.TypeFor[any]()
	}
	length, lk := ct.Length()
	precision, scale, dk := ct.DecimalSize()
	nullable, nk := ct.Nullable()
	if ct.DatabaseTypeName() != "_TEXT" || ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
		t.Fatalf("lexize descriptors: %#v scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", ct, ct.ScanType(), length, lk, precision, scale, dk, nullable, nk)
	}
}
