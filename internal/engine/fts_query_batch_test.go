package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// This is a low-level RowCursor demand trace, not a SQL rule executor or SQL
// acceptance oracle. Public SQLDemand scenarios exercise actual query effects.
type rewriteBatchUnitCursor struct {
	schema                               []catalog.Column
	total, produced, nextCalls, closes   int
	deleteAt, poisonAt, failAt, cancelAt int
	match, inert, empty                  sqltype.Value
	failure, closeFailure                error
	cancel                               context.CancelFunc
}

func (r *rewriteBatchUnitCursor) Schema() []catalog.Column { return r.schema }
func (r *rewriteBatchUnitCursor) Next(ctx context.Context, row []sqltype.Value) (bool, error) {
	r.nextCalls++
	if len(row) != len(r.schema) {
		return false, fmt.Errorf("wrong scratch width %d", len(row))
	}
	if r.nextCalls == r.cancelAt {
		r.cancel()
		return false, ctx.Err()
	}
	if r.nextCalls == r.failAt {
		return false, r.failure
	}
	if r.produced == r.total {
		return false, nil
	}
	r.produced++
	for i, column := range r.schema {
		row[i] = sqltype.Null(column.Usage.Type)
	}
	if len(row) == 2 && r.schema[0].Usage.Type == r.match.Type() {
		row[0], row[1] = r.inert, r.empty
		if r.produced == r.deleteAt {
			row[0] = r.match
		}
		if r.produced == r.poisonAt {
			row[0] = sqltype.Value{}
		}
	}
	return true, nil
}
func (r *rewriteBatchUnitCursor) Close() error { r.closes++; return r.closeFailure }

func rewriteBatchUnit(t *testing.T, total int, ctx context.Context) (*rewriteBatchUnitCursor, *sqltype.FTSRewriteRules, *sqltype.Descriptor) {
	t.Helper()
	var query, integer *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.TSQuery {
			query = d
		}
		if d.ID() == sqltype.Int4 {
			integer = d
		}
	}
	input := func(text string) sqltype.Value {
		v, err := query.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	r := &rewriteBatchUnitCursor{schema: []catalog.Column{{Name: "target", Usage: query.Usage()}, {Name: "substitute", Usage: query.Usage()}}, total: total, match: input("a"), inert: input("inert"), empty: input("")}
	accumulator, err := sqltype.NewFTSRewriteRules(ctx, r.match, query)
	if err != nil {
		t.Fatal(err)
	}
	return r, accumulator, integer
}

func TestFTSQueryFunctionsBatchDemand(t *testing.T) {
	for _, tc := range []struct{ total, deletion, poison, demand int }{
		{401, 1, 2, 200}, {401, 100, 101, 200},
		{401, 101, 102, 300}, {401, 200, 201, 300},
		{401, 201, 202, 400}, {100, 1, 2, 100},
	} {
		t.Run(fmt.Sprintf("delete_%d_total_%d", tc.deletion, tc.total), func(t *testing.T) {
			ctx := context.Background()
			rows, accumulator, _ := rewriteBatchUnit(t, tc.total, ctx)
			rows.deleteAt, rows.poisonAt = tc.deletion, tc.poison
			value, err := consumeRewriteRules(ctx, rows, accumulator)
			if err != nil {
				t.Fatal(err)
			}
			if empty, err := value.EmptyTSQuery(); err != nil || !empty {
				t.Fatal("deletion result", value, err)
			}
			if rows.produced != tc.demand || rows.closes != 1 {
				t.Fatal("bounded demand/close", rows.produced, rows.closes, tc.demand)
			}
			if tc.total > tc.demand && rows.nextCalls != tc.demand {
				t.Fatal("extra projected EOF probe", rows.nextCalls)
			}
		})
	}
	ctx := context.Background()
	rows, accumulator, _ := rewriteBatchUnit(t, 201, ctx)
	value, err := consumeRewriteRules(ctx, rows, accumulator)
	if err != nil || rows.produced != 201 || rows.closes != 1 {
		t.Fatal("all nondeleting rule batches", rows.produced, err)
	}
	text, err := value.Type().Output(ctx, value)
	if err != nil || text != "'a'" {
		t.Fatal("inert rules changed original", text, err)
	}
}

func TestFTSQueryFunctionsBatchShapePrecedence(t *testing.T) {
	for _, tc := range []struct {
		width, total, failureAt, demand int
		code                            string
	}{
		{0, 5, 0, 5, "22023"}, {0, 201, 0, 100, "22023"},
		{1, 0, 0, 0, "22023"}, {1, 201, 0, 100, "22023"},
		{2, 201, 1, 0, "22012"}, {2, 201, 100, 99, "22012"},
		{2, 201, 101, 100, "22023"}, {3, 201, 0, 100, "22023"},
	} {
		t.Run(fmt.Sprintf("width_%d_fail_%d_total_%d", tc.width, tc.failureAt, tc.total), func(t *testing.T) {
			ctx := context.Background()
			rows, accumulator, integer := rewriteBatchUnit(t, tc.total, ctx)
			rows.schema = make([]catalog.Column, tc.width)
			for i := range rows.schema {
				rows.schema[i].Usage = integer.Usage()
			}
			failure := sqlError("22012", "division by zero")
			rows.failAt, rows.failure = tc.failureAt, failure
			rows.closeFailure = errors.New("shape cleanup")
			value, err := consumeRewriteRules(ctx, rows, accumulator)
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != tc.code || value.Type() != nil {
				t.Fatal("shape/fetch precedence", value, err)
			}
			if tc.code == "22012" && err != failure {
				t.Fatal("fetch diagnostic replaced", err)
			}
			if rows.produced != tc.demand || rows.closes != 1 {
				t.Fatal("shape fetch demand/close", rows.produced, rows.closes)
			}
			if rows.total > 100 && tc.failureAt != 1 && tc.failureAt != 100 && rows.nextCalls != 100 {
				t.Fatal("shape read beyond first fetch", rows.nextCalls)
			}
		})
	}
}

func TestFTSQueryFunctionsBatchFailureOwnership(t *testing.T) {
	for _, failAt := range []int{1, 100, 101, 200, 201} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			ctx := context.Background()
			rows, accumulator, _ := rewriteBatchUnit(t, 201, ctx)
			rows.deleteAt, rows.failAt = 1, failAt
			failure, closeFailure := errors.New("fetch"), errors.New("close")
			rows.failure, rows.closeFailure = failure, closeFailure
			value, err := consumeRewriteRules(ctx, rows, accumulator)
			if failAt <= 200 {
				if err != failure || value.Type() != nil {
					t.Fatal("primary fetch error lost", value, err)
				}
			} else if err != closeFailure || value.Type() != nil {
				t.Fatal("early-stop close error lost", value, err)
			}
			if rows.closes != 1 {
				t.Fatal("cursor close count", rows.closes)
			}
		})
	}
	for _, cancelAt := range []int{50, 150} {
		ctx, cancel := context.WithCancel(context.Background())
		rows, accumulator, _ := rewriteBatchUnit(t, 201, ctx)
		rows.deleteAt, rows.cancelAt, rows.cancel = 1, cancelAt, cancel
		value, err := consumeRewriteRules(ctx, rows, accumulator)
		cancel()
		if !errors.Is(err, context.Canceled) || value.Type() != nil || rows.closes != 1 {
			t.Fatal("fill cancellation ownership", cancelAt, value, rows.closes, err)
		}
	}
}
