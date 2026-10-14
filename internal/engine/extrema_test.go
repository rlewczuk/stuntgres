package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/sqltype"
)

func extremaUnitProvider(t *testing.T, d *sqltype.Descriptor, maximum bool) extremaProvider {
	t.Helper()
	comparison, err := sqltype.BindComparison(d, d, d.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	return extremaProvider{usage: d.Usage(), comparison: comparison, maximum: maximum}
}

func TestExtremaProviderFamiliesAndTies(t *testing.T) {
	ctx := context.Background()
	cases := map[sqltype.ID][]string{
		sqltype.Int2: {"32767", "-32768"}, sqltype.Int4: {"2147483647", "-2147483648"}, sqltype.Int8: {"9223372036854775807", "-9223372036854775808"},
		sqltype.Oid: {"4294967295", "0"}, sqltype.Numeric: {"NaN", "-Infinity"}, sqltype.Float4: {"NaN", "-Infinity"}, sqltype.Float8: {"NaN", "-Infinity"},
		sqltype.Text: {"é", "Z"}, sqltype.BPChar: {"z  ", "a "}, sqltype.Bytea: {`\xff`, `\x00`},
		sqltype.Date: {"infinity", "-infinity"}, sqltype.Time: {"24:00:00", "00:00:00"}, sqltype.Timetz: {"08:00:00+00", "09:00:00+01"},
		sqltype.Timestamp: {"infinity", "-infinity"}, sqltype.Timestamptz: {"infinity", "-infinity"}, sqltype.Interval: {"31 days", "-1 day"},
	}
	for _, d := range sqltype.Builtins() {
		inputs, ok := cases[d.ID()]
		if !ok {
			continue
		}
		t.Run(d.Name(), func(t *testing.T) {
			large, err := d.Input(ctx, inputs[0], -1)
			if err != nil {
				t.Fatal(err)
			}
			small, err := d.Input(ctx, inputs[1], -1)
			if err != nil {
				t.Fatal(err)
			}
			for _, maximum := range []bool{false, true} {
				p := extremaUnitProvider(t, d, maximum)
				state, err := p.Init(ctx)
				if err != nil {
					t.Fatal(err)
				}
				isolated, err := p.Init(ctx)
				if err != nil || state == isolated {
					t.Fatal("shared state", err)
				}
				initial, err := p.Finalize(ctx, state)
				if err != nil || initial.Type() != d || !initial.IsNull() {
					t.Fatal(initial, err)
				}
				for _, v := range []sqltype.Value{sqltype.Null(d), large, sqltype.Null(d), small, large, small} {
					if err := p.Advance(ctx, state, v, sqltype.Value{}); err != nil {
						t.Fatal(err)
					}
				}
				want := small
				if maximum {
					want = large
				}
				for range 2 {
					got, err := p.Finalize(ctx, state)
					if err != nil || !sqltype.SameDatum(got, want) {
						t.Fatal("retained payload", got, want, err)
					}
				}
				got, err := p.Finalize(ctx, isolated)
				if err != nil || !got.IsNull() {
					t.Fatal("execution/group leak", got, err)
				}
			}
		})
	}
	for _, d := range sqltype.Builtins() {
		var inputs []string
		switch d.ID() {
		case sqltype.Numeric:
			inputs = []string{"1.0", "1.00"}
		case sqltype.Interval:
			inputs = []string{"1 mon", "30 days"}
		case sqltype.BPChar:
			inputs = []string{"a", "a "}
		case sqltype.Float4, sqltype.Float8:
			inputs = []string{"0", "-0"}
		default:
			continue
		}
		for _, maximum := range []bool{false, true} {
			for _, reverse := range []bool{false, true} {
				first, second := inputs[0], inputs[1]
				if reverse {
					first, second = second, first
				}
				a, err := d.Input(ctx, first, -1)
				if err != nil {
					t.Fatal(err)
				}
				b, err := d.Input(ctx, second, -1)
				if err != nil {
					t.Fatal(err)
				}
				p := extremaUnitProvider(t, d, maximum)
				s, _ := p.Init(ctx)
				if err := p.Advance(ctx, s, a, sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
				if err := p.Advance(ctx, s, b, sqltype.Value{}); err != nil {
					t.Fatal(err)
				}
				want := b
				if d.ID() == sqltype.BPChar {
					want = a
				}
				got, err := p.Finalize(ctx, s)
				if err != nil || !sqltype.SameDatum(got, want) {
					t.Fatal("equal representation", d.Name(), maximum, reverse, got, want, err)
				}
			}
		}
	}
}

func TestExtremaProviderDemandAndInvariants(t *testing.T) {
	ctx := context.Background()
	var typ, foreign *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			typ = d
		}
	}
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Text {
			foreign = d
		}
	}
	p := extremaUnitProvider(t, typ, false)
	var bindErr error
	p.comparison, bindErr = sqltype.BindComparison(typ, typ, typ.Usage().Collation, true)
	if bindErr != nil {
		t.Fatal(bindErr)
	}
	s, _ := p.Init(ctx)
	first := sqltype.String(typ, "a")
	for _, v := range []sqltype.Value{sqltype.Null(typ), first, sqltype.Null(typ)} {
		if err := p.Advance(ctx, s, v, sqltype.Value{}); err != nil {
			t.Fatal("premature comparison", err)
		}
	}
	state(t, p.Advance(ctx, s, sqltype.String(typ, "b"), sqltype.Value{}), "42P22")
	got, err := p.Finalize(ctx, s)
	if err != nil || !sqltype.SameDatum(got, first) {
		t.Fatal(got, err)
	}
	for _, invalid := range []any{nil, struct{}{}, (*extremaState)(nil), &extremaState{}, &extremaState{value: sqltype.Null(foreign)}} {
		if err := p.Advance(ctx, invalid, first, sqltype.Value{}); err == nil {
			t.Fatal("invalid state", invalid)
		}
		if _, err := p.Finalize(ctx, invalid); err == nil {
			t.Fatal("invalid final state", invalid)
		}
	}
	for _, invalid := range []sqltype.Value{{}, sqltype.Null(foreign), sqltype.String(foreign, "x")} {
		if err := p.Advance(ctx, s, invalid, sqltype.Value{}); err == nil {
			t.Fatal("foreign candidate accepted")
		}
	}
	if _, err := (extremaProvider{}).Init(ctx); err == nil {
		t.Fatal("invalid descriptor")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s, err := p.Init(canceled); s != nil || err != context.Canceled {
		t.Fatal(s, err)
	}
	if err := p.Advance(canceled, s, sqltype.String(typ, "0"), sqltype.Value{}); err != context.Canceled {
		t.Fatal(err)
	}
	if _, err := p.Finalize(canceled, s); err != context.Canceled {
		t.Fatal(err)
	}
	if !sqltype.SameDatum(s.(*extremaState).value, first) {
		t.Fatal("canceled mutation")
	}
}

// Detect the actual shared comparator on the stack, not an Err-call count or a
// mock provider. Cancel inside Compare's own context checkpoint deterministically.
type extremaCompareCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	entered bool
}

func (c *extremaCompareCancelContext) Err() error {
	if !c.entered {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, ".Comparison.Compare") {
				c.entered = true
				c.cancel()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestExtremaActualComparatorCancellationOwnership(t *testing.T) {
	for _, maximum := range []bool{false, true} {
		name := "min"
		if maximum {
			name = "max"
		}
		_, query, source := queryCoreFixture(t, "SELECT "+name+"(v) FROM t")
		ctx, cancel := context.WithCancel(context.Background())
		probe := &extremaCompareCancelContext{Context: ctx, cancel: cancel}
		rows, count, err := finalizeQuery(probe, query.Root, source, nil)
		cancel()
		if !probe.entered || err != context.Canceled || rows != nil || count != 0 || source.view != nil || source.anonymous != nil {
			t.Fatal("actual comparator publication/ownership", probe.entered, rows, count, err, source)
		}
	}
	b, source := insertSelectEvaluationFixture(t, "SELECT min(v) FROM t")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &extremaCompareCancelContext{Context: ctx, cancel: cancel}
	rows, err := evaluateInsertQuery(probe, b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if !probe.entered || rows.values != nil || err != context.Canceled || source.view != nil || source.anonymous != nil {
		t.Fatal(rows, err, probe.entered, source)
	}
}

func TestExtremaEagerEvaluationAndSlots(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT min(v),min(v),max(v) FROM t")
	node := query.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 2 || node.calls[0].operation != aggregateMin || node.calls[1].operation != aggregateMax {
		t.Fatal(node.calls)
	}
	for _, c := range query.Root.Schema() {
		if !c.NullabilityKnown || !c.Nullable || c.Usage.Typmod != -1 || c.OriginTable != 0 || c.OriginAttribute != 0 {
			t.Fatal(c)
		}
	}
	original := node.calls[0].argument
	calls := 0
	node.calls[0].argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { calls++; return original.Eval(c) })
	rows, _, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	before := calls
	if before != 4 {
		t.Fatal("later arguments skipped", calls)
	}
	values := make([]sqltype.Value, 3)
	if ok, err := rows.Next(context.Background(), values); !ok || err != nil || calls != before {
		t.Fatal("public Next evaluated", ok, err, calls)
	}
	_, volatile, owner := queryCoreFixture(t, "SELECT min(nextval($1)),min(nextval($1)),max(nextval($1)) FROM t")
	defer owner.close()
	if len(volatile.Root.(*projectNode).child.(*aggregateNode).calls) != 3 {
		t.Fatal("volatile calls shared")
	}
	_, failed, failedSource := queryCoreFixture(t, "SELECT min(v) FROM t")
	failedNode := failed.Root.(*projectNode).child.(*aggregateNode)
	argument := failedNode.calls[0].argument
	sentinel := errors.New("late extrema argument")
	calls = 0
	failedNode.calls[0].argument = queryProbe(argument.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 4 {
			return sqltype.Value{}, sentinel
		}
		return argument.Eval(c)
	})
	result, count, err := finalizeQuery(context.Background(), failed.Root, failedSource, nil)
	if result != nil || count != 0 || err != sentinel || calls != 4 || failedSource.view != nil || failedSource.anonymous != nil {
		t.Fatal(result, count, err, calls, failedSource)
	}
}
