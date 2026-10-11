package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func insertReturningPrecanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
	t.Helper()
	q := "INSERT INTO t VALUES(7,'canceled') RETURNING old.id,new.id"
	var x fixture.Executor = b.DB
	if prepared {
		held, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		x = fixture.HeldStatement{Executor: b.DB, Statement: held, SQL: q}
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := x.QueryContext(ctx, q)
	if rows != nil {
		rows.Close()
		t.Fatal("precanceled Query exposed cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled Query", err)
	}
	result, err := x.ExecContext(ctx, q)
	if result != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precanceled Exec", result, err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id,v FROM t", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "sentinel"}}))
}
