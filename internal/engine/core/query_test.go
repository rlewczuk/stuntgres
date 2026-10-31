package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

// Deterministic cancellation, including comparisons taking NULL/tie shortcuts.
type queryCountingContext struct {
	context.Context
	remaining int
}

func (c *queryCountingContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func TestQueryCoreZeroWidth(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(v INT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1),(2),(3),(4)")
	enginetest.Run(t, e, "ALTER TABLE t DROP COLUMN v")
	r := enginetest.Run(t, e, "SELECT * FROM t")
	defer r.Rows.Close()
	if len(r.Rows.Schema()) != 0 || r.Count != 4 {
		t.Fatal(r)
	}
	for range 4 {
		if present, err := r.Rows.Next(context.Background(), nil); !present || err != nil {
			t.Fatal(present, err)
		}
	}
	if present, err := r.Rows.Next(context.Background(), nil); present || err != nil {
		t.Fatal(present, err)
	}
	enginetest.Run(t, e, "CREATE TABLE empty(v INT)")
	enginetest.Run(t, e, "ALTER TABLE empty DROP v")
	empty := enginetest.Run(t, e, "SELECT * FROM empty")
	defer empty.Rows.Close()
	if empty.Rows == nil || len(empty.Rows.Schema()) != 0 {
		t.Fatal(empty)
	}
	if present, err := empty.Rows.Next(context.Background(), nil); present || err != nil {
		t.Fatal(present, err)
	}
	typ, _ := e.CatalogRead().LookupType(sqltype.Text)
	anonymous, err := core.AnonymousResult([]catalog.Column{{Name: "value", Usage: typ.Usage()}}, nil, 0)
	if err != nil || anonymous.Rows == nil || len(anonymous.Rows.Schema()) != 1 {
		t.Fatal(anonymous, err)
	}
	defer anonymous.Rows.Close()
	if present, err := anonymous.Rows.Next(context.Background(), make([]sqltype.Value, 1)); present || err != nil {
		t.Fatal(present, err)
	}
}
