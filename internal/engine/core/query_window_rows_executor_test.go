package core_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowRowsEndpoints(t *testing.T) {
	for _, tc := range []struct {
		kind   parser.FrameBoundKind
		offset int64
		want   [3][2]int
	}{
		{parser.FrameUnboundedPreceding, 0, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameUnboundedFollowing, 0, [3][2]int{{3, 3}, {3, 3}, {3, 3}}},
		{parser.FrameCurrentRow, 0, [3][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{parser.FrameOffsetPreceding, 0, [3][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{parser.FrameOffsetPreceding, 1, [3][2]int{{0, 0}, {0, 1}, {1, 2}}},
		{parser.FrameOffsetPreceding, 2, [3][2]int{{0, 0}, {0, 0}, {0, 1}}},
		{parser.FrameOffsetPreceding, 100, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameOffsetPreceding, math.MaxInt64, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameOffsetFollowing, 0, [3][2]int{{0, 1}, {1, 2}, {2, 3}}},
		{parser.FrameOffsetFollowing, 1, [3][2]int{{1, 2}, {2, 3}, {3, 3}}},
		{parser.FrameOffsetFollowing, 2, [3][2]int{{2, 3}, {3, 3}, {3, 3}}},
		{parser.FrameOffsetFollowing, 100, [3][2]int{{3, 3}, {3, 3}, {3, 3}}},
		{parser.FrameOffsetFollowing, math.MaxInt64, [3][2]int{{3, 3}, {3, 3}, {3, 3}}},
	} {
		for row, want := range tc.want {
			for side, upper := range []bool{false, true} {
				got, err := exec.WindowRowsEndpoint(context.Background(), tc.kind, tc.offset, row, 3, upper)
				if err != nil || got != want[side] {
					t.Fatal(tc, row, upper, got, err)
				}
			}
		}
	}
	for _, tc := range []struct {
		kind   parser.FrameBoundKind
		offset int64
		want   [2]int
	}{
		{parser.FrameCurrentRow, 0, [2]int{0, 1}},
		{parser.FrameOffsetPreceding, math.MaxInt64, [2]int{0, 0}},
		{parser.FrameOffsetFollowing, math.MaxInt64, [2]int{1, 1}},
	} {
		for side, upper := range []bool{false, true} {
			got, err := exec.WindowRowsEndpoint(context.Background(), tc.kind, tc.offset, 0, 1, upper)
			if err != nil || got != tc.want[side] {
				t.Fatal(tc, got, err)
			}
		}
	}
	var usage sqltype.Usage
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			usage = typ.Usage()
		}
	}
	v, err := exec.WindowFrameCount(context.Background(), usage, 3, 2)
	n, integerErr := v.Integer()
	if err != nil || integerErr != nil || n != 0 {
		t.Fatal(v, err, integerErr)
	}
}

func TestQueryWindowRowsCancellation(t *testing.T) {
	for _, phase := range []string{"key", "prepare", "hash", "equal", "sort", "rows-offset", "rows-validation", "rows-frame", "count", "output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			q := "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id),count(*) OVER(PARTITION BY v ORDER BY id ROWS BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t"
			query, source := enginetest.QueryFixture(t, engine.New(), q)
			continuation, err := exec.CaptureSource(context.Background(), source.View, source.Ref)
			if err != nil {
				t.Fatal(err)
			}
			defer continuation.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &windowCancelContext{Context: ctx, cancel: cancel, phase: phase}
			rows, count, err := exec.Finalize(probe, query.Root, source, nil)
			if !probe.entered || !errors.Is(err, context.Canceled) || rows != nil || count != 0 || source.View != nil {
				t.Fatal(probe.entered, rows, count, err, source.View)
			}
			source.Close()
			source.Close()
			recovered, n, err := exec.Finalize(context.Background(), query.Root, continuation, nil)
			if err != nil || recovered == nil || n != 4 {
				t.Fatal(recovered, n, err)
			}
			recovered.Close()
			recovered.Close()
		})
	}
}

func TestQueryWindowRowsMetadata(t *testing.T) {
	e := engine.New()
	query, source := enginetest.QueryFixture(t, e, "SELECT id,count(*) OVER(ORDER BY id ROWS 1 PRECEDING) AS n FROM t")
	defer source.Close()
	schema := query.Root.Schema()
	if schema[0].OriginTable == 0 || schema[0].OriginAttribute != 1 {
		t.Fatal(schema)
	}
	column := schema[1]
	if column.Usage.Type.ID() != sqltype.Int8 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || column.Nullable || !column.NullabilityKnown {
		t.Fatal(column)
	}
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || rows == nil || count != 4 {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	tuple := make([]sqltype.Value, 2)
	for i, want := range []int64{1, 2, 2, 2} {
		present, err := rows.Next(context.Background(), tuple)
		id, _ := tuple[0].Integer()
		n, _ := tuple[1].Integer()
		if !present || err != nil || id != int64(i+1) || n != want {
			t.Fatal(present, err, id, n)
		}
	}
	rows.Close()
	rows.Close()
	if source.View != nil || source.WindowValues != nil || source.WindowIndices != nil || source.Input != nil || source.Scratch != nil {
		t.Fatal("overlay/pin resources survived close")
	}
}

func TestQueryWindowRowsFinishAtomicity(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE s(id INT)")
	enginetest.Run(t, e, "INSERT INTO s VALUES(1),(2),(3)")
	enginetest.Run(t, e, "CREATE TABLE d(n BIGINT)")
	enginetest.Run(t, e, "INSERT INTO d VALUES(99)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO d SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING) FROM s"), 0, nil)
	if err != nil || result.Count != 3 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !scope.Finished || scope.View != nil || scope.LockTicket != nil {
		t.Fatal("statement resources survived Finish")
	}
	current := enginetest.Run(t, e, "SELECT n FROM d").Rows
	defer current.Close()
	tuple := make([]sqltype.Value, 1)
	present, err := current.Next(context.Background(), tuple)
	value, _ := tuple[0].Integer()
	if !present || err != nil || value != 99 {
		t.Fatal(present, err, value)
	}
	if present, err := current.Next(context.Background(), tuple); present || err != nil {
		t.Fatal(present, err)
	}
	enginetest.Run(t, e, "INSERT INTO d SELECT count(*) OVER(ORDER BY id ROWS 1 PRECEDING) FROM s")
}
