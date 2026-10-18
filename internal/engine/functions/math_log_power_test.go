package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestMathFourArgumentTraversal(t *testing.T) {
	source, target := arraySearchTestTypes(), arraySearchTestTypes()
	var order []int
	makeArgs := func(types map[sqltype.ID]*sqltype.Descriptor) []expr.Expr {
		args := make([]expr.Expr, 4)
		for i, n := range []int64{2, 0, 10, 5} {
			d := types[sqltype.Numeric]
			if i == 3 {
				d = types[sqltype.Int4]
			}
			value, err := d.Input(context.Background(), []string{"2", "0", "10", "5"}[i], -1)
			if err != nil {
				t.Fatal(n, err)
			}
			args[i] = functionTestArgument{Info: expr.Info{Usage: d.Usage()}, value: value, order: &order, index: i}
		}
		return args
	}
	args := makeArgs(source)
	call, err := NewMath(expr.Info{Usage: source[sqltype.Int4].Usage()}, "width_bucket", args)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &functionTestContext{Context: context.Background()}
	check := func(call Math, want int64) {
		order = nil
		got, err := call.Eval(ctx)
		n, numberErr := got.Integer()
		if err != nil || numberErr != nil || n != want || got.Type() != call.Usage.Type || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
			t.Fatal(got, order, err, numberErr)
		}
	}
	check(call, 2)
	mapped := makeArgs(target)
	rebuilt, err := call.Reconstruct(expr.Info{Usage: target[sqltype.Int4].Usage()}, mapped, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := rebuilt.Validate(); err != nil {
		t.Fatal(err)
	}
	check(rebuilt, 2)
	check(call, 2)
	call.Args = append([]expr.Expr(nil), call.Args...)
	call.Args[0] = functionTestArgument{Info: expr.Info{Usage: source[sqltype.Numeric].Usage()}, value: sqltype.Null(source[sqltype.Numeric]), literal: true, order: &order, index: 0}
	order = nil
	got, err := call.Eval(ctx)
	if err != nil || !got.IsNull() || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal("selected literal NULL demands operands", got, order, err)
	}
	demanded := errors.New("required fourth argument")
	call.Args[3] = functionTestArgument{Info: expr.Info{Usage: source[sqltype.Int4].Usage()}, err: demanded, order: &order, index: 3}
	order = nil
	got, err = call.Eval(ctx)
	if !errors.Is(err, demanded) || got.Type() != nil || !reflect.DeepEqual(order, []int{0, 1, 2, 3}) {
		t.Fatal("fourth argument failure", got, order, err)
	}
	// A wrapper only annotates its own provider failure, never a child's error.
	d := source[sqltype.Numeric]
	negative, err := d.Input(context.Background(), "-1", -1)
	if err != nil {
		t.Fatal(err)
	}
	wrapper, err := NewMath(expr.Info{Usage: d.Usage()}, "log10", []expr.Expr{functionTestArgument{Info: expr.Info{Usage: d.Usage()}, value: negative}})
	if err != nil {
		t.Fatal(err)
	}
	wrapper.WrapperContext = "SQL function \"log10\" statement 1"
	_, err = wrapper.Eval(ctx)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Context != wrapper.WrapperContext || diagnostic.Position != "" || diagnostic.Code != "2201E" {
		t.Fatal(err)
	}
	child := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "child failure"}
	wrapper.Args[0] = functionTestArgument{Info: expr.Info{Usage: d.Usage()}, err: child}
	_, err = wrapper.Eval(ctx)
	if err != child || child.Context != "" {
		t.Fatal("child diagnostic annotated/mutated", err)
	}
	// Reconstructed collated thresholds bind target leaves and input authority.
	makeThreshold := func(types map[sqltype.ID]*sqltype.Descriptor) Math {
		text, array := types[sqltype.Text], types[1009]
		p, err := sqltype.BindWidthBucketThreshold(types[sqltype.Int4].Usage(), text.Usage(), array.Usage(), text.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		a, err := text.Input(context.Background(), "m", -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := array.Input(context.Background(), "{a,m,m,z}", -1)
		if err != nil {
			t.Fatal(err)
		}
		return Math{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, Plan: p, Args: []expr.Expr{functionTestArgument{Info: expr.Info{Usage: text.Usage()}, value: a}, functionTestArgument{Info: expr.Info{Usage: array.Usage()}, value: b}}}
	}
	old, next := makeThreshold(source), makeThreshold(target)
	ref, conflict := next.Plan.InputCollation()
	mappedThreshold, err := old.Reconstruct(next.Info, next.Args, ref, conflict)
	if err != nil {
		t.Fatal(err)
	}
	if err := mappedThreshold.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, call := range []Math{old, mappedThreshold} {
		got, err := call.Eval(ctx)
		n, numberErr := got.Integer()
		if err != nil || numberErr != nil || n != 3 || got.Type() != call.Usage.Type {
			t.Fatal(got, err, numberErr)
		}
	}
	if old.Plan.Equal(mappedThreshold.Plan) || old.Args[0].ResultUsage().Type != source[sqltype.Text] {
		t.Fatal("source/target authority lost")
	}
}
