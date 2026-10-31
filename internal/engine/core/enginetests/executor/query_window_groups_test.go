package executor_test

import (
	"context"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQueryWindowGroupsEndpoints(t *testing.T) {
	// Each pair is independently specified head/tail for groups [0,1,2]
	// having complete row spans [0,2),[2,3),[3,5).
	for _, tc := range []struct {
		kind   parser.FrameBoundKind
		offset int64
		want   [3][2]int
	}{
		{parser.FrameUnboundedPreceding, 0, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameUnboundedFollowing, 0, [3][2]int{{5, 5}, {5, 5}, {5, 5}}},
		{parser.FrameCurrentRow, 0, [3][2]int{{0, 2}, {2, 3}, {3, 5}}},
		{parser.FrameOffsetPreceding, 0, [3][2]int{{0, 2}, {2, 3}, {3, 5}}},
		{parser.FrameOffsetPreceding, 1, [3][2]int{{0, 0}, {0, 2}, {2, 3}}},
		{parser.FrameOffsetPreceding, 2, [3][2]int{{0, 0}, {0, 0}, {0, 2}}},
		{parser.FrameOffsetPreceding, math.MaxInt64, [3][2]int{{0, 0}, {0, 0}, {0, 0}}},
		{parser.FrameOffsetFollowing, 0, [3][2]int{{0, 2}, {2, 3}, {3, 5}}},
		{parser.FrameOffsetFollowing, 1, [3][2]int{{2, 3}, {3, 5}, {5, 5}}},
		{parser.FrameOffsetFollowing, 2, [3][2]int{{3, 5}, {5, 5}, {5, 5}}},
		{parser.FrameOffsetFollowing, math.MaxInt64, [3][2]int{{5, 5}, {5, 5}, {5, 5}}},
	} {
		for group, want := range tc.want {
			for side, upper := range []bool{false, true} {
				got, err := exec.WindowGroupsEndpoint(context.Background(), tc.kind, tc.offset, []int{2, 3, 5}, group, upper)
				if err != nil || got != want[side] {
					t.Fatal(tc, group, upper, got, err)
				}
			}
		}
	}
	usage := sqltype.Builtins()[0].Usage()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int8 {
			usage = typ.Usage()
		}
	}
	value, err := exec.WindowFrameCount(context.Background(), usage, 3, 2)
	n, integerErr := value.Integer()
	if err != nil || integerErr != nil || n != 0 {
		t.Fatal(value, err, integerErr)
	}
}

func TestQueryWindowGroupsCancellation(t *testing.T) {
	for _, phase := range []string{"key", "prepare", "hash", "equal", "sort", "peer", "groups-offset", "groups-validation", "groups-frame", "count", "output", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			q := "SELECT id,row_number() OVER(PARTITION BY v ORDER BY id),count(*) OVER(PARTITION BY v ORDER BY id GROUPS BETWEEN 1 PRECEDING AND 1 FOLLOWING) FROM t"
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

func TestQueryWindowGroupsMetadata(t *testing.T) {
	e := engine.New()
	query, source := enginetest.QueryFixture(t, e, "SELECT id,count(*) OVER(ORDER BY v GROUPS 1 PRECEDING) AS n FROM t")
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
	for i, want := range []int64{2, 2, 3, 2} {
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

func TestQueryWindowGroupsFinishAtomicity(t *testing.T) {
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
	result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO d SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING) FROM s"), 0, nil)
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
	enginetest.Run(t, e, "INSERT INTO d SELECT count(*) OVER(ORDER BY id GROUPS 1 PRECEDING) FROM s")
}

// Observe real operations on the window stack and invoke an ordinary CancelFunc.
// This never substitutes a provider, adds a production hook, or depends on timing.
type windowCancelContext struct {
	context.Context
	cancel            context.CancelFunc
	phase             string
	entered, computed bool
}

func (c *windowCancelContext) Err() error {
	if !c.entered {
		var pcs [48]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		window, merge, compare, hash, equal, prepare, key, advance, finalize, output, publication := false, false, false, false, false, false, false, false, false, false, false
		offset, inrange, groups, physical, offsetEval := false, false, false, false, false
		caller := ""
		for {
			frame, more := frames.Next()
			name := frame.Function
			if caller == "" {
				caller = name
			}
			window = window || strings.HasSuffix(name, ".openWindowNode")
			merge = merge || strings.Contains(name, ".MergeQueryIndices")
			compare = compare || strings.HasSuffix(name, ".Comparison.Compare")
			hash = hash || strings.HasSuffix(name, ".Grouping.Hash")
			equal = equal || strings.HasSuffix(name, ".Grouping.Equal")
			prepare = prepare || strings.HasSuffix(name, ".Grouping.PrepareKey")
			key = key || strings.Contains(name, "ColumnExpr.Eval")
			advance = advance || strings.HasSuffix(name, ".WindowFrameEndpoint")
			offset = offset || strings.HasSuffix(name, ".evaluateWindowOffsets")
			inrange = inrange || strings.HasSuffix(name, ".RangeSupport.InRange")
			groups = groups || strings.HasSuffix(name, ".WindowGroupsEndpoint")
			physical = physical || strings.HasSuffix(name, ".WindowRowsEndpoint")
			offsetEval = offsetEval || strings.Contains(name, "LiteralExpr.Eval")
			finalize = finalize || strings.HasSuffix(name, ".WindowFrameCount")
			output = output || strings.Contains(name, "WindowExpr).Eval")
			publication = publication || strings.HasSuffix(name, "exec.Finalize")
			if !more {
				break
			}
		}
		c.computed = c.computed || window && finalize
		hit := false
		switch c.phase {
		case "key":
			hit = window && key
		case "prepare":
			hit = window && prepare
		case "hash":
			hit = window && hash
		case "equal":
			hit = window && equal
		case "sort":
			hit = window && compare && merge
		case "peer":
			hit = window && compare && !merge
		case "frame":
			hit = window && advance
		case "offset":
			hit = window && offset
		case "inrange":
			hit = window && inrange
		case "groups-offset", "rows-offset":
			hit = window && offset && offsetEval
		case "groups-validation", "rows-validation":
			// Child Eval/coercion may also check context after the literal has
			// returned. Only the direct offset-validator checkpoint qualifies.
			hit = window && strings.HasSuffix(caller, ".evaluateWindowOffsets")
		case "groups-frame":
			hit = window && groups
		case "rows-frame":
			hit = window && physical
		case "count":
			hit = window && finalize
		case "output":
			hit = output && c.computed
		case "prepublication":
			hit = publication && !window && !output && c.computed
		}
		if hit {
			c.entered = true
			c.cancel()
		}
	}
	return c.Context.Err()
}
