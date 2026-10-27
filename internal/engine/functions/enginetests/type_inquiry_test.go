package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeProtocol(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	bind := func(t *testing.T, source string) functions.TypeInquiry {
		t.Helper()
		b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		raw := enginetest.Statement(t, "SELECT "+source).(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(source, err)
		}
		n, ok := node.(functions.TypeInquiry)
		if !ok {
			t.Fatalf("%s: %T", source, node)
		}
		return n
	}
	for _, tc := range []struct {
		source          string
		routine         uint32
		result, logical sqltype.ID
		replay          string
	}{
		{"pg_typeof('x')", 1619, sqltype.Regtype, sqltype.Unknown, "pg_catalog.pg_typeof('x')"},
		{"pg_typeof(NULL)", 1619, sqltype.Regtype, sqltype.Unknown, "pg_catalog.pg_typeof(NULL)"},
		{"pg_typeof('x'::unknown)", 1619, sqltype.Regtype, sqltype.Unknown, `pg_catalog.pg_typeof(CAST('x' AS pg_catalog."unknown"))`},
		{"pg_typeof(NULL::unknown)", 1619, sqltype.Regtype, sqltype.Unknown, `pg_catalog.pg_typeof(CAST(NULL AS pg_catalog."unknown"))`},
		{"pg_typeof(NULL::integer)", 1619, sqltype.Regtype, sqltype.Int4, ""},
		{"pg_typeof('x'::text)", 1619, sqltype.Regtype, sqltype.Text, ""},
		{"pg_typeof(NULL::text::unknown)", 1619, sqltype.Regtype, sqltype.Unknown, ""},
		{"pg_typeof('x'::anyelement)", 1619, sqltype.Regtype, sqltype.Unknown, `pg_catalog.pg_typeof(CAST('x' AS pg_catalog."anyelement"))`},
		{"pg_typeof(NULL::anycompatible)", 1619, sqltype.Regtype, sqltype.Unknown, `pg_catalog.pg_typeof(CAST(NULL AS pg_catalog."anycompatible"))`},
		{"format_type(23,NULL)", 1081, sqltype.Text, 0, ""},
		{"pg_basetype(NULL)", 6315, sqltype.Regtype, 0, ""},
	} {
		t.Run(tc.source, func(t *testing.T) {
			n := bind(t, tc.source)
			if err := n.ValidateStoredProvider(); err != nil {
				t.Fatal(err)
			}
			if n.RoutineReference == nil || n.RoutineReference.RoutineOID() != tc.routine || n.ResultUsage().Type.ID() != tc.result || n.Properties().Volatility != expr.Stable || n.IntrinsicVolatility() != expr.Stable || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren {
				t.Fatal("selected declaration/protocol", n.Info)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != len(n.Args) || capture.Check(n, len(n.Args)) != nil {
				t.Fatal("capture", capture, err)
			}
			for i := range n.Args {
				if !reflect.DeepEqual(capture.Args[i], n.Args[i]) {
					t.Fatal("lost written child")
				}
			}
			if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
				t.Fatal("wrong syntax admitted", err)
			}
			rebuilt, err := n.Rebuild(n.Info, n.Children())
			if err != nil || !n.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("rebuild/group", err)
			}
			if _, err := n.Rebuild(n.Info, nil); err == nil {
				t.Fatal("missing operands accepted")
			}
			foreign := *n.ResultUsage().Type
			info := n.Info
			info.Usage.Type = &foreign
			if _, err := n.Rebuild(info, n.Children()); err == nil {
				t.Fatal("foreign result generation accepted")
			}
			if tc.logical != 0 {
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
				value, err := n.Eval(&evaluation)
				if err != nil {
					t.Fatal(err)
				}
				id, err := value.TypeOID()
				if err != nil || id != uint32(tc.logical) || value.Type() != n.ResultUsage().Type || value.IsNull() {
					t.Fatal("logical witness", value, id, err)
				}
				if tc.logical == sqltype.Unknown && n.Args[0].ResultUsage().Type.ID() != sqltype.Text {
					t.Fatal("UNKNOWN leaked into executable transport")
				}
			}
			if tc.replay != "" {
				sql, err := stored.DefaultSQL(ctx, e.CatalogRead(), n)
				if err != nil || sql != tc.replay {
					t.Fatal("logical source replay", sql, tc.replay, err)
				}
				child := n.Args[0].(plan.LiteralExpr)
				child.Value = sqltype.String(child.ResultUsage().Type, "replacement")
				if _, err := n.Rebuild(n.Info, []expr.Expr{child}); err == nil {
					t.Fatal("unknown source replaced")
				}
			}
		})
	}
	t.Run("real string IO logical cast", func(t *testing.T) {
		for _, tc := range []struct{ source, want string }{
			{"pg_typeof('x'::text::unknown)", "x"},
			{"pg_typeof('x'::bpchar(4)::unknown)", "x   "},
			{"pg_typeof('name'::name::unknown)", "name"},
		} {
			n := bind(t, tc.source)
			transport, ok := n.Args[0].(plan.CoercionExpr)
			if !ok || transport.UnknownInputCast == nil || !transport.Plan.IsUnknownInputTransport() {
				t.Fatal("logical IO cast lacks real existing transport")
			}
			cast, err := transport.LogicalInputCast()
			if err != nil || cast.Target.Type.ID() != sqltype.Unknown || !reflect.DeepEqual(cast.Source, transport.Child) {
				t.Fatal("logical IO capture lost source/target", err)
			}
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
			value, err := transport.Eval(&evaluation)
			text, textErr := value.Text()
			if err != nil || textErr != nil || text != tc.want || value.Type().ID() != sqltype.Text {
				t.Fatal("IO bytes differ from source output", text, tc.want, err, textErr)
			}
			identity, err := n.Eval(&evaluation)
			oid, oidErr := identity.TypeOID()
			if err != nil || oidErr != nil || oid != uint32(sqltype.Unknown) {
				t.Fatal("concrete IO transport erased logical705", err, oidErr, oid)
			}
			replay, err := stored.DefaultSQL(ctx, e.CatalogRead(), n)
			if err != nil || !strings.Contains(replay, ` AS "pg_catalog"."unknown")`) {
				t.Fatal("IO cast replay lost actual UNKNOWN target", replay, err)
			}
			for _, mutate := range []func(*plan.CoercionExpr){
				func(c *plan.CoercionExpr) {
					marker := *c.UnknownInputCast
					marker.Target = c.Usage.Type
					c.UnknownInputCast = &marker
				},
				func(c *plan.CoercionExpr) { c.Mode = sqltype.Implicit },
				func(c *plan.CoercionExpr) {
					c.Plan, _ = sqltype.PlanCoercion(c.Child.ResultUsage(), c.Usage, sqltype.Explicit)
				},
				func(c *plan.CoercionExpr) { c.UnknownInputCast = nil },
				func(c *plan.CoercionExpr) {
					integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
					v, _ := sqltype.Integer(integer, 1)
					c.Child = plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: v}
				},
			} {
				bad := transport
				mutate(&bad)
				if _, err := n.Rebuild(n.Info, []expr.Expr{bad}); err == nil {
					t.Fatal("malformed IO authority acquired logical705")
				}
			}
			if err := n.ValidateStoredProvider(); err != nil {
				t.Fatal("rejected transport changed original", err)
			}
		}
		n := bind(t, "pg_typeof($1::text::unknown)")
		if !n.Properties().ParameterDependent {
			t.Fatal("IO cast erased parameter ownership")
		}
		failing := bind(t, "pg_typeof('x'::text::unknown)")
		transport := failing.Args[0].(plan.CoercionExpr)
		var order []int
		cause := errors.New("source output demand")
		transport.Child = typeDescribeObserved{Info: expr.InfoOf(transport.Child), order: &order, index: 1, failure: cause}
		rebuilt, err := failing.Rebuild(failing.Info, []expr.Expr{transport})
		if err != nil {
			t.Fatal(err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
		if _, err := rebuilt.Eval(&evaluation); !errors.Is(err, cause) || !reflect.DeepEqual(order, []int{1}) {
			t.Fatal("IO metadata erased demanded source", order, err)
		}
	})
	t.Run("binding retains child dependence", func(t *testing.T) {
		volatile := bind(t, "pg_typeof(random())")
		if volatile.Properties().Volatility != expr.Volatile || volatile.IntrinsicVolatility() != expr.Stable {
			t.Fatal("volatile child downgraded", volatile.Properties())
		}
		parameter := bind(t, "pg_typeof($1::integer)")
		if !parameter.Properties().ParameterDependent {
			t.Fatal("parameter dependence erased")
		}
		if !parameter.ClosedDefault(func(expr.Expr) bool { return true }) || parameter.ClosedDefault(func(expr.Expr) bool { return false }) {
			t.Fatal("DEFAULT closure does not defer to its consumer")
		}
		if _, strict := any(bind(t, "pg_basetype(NULL)")).(expr.StrictFolding); strict {
			t.Fatal("strict metadata acquired child-eliminating planning")
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}
		if _, err := volatile.Eval(&evaluation); !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation prologue", err)
		}
		if _, err := volatile.Eval(nil); err == nil {
			t.Fatal("nil evaluation context admitted")
		}
	})
	t.Run("children retain effects and properties", func(t *testing.T) {
		n := bind(t, "format_type(NULL::oid,0)")
		var order []int
		failure := errors.New("demanded child")
		children := []expr.Expr{
			typeDescribeObserved{Info: expr.InfoOf(n.Args[0]), value: sqltype.Null(n.Args[0].ResultUsage().Type), index: 1, order: &order},
			typeDescribeObserved{Info: expr.InfoOf(n.Args[1]), index: 2, order: &order, failure: failure},
		}
		p := expr.Properties{Volatility: expr.Volatile, RowDependent: true, ParameterDependent: true, StageDependent: true, DomainDependent: true, RuntimeDomainConversion: true}
		second := children[1].(typeDescribeObserved)
		second.Info.Property = p
		children[1] = second
		info := n.Info
		info.Property = expr.MergeProperties(expr.Properties{Volatility: expr.Stable}, p)
		node, err := n.Rebuild(info, children)
		if err != nil || node.Properties() != info.Property {
			t.Fatal("child properties erased", err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
		if _, err := node.Eval(&evaluation); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{1, 2}) {
			t.Fatal("NULL hid child demand/order", order, err)
		}
		order = nil
		typeof := bind(t, "pg_typeof(1)")
		child := typeDescribeObserved{Info: expr.InfoOf(typeof.Args[0]), index: 3, order: &order, failure: failure}
		typeof.Args = []expr.Expr{child}
		if _, err := typeof.Eval(&evaluation); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{3}) {
			t.Fatal("typeof erased child", order, err)
		}
	})
	t.Run("concrete witness follows rebuilding", func(t *testing.T) {
		n := bind(t, "pg_typeof(1)")
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		child := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "typed")}
		node, err := n.Rebuild(n.Info, []expr.Expr{child})
		if err != nil {
			t.Fatal(err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
		value, err := node.Eval(&evaluation)
		id, failure := value.TypeOID()
		if err != nil || failure != nil || id != uint32(sqltype.Text) {
			t.Fatal("stale logical identity", id, err, failure)
		}
		textSQL, err := stored.DefaultSQL(ctx, e.CatalogRead(), node)
		if err != nil || textSQL != "pg_catalog.pg_typeof(CAST('typed' AS TEXT))" {
			t.Fatal("concrete transport lost", textSQL, err)
		}
	})
}

type typeDescribeObserved struct {
	expr.Info
	value   sqltype.Value
	index   int
	order   *[]int
	failure error
}

func (n typeDescribeObserved) Eval(expr.Context) (sqltype.Value, error) {
	*n.order = append(*n.order, n.index)
	return n.value, n.failure
}
