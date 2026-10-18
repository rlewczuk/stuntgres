package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type arraySearchTestContext struct {
	unitCapabilities
	context.Context
}

func (c *arraySearchTestContext) FunctionContext() context.Context { return c.Context }
func (c *arraySearchTestContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}

type arraySearchTestArgument struct {
	expr.Info
	value sqltype.Value
	order *[]int
	index int
	err   error
}

func (a arraySearchTestArgument) Eval(expr.Context) (sqltype.Value, error) {
	if a.order != nil {
		*a.order = append(*a.order, a.index)
	}
	return a.value, a.err
}
func arraySearchTestTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}
func arraySearchTestLiteral(v sqltype.Value) arraySearchTestArgument {
	return arraySearchTestArgument{Info: expr.Info{Usage: v.Type().Usage()}, value: v}
}

func TestArraySearchCarrierDemand(t *testing.T) {
	types := arraySearchTestTypes()
	integer, array := types[sqltype.Int4], types[1007]
	plan, err := sqltype.BindArraySearch("array_position", array, integer, integer, array.Usage().Collation, false)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := array.Input(context.Background(), "[-2:1]={7,NULL,7,8}", -1)
	if err != nil {
		t.Fatal(err)
	}
	seven, err := sqltype.Integer(integer, 7)
	if err != nil {
		t.Fatal(err)
	}
	start, err := sqltype.Integer(integer, -1)
	if err != nil {
		t.Fatal(err)
	}
	args := []expr.Expr{arraySearchTestLiteral(payload), arraySearchTestLiteral(seven), arraySearchTestLiteral(start)}
	call := NewArraySearch(expr.Info{Usage: integer.Usage()}, plan, args, "")
	c := &arraySearchTestContext{Context: context.Background()}
	got, err := call.Eval(c)
	number, numberErr := got.Integer()
	if err != nil || numberErr != nil || number != 0 || got.Type() != integer {
		t.Fatal(got, err, numberErr)
	}
	args[1] = arraySearchTestLiteral(sqltype.Null(integer))
	got, err = call.Eval(c)
	number, numberErr = got.Integer()
	if err != nil || numberErr != nil || number != -1 {
		t.Fatal(got, err, numberErr)
	}
	var order []int
	failure := errors.New("reached array argument")
	args[0] = arraySearchTestArgument{Info: expr.Info{Usage: array.Usage()}, value: sqltype.Null(array), order: &order, index: 0}
	args[1] = arraySearchTestArgument{Info: expr.Info{Usage: integer.Usage()}, value: seven, order: &order, index: 1, err: failure}
	args[2] = arraySearchTestArgument{Info: expr.Info{Usage: integer.Usage()}, value: start, order: &order, index: 2}
	if _, err := call.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
	order = nil
	args[1] = arraySearchTestArgument{Info: expr.Info{Usage: integer.Usage()}, value: seven, order: &order, index: 1}
	got, err = call.Eval(c)
	if err != nil || !got.IsNull() || got.Type() != integer || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal(got, order, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	order = nil
	if _, err := call.Eval(&arraySearchTestContext{Context: ctx}); !errors.Is(err, context.Canceled) || len(order) != 0 {
		t.Fatal(order, err)
	}
}

func TestArraySearchCarrierReconstruct(t *testing.T) {
	source, target := arraySearchTestTypes(), arraySearchTestTypes()
	makeCall := func(types map[sqltype.ID]*sqltype.Descriptor) ArraySearch {
		array, integer := types[1007], types[sqltype.Int4]
		payload, err := array.Input(context.Background(), "[-2:-1]={1,NULL}", -1)
		if err != nil {
			t.Fatal(err)
		}
		one, err := sqltype.Integer(integer, 1)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := sqltype.BindArraySearch("array_remove", array, array, integer, array.Usage().Collation, false)
		if err != nil {
			t.Fatal(err)
		}
		return NewArraySearch(expr.Info{Usage: array.Usage()}, plan, []expr.Expr{arraySearchTestLiteral(payload), arraySearchTestLiteral(one)}, "pg_catalog")
	}
	old, mapped := makeCall(source), makeCall(target)
	ref, conflict := mapped.Plan.InputCollation()
	rebuilt, err := old.Reconstruct(mapped.Info, mapped.Args, target[1007], target[1007], target[sqltype.Int4], ref, conflict)
	if err != nil {
		t.Fatal(err)
	}
	if err := rebuilt.Validate(); err != nil {
		t.Fatal(err)
	}
	if old.Plan.Array() != source[1007] || old.Usage.Type != source[1007] || old.Args[0].ResultUsage().Type != source[1007] || rebuilt.Plan.Array() != target[1007] || rebuilt.Namespace != old.Namespace {
		t.Fatal("mutated descriptor authority")
	}
	for _, call := range []ArraySearch{old, rebuilt} {
		value, err := call.Eval(&arraySearchTestContext{Context: context.Background()})
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Type().Output(context.Background(), value)
		if err != nil || text != "[-2:-2]={NULL}" || value.Type() != call.Usage.Type || value.ArrayElement(0).Type() != call.Usage.Type.Element() {
			t.Fatal(text, err)
		}
	}
	invalid := rebuilt
	invalid.Info.Usage = source[1007].Usage()
	if err := invalid.Validate(); err == nil {
		t.Fatal("accepted foreign result generation")
	}
}

func TestArraySearchSelectionFacts(t *testing.T) {
	types := arraySearchTestTypes()
	integer, array := types[sqltype.Int4], types[1007]
	for _, tc := range []struct {
		name  string
		count int
		valid bool
		arity int
	}{
		{"array_position", 3, true, 2}, {"array_replace", 3, true, 3}, {"array_positions", 3, false, 2}, {"ARRAY_REMOVE", 2, false, 2},
	} {
		arity, valid := ArraySearchArity(tc.name, true, false, tc.count)
		if arity != tc.arity || valid != tc.valid {
			t.Fatal(tc, arity, valid)
		}
	}
	if _, valid := ArraySearchArity("array_position", false, false, 2); valid {
		t.Fatal("invisible signature")
	}
	if _, valid := ArraySearchArity("array_position", true, true, 2); valid {
		t.Fatal("named signature")
	}
	usage := array.Usage()
	usage.Typmod = 8
	leaf, valid := ArraySearchLeafUsage(usage)
	if !valid || leaf.Type != integer || leaf.Typmod != 8 || leaf.Collation != usage.Collation {
		t.Fatal(leaf, valid)
	}
	if _, valid := ArraySearchLeafUsage(integer.Usage()); valid {
		t.Fatal("scalar array slot")
	}
	args := []Argument{{Usage: array.Usage()}, {Usage: integer.Usage()}, {Usage: integer.Usage()}}
	targets, valid := SelectArraySearch("array_position", args, array.Usage(), integer.Usage(), integer.Usage())
	if !valid || targets[0].Type != array || targets[1].Type != integer || targets[2].Type != integer {
		t.Fatal(targets, valid)
	}
	args[2].Usage = types[sqltype.Text].Usage()
	if _, valid := SelectArraySearch("array_position", args, array.Usage(), integer.Usage(), integer.Usage()); valid {
		t.Fatal("accepted nonimplicit start")
	}
}
