package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func cycleUnitRecipe(t *testing.T, usages ...sqltype.Usage) *plan.CycleRecipe {
	t.Helper()
	c := &plan.CycleRecipe{MarkSlot: len(usages), Keys: make([]int, len(usages)), Grouping: make([]sqltype.Grouping, len(usages))}
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	c.Mark = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	c.Fallback = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}
	members := make([]sqltype.CompositeMember, len(usages))
	for i, u := range usages {
		c.Keys[i] = i
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: "key", Usage: u}
		var err error
		c.Grouping[i], err = sqltype.BindGrouping(u)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	c.Record, err = sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	c.Path, err = sqltype.Array(2287, "_record", c.Record)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func cycleUnitAugment(t *testing.T, c *plan.CycleRecipe, parent sqltype.Value, fields ...sqltype.Value) []sqltype.Value {
	t.Helper()
	boolean := c.Mark.ResultUsage().Type
	row, err := c.Augment(context.Background(), fields, parent, sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false))
	if err != nil {
		t.Fatal(err)
	}
	return row
}

type cycleUnitCancelContext struct {
	context.Context
	remaining int
}

func (c *cycleUnitCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
