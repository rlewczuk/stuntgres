package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestGINCleanPendingListProtocol(t *testing.T) {
	e := engine.New()
	for _, query := range []string{
		"CREATE TABLE strict_null_rows(v integer)",
		"CREATE SEQUENCE strict_null_steps START 1",
		"CREATE TABLE gin_protocol_rows(a integer[])",
		"CREATE INDEX gin_protocol_i ON gin_protocol_rows USING gin(a)",
	} {
		enginetest.Run(t, e, query)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { scope.Finish(nil) })
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows != nil {
		result.Rows.Close()
	}
	for _, operand := range []string{"$1::regclass", "'gin_protocol_i'::regclass", "NULL::regclass"} {
		t.Run(operand, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "pg_catalog.gin_clean_pending_list("+operand+")")
			n := node.(functions.GinCleanPendingList)
			declaration, err := exec.ExpressionRoutineDescriptor(n)
			if err != nil || declaration == nil || declaration.Builtin == nil {
				t.Fatal("selected declaration", declaration, err)
			}
			d := declaration.Builtin
			if d.Identity.ID != 3789 || d.Identity.Namespace != 11 || d.Identity.Name != "gin_clean_pending_list" || d.Identity.Signature != "2205" || d.Result != 20 || !d.Strict || d.Volatility != expr.Volatile || d.Parallel != 'u' || d.ReturnsSet || d.Providers != functions.CatalogRoutineProvider {
				t.Fatal("released identity and actual provider", d)
			}
			if declaration.Display.Language != catalog.InternalRoutineLanguage || declaration.Display.Source != "gin_clean_pending_list" || declaration.Cost != 1 {
				t.Fatal("released display and cost", declaration)
			}
			if declaration.Owner.ID() != 10 {
				t.Fatal("released builtin owner", declaration.Owner)
			}
			if n.Validate() != nil || n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Volatile || n.StrictNull() != expr.StrictAfter || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.ResultUsage() != n.ResultUsage().Type.Usage() || n.ResultUsage().Type.ID() != sqltype.Int8 || n.Arg.ResultUsage().Type.ID() != sqltype.Regclass || n.CallCost() != 1 || !n.LooksLikeFunction() {
				t.Fatal("typed recipe", n)
			}
			if admitted, set := n.SourceAdmission(); !admitted || set {
				t.Fatal("ordinary scalar admission", admitted, set)
			}
			if len(n.Children()) != 1 || !reflect.DeepEqual(n.Children()[0], n.Arg) {
				t.Fatal("single concrete operand")
			}
			original := n
			info := n.Info
			info.Occurrence++
			rebuilt, err := n.Rebuild(info, n.Children())
			if err != nil {
				t.Fatal(err)
			}
			next := rebuilt.(functions.GinCleanPendingList)
			if next.Info != info || next.RoutineReference != n.RoutineReference || !reflect.DeepEqual(next.Arg, n.Arg) || !reflect.DeepEqual(n, original) || !n.SameGroup(next, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("immutable reconstruction/provenance/group")
			}
			for _, children := range [][]expr.Expr{nil, {n.Arg, n.Arg}, {nil}} {
				if _, err := n.Rebuild(n.Info, children); err == nil {
					t.Fatal("malformed children accepted", children)
				}
			}
			bad := n
			bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Int4)
			if bad.Validate() == nil {
				t.Fatal("non-INT8 result accepted")
			}
			if _, err := n.Rebuild(bad.Info, n.Children()); err == nil {
				t.Fatal("invalid result reconstruction accepted")
			}
			bad = n
			bad.Arg = nil
			if bad.Validate() == nil {
				t.Fatal("missing target accepted")
			}
			if _, err := bad.Render(); err == nil {
				t.Fatal("invalid recipe rendered")
			}
			if _, _, err := bad.Capture(expr.CaptureFunction); err == nil {
				t.Fatal("invalid recipe captured")
			}
			wrong, _, _ := strictNullUnitBind(t, e, op, scope, "1")
			bad = n
			bad.Arg = wrong
			if bad.Validate() == nil {
				t.Fatal("non-REGCLASS target accepted")
			}
			bad = n
			bad.Property.Volatility = expr.Immutable
			if bad.Validate() == nil {
				t.Fatal("nonvolatile recipe accepted")
			}
			render, err := n.Render()
			if err != nil || render.Name != "gin_clean_pending_list" || render.Tree != "GinCleanPendingList" || render.Form != expr.FormCall || len(render.Inputs) != 1 || len(render.Args) != 1 || !reflect.DeepEqual(render.Inputs[0], n.Arg) {
				t.Fatal("ordinary rendering", render, err)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 1 || capture.Check(n, 1) != nil {
				t.Fatal("function capture", capture, err)
			}
			if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
				t.Fatal("operator capture", ok, err)
			}
			retagged, changed, err := stored.RetagTypes(e.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil || changed || !reflect.DeepEqual(retagged, n) {
				t.Fatal("identity retag changed recipe/provenance", retagged, err)
			}
			var jumble ginCleanupJumble
			n.JumbleFields(&jumble)
			if !reflect.DeepEqual(jumble.fields, []string{"functions.GinCleanPendingList"}) {
				t.Fatal("owned jumble discriminator", jumble)
			}
			if operand == "$1::regclass" {
				evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
				value, err := n.Eval(&evaluation)
				if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
					t.Fatal("strict runtime typed NULL", value, err)
				}
				constant, _, _ := strictNullUnitBind(t, e, op, scope, "'gin_protocol_i'::regclass")
				evaluation.Parameters[0], err = constant.Eval(&evaluation)
				if err != nil {
					t.Fatal(err)
				}
			}
			value, err := n.Eval(&evaluation)
			if operand == "NULL::regclass" {
				if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
					t.Fatal("literal typed NULL", value, err)
				}
				cursor, err := n.OpenSource(&evaluation)
				if err != nil || cursor.Type != n.Usage.Type || !cursor.Value.IsNull() {
					t.Fatal("scalar typed NULL source", cursor, err)
				}
				for _, planner := range []func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error){exec.SimplifyConditional, exec.PreflightCoercionsWithPolicy} {
					planned, changed, err := planner(&evaluation, n, true)
					if err != nil || !changed || reflect.TypeOf(planned) == reflect.TypeOf(n) {
						t.Fatal("strict NULL must precede FoldNever", planned, changed, err)
					}
					if declaration, err := exec.ExpressionRoutineDescriptor(planned); err != nil || declaration != nil {
						t.Fatal("erased NULL retained invocation", declaration, err)
					}
					value, err := planned.Eval(&evaluation)
					if err != nil || !value.IsNull() || value.Type() != n.Usage.Type {
						t.Fatal("folded INT8 NULL", value, err)
					}
				}
			} else {
				nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "0A000", Message: "gin_clean_pending_list physical GIN pending-list cleanup is unsupported in embedded mode"})
				if value.Type() != nil {
					t.Fatal("failed cleanup manufactured result", value)
				}
				planned, _, err := exec.PreflightCoercionsWithPolicy(&evaluation, n, true)
				if err != nil || reflect.TypeOf(planned) != reflect.TypeOf(n) {
					t.Fatal("preflight demanded cleanup", planned, err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			evaluation.Context = ctx
			if _, err := n.Eval(&evaluation); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation precedence", err)
			}
			if !reflect.DeepEqual(n, original) {
				t.Fatal("evaluation mutated recipe")
			}
		})
	}
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "gin_clean_pending_list((1/0)::oid::regclass)")
	child := node.(functions.GinCleanPendingList).Arg
	_, expected := child.Eval(&evaluation)
	_, actual := node.Eval(&evaluation)
	if expected == nil || !reflect.DeepEqual(expected, actual) {
		t.Fatal("parent replaced child diagnostic", actual, expected)
	}
	nullCountUnitDiagnostic(t, actual, sqlstate.Error{Code: "22012", Message: "division by zero"})
	// A genuine volatile argument advances once even though its target fails.
	node, _, evaluation = strictNullUnitBind(t, e, op, scope, "gin_clean_pending_list(nextval('strict_null_steps')::oid::regclass)")
	if value, err := node.Eval(&evaluation); err == nil || value.Type() != nil {
		t.Fatal("invalid target succeeded", value, err)
	}
	last, _, evaluation := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps')")
	value, err := last.Eval(&evaluation)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := value.Integer(); err != nil || n != 1 {
		t.Fatal("operand was not evaluated exactly once", n, err)
	}
}

type ginCleanupJumble struct{ fields []string }

func (j *ginCleanupJumble) JumbleText(s string)  { j.fields = append(j.fields, s) }
func (*ginCleanupJumble) JumbleUint64(uint64)    {}
func (*ginCleanupJumble) JumbleBool(bool)        {}
func (*ginCleanupJumble) JumbleConst(sqltype.ID) {}
