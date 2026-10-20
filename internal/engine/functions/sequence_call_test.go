package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// sequenceUnitContext records the runtime demand of real SequenceCall nodes;
// generator, lock and privilege semantics stay engine-owned.
type sequenceUnitContext struct {
	formatUnitContext
	order     *[]int
	authority error
	result    int64
	failure   error
	requests  []SequenceRequest
}

func (c *sequenceUnitContext) SequenceAuthority() error {
	*c.order = append(*c.order, -1)
	return c.authority
}

func (c *sequenceUnitContext) Sequence(request SequenceRequest) (int64, error) {
	*c.order = append(*c.order, -2)
	c.requests = append(c.requests, request)
	return c.result, c.failure
}

func sequenceUnitValue(t *testing.T, typ *sqltype.Descriptor, text string) sqltype.Value {
	t.Helper()
	value, err := typ.Input(context.Background(), text, -1)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSequenceCallRuntimeDemand(t *testing.T) {
	types := formatUnitTypes()
	var order []int
	regclass := sequenceUnitValue(t, types[sqltype.Regclass], "16391")
	argument := func(index int, value sqltype.Value) expr.Expr {
		return formatUnitArgument{Info: expr.Info{Usage: value.Type().Usage()}, value: value, order: &order, index: index}
	}
	ten := sequenceUnitValue(t, types[sqltype.Int8], "10")
	called := sequenceUnitValue(t, types[sqltype.Bool], "false")
	n := SequenceCall{Info: expr.Info{Usage: types[sqltype.Int8].Usage()}, Name: "setval", Args: []expr.Expr{argument(0, regclass), argument(1, ten), argument(2, called)}}
	c := &sequenceUnitContext{formatUnitContext: formatUnitContext{Context: context.Background()}, order: &order, result: 10}
	value, err := n.Eval(c)
	if err != nil || value.Type() != types[sqltype.Int8] || !reflect.DeepEqual(order, []int{-1, 0, 1, 2, -2}) {
		t.Fatal("authority, arguments once in written order, then runtime", value, order, err)
	}
	if got, _ := value.Integer(); got != 10 {
		t.Fatal(got)
	}
	request := c.requests[0]
	if request.Name != "setval" || request.OID != 16391 || request.Ref.Domain() != nil || !request.HasCalled || !reflect.DeepEqual(request.Value, ten) || !reflect.DeepEqual(request.Called, called) {
		t.Fatal("argument-supplied provider OID and setval inputs", request)
	}

	// Authority failures precede every argument; argument failures precede the runtime.
	order, c.requests = nil, nil
	c.authority = errors.New("no statement authority")
	if _, err := n.Eval(c); !errors.Is(err, c.authority) || !reflect.DeepEqual(order, []int{-1}) {
		t.Fatal(order, err)
	}
	c.authority = nil
	order = nil
	failure := errors.New("argument failure")
	n.Args[1] = formatUnitArgument{Info: expr.Info{Usage: types[sqltype.Int8].Usage()}, order: &order, index: 1, err: failure}
	if _, err := n.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{-1, 0, 1}) || len(c.requests) != 0 {
		t.Fatal(order, err)
	}

	// Every argument is demanded before a NULL skips the provider entirely.
	order = nil
	n.Args[1] = argument(1, sqltype.Null(types[sqltype.Int8]))
	value, err = n.Eval(c)
	if err != nil || !value.IsNull() || value.Type() != types[sqltype.Int8] || !reflect.DeepEqual(order, []int{-1, 0, 1, 2}) || len(c.requests) != 0 {
		t.Fatal("strict NULL", value, order, err)
	}

	// A bound provider keeps its reference; lastval carries none.
	domain := &ids.Domain{}
	ref := domain.Ref(3, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16391})
	order = nil
	bound := SequenceCall{Info: expr.Info{Usage: types[sqltype.Int8].Usage()}, Name: "nextval", Args: []expr.Expr{argument(0, regclass)}, Ref: ref, Temporary: ids.SessionToken{}}
	if _, err := bound.Eval(c); err != nil || c.requests[0].Ref != ref || c.requests[0].OID != 0 || c.requests[0].HasCalled {
		t.Fatal(c.requests, err)
	}
	c.requests = nil
	if _, err := (SequenceCall{Info: bound.Info, Name: "lastval"}).Eval(c); err != nil || c.requests[0].Name != "lastval" || c.requests[0].OID != 0 {
		t.Fatal(c.requests, err)
	}

	// Malformed argument-supplied provider text fails before the runtime.
	c.requests = nil
	text := sequenceUnitValue(t, types[sqltype.Text], "s")
	if _, err := (SequenceCall{Info: bound.Info, Name: "currval", Args: []expr.Expr{argument(0, text)}}).Eval(c); err == nil || !strings.Contains(err.Error(), "invalid syntax") || len(c.requests) != 0 {
		t.Fatal(c.requests, err)
	}

	// Runtime failures surface unchanged.
	c.failure = errors.New("sequence failure")
	if _, err := bound.Eval(c); !errors.Is(err, c.failure) {
		t.Fatal(err)
	}
}

func TestSequenceCallIdentityResult(t *testing.T) {
	types := formatUnitTypes()
	var order []int
	for _, tc := range []struct {
		id   sqltype.ID
		want int64
	}{{sqltype.Int2, -32768}, {sqltype.Int4, -2147483648}, {sqltype.Int8, 2147483648}} {
		c := &sequenceUnitContext{formatUnitContext: formatUnitContext{Context: context.Background()}, order: &order, result: 2147483648}
		if tc.id == sqltype.Int2 {
			c.result = 32768
		}
		n := SequenceCall{Info: expr.Info{Usage: types[tc.id].Usage()}, Name: "nextval", Ref: (&ids.Domain{}).Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1}), Identity: true}
		value, err := n.Eval(c)
		if err != nil || value.Type() != types[tc.id] || !c.requests[0].Identity {
			t.Fatal(tc.id, value, err)
		}
		if got, _ := value.Integer(); got != tc.want {
			t.Fatal(tc.id, got)
		}
	}
}

func TestSequenceCallMissingRuntime(t *testing.T) {
	types := formatUnitTypes()
	n := SequenceCall{Info: expr.Info{Usage: types[sqltype.Int8].Usage()}, Name: "lastval"}
	if _, err := n.Eval(nil); err == nil || err.Error() != "stuntgres: missing expression context" {
		t.Fatal(err)
	}
	if _, err := n.Eval(&formatUnitContext{Context: context.Background()}); err == nil || err.Error() != "stuntgres: sequence call lacks statement authority" {
		t.Fatal(err)
	}
}

func TestSequenceCallProtocols(t *testing.T) {
	types := formatUnitTypes()
	a := &captureMarker{Info: expr.Info{Usage: types[sqltype.Regclass].Usage()}, index: 0}
	b := &captureMarker{Info: expr.Info{Usage: types[sqltype.Int8].Usage()}, index: 1}
	domain := &ids.Domain{}
	ref := domain.Ref(2, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 16391})
	token := domain.Session(7)
	n := SequenceCall{Info: expr.Info{Usage: types[sqltype.Int8].Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, Name: "setval", Args: []expr.Expr{a, b}, Ref: ref, Temporary: token}
	unchanged, changed, err := expr.Map(n, func(x expr.Expr) (expr.Expr, bool, error) { return x, false, nil })
	if err != nil || changed || !reflect.DeepEqual(unchanged, n) {
		t.Fatal(unchanged, changed, err)
	}
	info := n.Info
	info.Property.ParameterDependent = true
	rebuilt, err := n.Rebuild(info, []expr.Expr{b, a})
	if err != nil {
		t.Fatal(err)
	}
	got := rebuilt.(SequenceCall)
	if got.Info != info || got.Name != n.Name || got.Ref != ref || got.Temporary != token || got.Args[0] != b || n.Args[0] != a {
		t.Fatal("rebuild keeps provider identity, copies children", got)
	}
	identity := SequenceCall{Info: n.Info, Name: "nextval", Ref: ref, Identity: true}
	if again, err := identity.Rebuild(identity.Info, nil); err != nil || !again.(SequenceCall).Identity {
		t.Fatal(again, err)
	}
	if _, err := n.Rebuild(info, []expr.Expr{a}); err == nil {
		t.Fatal("wrong child count accepted")
	}
	if n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || !n.LooksLikeFunction() {
		t.Fatal("fold/preflight/syntax")
	}
	operands, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Check(n, 2) != nil {
		t.Fatal(operands, ok, err)
	}
	for i, want := range []expr.Expr{a, b} {
		if got, err := operands.At(n, i); err != nil || got != want {
			t.Fatal(i, got, err)
		}
	}
	if _, ok, _ := n.Capture(expr.CaptureIdentity); ok {
		t.Fatal("non-function capture claimed")
	}
	same := func(x, y expr.Expr) bool { return x == y }
	other := n
	other.Args = []expr.Expr{b, a}
	moved := n
	moved.Ref.Address.ObjectID++
	temporary := n
	temporary.Temporary = ids.SessionToken{}
	marked := n
	marked.Identity = true
	renamed := n
	renamed.Name = "currval"
	if !n.SameGroup(n, same, false) || !n.SameGroup(marked, same, false) || n.SameGroup(other, same, false) || n.SameGroup(moved, same, false) || n.SameGroup(renamed, same, false) || n.SameGroup(temporary, same, false) {
		t.Fatal("group identity")
	}
	r, err := n.Render()
	if err != nil || r.Tree != "Sequence setval oid 16391" || r.Form != expr.FormCall || r.Name != "setval" || r.Quote != expr.QuoteOnRequest || r.SimpleCast || len(r.Args) != 2 || r.Args[0].Expr != a || r.Args[1].Expr != b || r.Args[0].HideImplicit || len(r.Inputs) != 2 {
		t.Fatal(r, err)
	}
	if r, err := (SequenceCall{Info: n.Info, Name: "lastval"}).Render(); err != nil || r.Tree != "Sequence lastval oid 0" || len(r.Args) != 0 {
		t.Fatal(r, err)
	}
}
