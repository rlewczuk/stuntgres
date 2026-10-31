package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func portugueseScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"dictionary_creator", "dictionary_peer", "dictionary_recipient"})
	fixture.SchemaReferenceCleanup(t, "dictionary_left", "dictionary_right", "dictionary_owned")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func portugueseSmokeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
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

func portugueseStockCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	return func() {
		if !b.PG {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "ALTER TEXT SEARCH DICTIONARY pg_catalog.portuguese_stem (language=portuguese,stopwords=portuguese)"); err != nil {
			t.Error("restore stock Portuguese options", err)
		}
	}
}

func portugueseLexizeMetadata(t *testing.T, b fixture.Backend, rows *sql.Rows) {
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
