package enginetests

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Statement parameters are fixed for an admitted composite invocation. Rewind
// must nevertheless re-export them under current demand, not replay cached rows.
// Changed correlated invocations are rejected before composite discovery; the
// authentic SQL provider's changed-parameter ReScan has complementary coverage.
func TestFDWQueryStageRewind(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(map[bool]string{false: "join", true: "order_cache"}[cached], func(t *testing.T) {
			scan := &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, false, true}}
			p := &queryUnitProvider{path: queryUnitPath, scan: scan}
			state, join, owner := queryUnitTree(t, p, false)
			t.Cleanup(func() { _ = owner.Finish(context.Background()) })
			value, err := sqltype.Integer(join.Schema()[0].Usage.Type, 70)
			if err != nil {
				t.Fatal(err)
			}
			state.Parameters = []sqltype.Value{value}
			var node plan.Node = join
			if cached {
				parameter := plan.ParameterExpr{Info: expr.Info{Usage: value.Type().Usage()}, Number: 1}
				node = &plan.SortNode{QuerySchema: join.QuerySchema, Child: join, CacheWidth: 1, Keys: []plan.SortKey{{Expression: parameter, Slot: -1, Cache: 0}}}
				scan.row = catalog.ForeignRow{int64(7), int64(8), int64(70), true, false, true}
			}
			var exportContexts []context.Context
			f := state.Source.Sources[0].Foreign
			export := f.ExportValue
			f.ExportValue = func(ctx context.Context, v sqltype.Value, environment sqltype.OutputEnvironment) (driver.Value, error) {
				exportContexts = append(exportContexts, ctx)
				return export(ctx, v, environment)
			}
			first, cancelFirst := context.WithCancel(context.Background())
			defer cancelFirst()
			stage, err := exec.OpenNode(first, state, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			source := stage.Source
			ordinal, present, err := stage.NextOccurrence(first)
			if err != nil || !present || stage.Index(ordinal) != 0 {
				t.Fatal("first occurrence", ordinal, present, err)
			}
			if !reflect.DeepEqual(source.PresenceRow(0), []bool{true, false, true}) {
				t.Fatal("first presence", source.PresenceRow(0))
			}
			if _, present, err := stage.NextOccurrence(first); err != nil || present || !stage.Done {
				t.Fatal("first EOF", present, err, stage.Done)
			}
			if !reflect.DeepEqual(p.request.Parameters.Values, []driver.Value{int64(70)}) {
				t.Fatal("Begin parameters", p.request.Parameters)
			}
			cancelFirst()
			type demandKey struct{}
			current := context.WithValue(context.Background(), demandKey{}, "rewind")
			scan.rescan = func(ctx context.Context, parameters catalog.ForeignScanParameters) error {
				if ctx != current || ctx.Err() != nil || !reflect.DeepEqual(parameters.Values, []driver.Value{int64(70)}) || len(parameters.Correlation) != 0 {
					t.Fatal("current ReScan context/parameters", ctx, parameters)
				}
				scan.row = catalog.ForeignRow{nil, int64(18), false, true, true}
				if cached {
					scan.row = catalog.ForeignRow{nil, int64(18), int64(170), false, true, true}
				}
				return nil
			}
			changed, err := stage.Rewind(current)
			if err != nil || !changed || stage.Position != 0 || stage.Done || len(stage.Selection) != 0 || len(stage.OrderValues) != 0 {
				t.Fatal("reset retained first-pass demand", changed, err, stage)
			}
			if len(exportContexts) != 2 || exportContexts[0] != first || exportContexts[1] != current {
				t.Fatal("ReScan did not re-export under current context", exportContexts)
			}
			ordinal, present, err = stage.NextOccurrence(current)
			if err != nil || !present || ordinal != 0 || stage.Count() != 1 {
				t.Fatal("second pass", ordinal, present, err)
			}
			// Source occurrence identities are append-only. Reset replaces the
			// selection/cache, not old rows that may still be borrowed by parents.
			index := stage.Index(ordinal)
			row := source.Row(index)
			number, numberErr := row[1].Integer()
			if index != 1 || source.FlatCount != 2 || len(source.Anonymous) != 2 || !row[0].IsNull() || numberErr != nil || number != 18 {
				t.Fatal("stale or misaligned second-pass row", index, row, numberErr)
			}
			if !reflect.DeepEqual(source.PresenceRow(index), []bool{false, true, true}) || !reflect.DeepEqual(source.PresenceRow(0), []bool{true, false, true}) {
				t.Fatal("presence lost occurrence alignment", source.PresenceRow(index), source.PresenceRow(0))
			}
			if cached {
				cache := stage.OrderRow(ordinal)
				number, err := cache[0].Integer()
				if len(stage.OrderValues) != 1 || err != nil || number != 170 {
					t.Fatal("stale second-pass order cache", cache, err)
				}
			}
			if _, present, err := stage.NextOccurrence(current); err != nil || present {
				t.Fatal("second EOF", present, err)
			}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Finish(first); err != nil {
				t.Fatal(err)
			}
			if err := owner.Finish(first); err != nil {
				t.Fatal(err)
			}
			if p.begins != 1 || p.discoveries != 1 || scan.rescans != 1 || scan.ends != 1 || scan.cleanupCanceled {
				t.Fatal("rewind ownership", p.begins, p.discoveries, scan.rescans, scan.ends, scan.cleanupCanceled)
			}
		})
	}
}

func TestFDWQueryStageRewindFailure(t *testing.T) {
	primary, cleanup := errors.New("composite ReScan primary"), errors.New("composite End cleanup")
	for _, tc := range []struct {
		name        string
		rescan, end error
	}{
		{"rescan", primary, nil},
		{"rescan_and_end", primary, cleanup},
		{"terminal_end", nil, cleanup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scan := &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, false, true}, endError: tc.end}
			p := &queryUnitProvider{path: queryUnitPath, scan: scan}
			state, node, owner := queryUnitTree(t, p, false)
			t.Cleanup(func() { _ = owner.Finish(context.Background()) })
			stage, err := exec.OpenNode(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, present, err := stage.NextOccurrence(context.Background()); err != nil || !present {
				t.Fatal("first private row", present, err)
			}
			scan.rescan = func(context.Context, catalog.ForeignScanParameters) error { return tc.rescan }
			changed, err := stage.Rewind(context.Background())
			if tc.rescan != nil {
				if changed || !errors.Is(err, primary) || !stage.Closed || !errors.Is(stage.Terminal, primary) || stage.Source != nil || len(stage.Selection) != 0 {
					t.Fatal("failed ReScan retained partial output", changed, err, stage)
				}
				if _, present, err := stage.NextOccurrence(context.Background()); present || !errors.Is(err, primary) {
					t.Fatal("failed ReScan replayed a prefix", present, err)
				}
			} else {
				if !changed || err != nil {
					t.Fatal(changed, err)
				}
				if _, present, err := stage.NextOccurrence(context.Background()); err != nil || !present {
					t.Fatal("successful ReScan lost row", present, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			endErr := owner.Finish(ctx)
			if !errors.Is(endErr, tc.end) {
				t.Fatal("owner cleanup error", endErr)
			}
			if tc.rescan != nil && !errors.Is(stage.Terminal, primary) {
				t.Fatal("End replaced ReScan primary", stage.Terminal)
			}
			if err := owner.Finish(ctx); !errors.Is(err, tc.end) {
				t.Fatal("repeated Finish changed error", err)
			}
			if p.begins != 1 || p.discoveries != 1 || scan.rescans != 1 || scan.ends != 1 || scan.cleanupCanceled {
				t.Fatal("failure retried or leaked scan", p.begins, p.discoveries, scan.rescans, scan.ends, scan.cleanupCanceled)
			}
		})
	}
}

func TestFDWQueryStageRewindCanceled(t *testing.T) {
	cleanup := errors.New("composite End cleanup")
	for _, tc := range []struct {
		name string
		end  error
	}{
		{"rescan", nil},
		{"rescan_and_end", cleanup},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scan := &queryUnitScan{row: catalog.ForeignRow{int64(7), int64(8), true, false, true}, endError: tc.end}
			p := &queryUnitProvider{path: queryUnitPath, scan: scan}
			state, node, owner := queryUnitTree(t, p, false)
			t.Cleanup(func() { _ = owner.Finish(context.Background()) })
			type demandKey struct{}
			first := context.WithValue(context.Background(), demandKey{}, "first pass")
			stage, err := exec.OpenNode(first, state, node)
			if err != nil {
				t.Fatal(err)
			}
			defer stage.Close()
			if _, present, err := stage.NextOccurrence(first); err != nil || !present {
				t.Fatal("first private row", present, err)
			}
			source := stage.Source
			current, cancel := context.WithCancel(context.WithValue(context.Background(), demandKey{}, "canceled rewind"))
			admitted, stop, joined := make(chan struct{}), make(chan struct{}), make(chan struct{})
			// Only the cancellation helper runs concurrently. Rewind and all
			// stage/provider observations remain on this owning goroutine.
			go func() {
				defer close(joined)
				select {
				case <-admitted:
					cancel()
				case <-stop:
				}
			}()
			defer func() {
				close(stop)
				cancel()
				<-joined
			}()
			var rescanContext context.Context
			var admissionError error
			scan.rescan = func(ctx context.Context, _ catalog.ForeignScanParameters) error {
				rescanContext, admissionError = ctx, ctx.Err()
				close(admitted)
				// Wait on the owned demand so even an incorrectly forwarded
				// context can be diagnosed without stranding the worker.
				<-current.Done()
				return ctx.Err()
			}
			changed, err := stage.Rewind(current)
			if rescanContext != current || admissionError != nil {
				t.Fatal("ReScan did not admit live current demand", rescanContext, admissionError)
			}
			<-joined
			primary := current.Err()
			if primary != context.Canceled || changed || !errors.Is(err, primary) {
				t.Fatal("admitted ReScan cancellation", changed, err, primary)
			}
			if !stage.Closed || !errors.Is(stage.Terminal, primary) || stage.Source != nil || len(stage.Selection) != 0 || len(stage.OrderValues) != 0 {
				t.Fatal("canceled ReScan retained reachable output", stage)
			}
			if _, present, err := stage.NextOccurrence(context.Background()); present || !errors.Is(err, primary) {
				t.Fatal("canceled ReScan replayed a prefix", present, err)
			}
			// A fresh, uncanceled consumer cannot publish either the retained
			// first-pass row or a partial second pass after terminal failure.
			dst := append([]sqltype.Value(nil), source.Row(0)...)
			before := append([]sqltype.Value(nil), dst...)
			if present, err := stage.Next(context.Background(), dst); present || !errors.Is(err, primary) || !reflect.DeepEqual(dst, before) {
				t.Fatal("canceled ReScan published output", present, err, dst)
			}
			if source.FlatCount != 1 || len(source.Anonymous) != 1 || !scan.emitted {
				t.Fatal("canceled ReScan imported second-pass rows", source.FlatCount, len(source.Anonymous), scan.emitted)
			}
			if err := owner.Finish(current); !errors.Is(err, tc.end) {
				t.Fatal("owner cleanup error", err)
			}
			if !errors.Is(stage.Terminal, primary) {
				t.Fatal("End replaced originating context error", stage.Terminal)
			}
			if err := owner.Finish(current); !errors.Is(err, tc.end) {
				t.Fatal("repeated Finish changed error", err)
			}
			if p.begins != 1 || p.discoveries != 1 || scan.rescans != 1 || scan.ends != 1 || scan.cleanupCanceled {
				t.Fatal("canceled ReScan retried or leaked scan", p.begins, p.discoveries, scan.rescans, scan.ends, scan.cleanupCanceled)
			}
		})
	}
}
