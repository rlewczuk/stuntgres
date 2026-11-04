package schemacmds_test

import (
	"context"
	"errors"
	"math"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestEarthQueryDiagnosticPresentation(t *testing.T) {
	e, ext, earth := earthUnitInstalled(t)
	view := e.CatalogRead()
	query := view.MustProcedure(ext.Members[extension.EarthBoxProcedureSlot])
	helper, err := extension.EarthQueryConversionHelper(view, query)
	if err != nil {
		t.Fatal(err)
	}
	floating, _ := view.LookupType(sqltype.Float8)
	base := earth.SQLType.DomainBase().Type
	center, err := base.Input(context.Background(), "(6378168)", -1)
	if err != nil {
		t.Fatal(err)
	}
	center, err = earth.SQLType.Wrap(center)
	if err != nil {
		t.Fatal(err)
	}
	tiny, _ := sqltype.Float(floating, math.SmallestNonzeroFloat64)
	state := catalog.CloneState(view.State())
	for _, pair := range []struct {
		old  *catalog.Procedure
		name string
	}{{query, "box_now"}, {helper, "chord_now"}} {
		n := *pair.old
		n.ObjectName = pair.name
		n.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
		n.ObjectRevision = pair.old.Revision() + 1
		state.UnbindName(catalog.DescriptorNameKey(pair.old))
		state.SetDescriptor(n.Address(), &n)
		state.BindName(catalog.DescriptorNameKey(&n), n.Address())
	}
	current := view.WithState(state)
	source, evaluation := earthDiagnosticSource(t, e, view, sqltype.EarthBox)
	for _, tc := range []struct {
		first, radius expr.Properties
		mode          functions.EarthBoxContext
		demand        bool
		outer         string
	}{
		{expr.Properties{}, expr.Properties{}, functions.EarthBoxInlining, true, " during inlining"},
		{expr.Properties{ParameterDependent: true}, expr.Properties{}, functions.EarthBoxStatement, false, " statement 1"},
		{expr.Properties{ParameterDependent: true}, expr.Properties{ParameterDependent: true}, functions.EarthBoxStatement, false, " statement 1"},
		{expr.Properties{ParameterDependent: true}, expr.Properties{RowDependent: true}, functions.EarthBoxStatement, false, " statement 1"},
		{expr.Properties{RowDependent: true}, expr.Properties{RowDependent: true}, functions.EarthBoxHelperOnly, true, ""},
		{expr.Properties{RowDependent: true}, expr.Properties{}, functions.EarthBoxInlining, true, " during inlining"},
		{expr.Properties{RowDependent: true}, expr.Properties{ParameterDependent: true}, functions.EarthBoxInlining, true, " during inlining"},
		{expr.Properties{StageDependent: true}, expr.Properties{}, functions.EarthBoxHelperOnly, true, ""},
	} {
		args := []expr.Expr{
			plan.LiteralExpr{Info: expr.Info{Usage: earth.SQLType.Usage(), Property: tc.first}, Value: center},
			plan.LiteralExpr{Info: expr.Info{Usage: floating.Usage(), Property: tc.radius}, Value: tiny},
		}
		info := source
		info.Usage, info.RoutineReference = base.Usage(), query
		call, err := functions.NewEarthCoordinate(info, sqltype.EarthBox, earth.SQLType, args)
		if err != nil || call.BoxContext != tc.mode || call.BoxConversionDemand != tc.demand {
			t.Fatal("independent construction provenance", call.BoxContext, call.BoxConversionDemand, err)
		}
		nullArgs := append([]expr.Expr(nil), args...)
		nullArgs[0] = plan.LiteralExpr{Info: expr.Info{Usage: earth.SQLType.Usage(), Property: tc.first}, Value: sqltype.Null(earth.SQLType)}
		nullCall, err := functions.NewEarthCoordinate(call.Info, sqltype.EarthBox, earth.SQLType, nullArgs)
		if err != nil || nullCall.BoxContext != tc.mode || nullCall.BoxConversionDemand != tc.demand {
			t.Fatal("NULL changed captured demand/presentation", err)
		}
		wrapper := plan.InstalledEarthExpr{EarthCoordinate: nullCall, Procedure: query, Ref: e.Catalog.Domain.Ref(view.Epoch(), query.Address())}
		c := evaluation(view)
		if _, changed, err := exec.FoldStrictParameterNull(c, wrapper); err != nil || tc.demand && changed {
			t.Fatal("outer strict proof erased independent helper demand", changed, err)
		}
		value, err := wrapper.Eval(c)
		var diagnostic *sqlstate.Error
		if tc.demand {
			if !errors.As(err, &diagnostic) || diagnostic.Code != "22003" {
				t.Fatal("NULL erased expanded conversion demand", err)
			}
		} else if err != nil || !value.IsNull() || value.Type() != base {
			t.Fatal("parameter statement lost typed strict NULL", err)
		}
		if !tc.demand && (tc.radius.ParameterDependent || tc.radius.RowDependent) {
			for _, snapshot := range []struct {
				view   catalog.Read
				helper string
			}{{view, "gc_to_sec"}, {current, "chord_now"}} {
				frame := evaluation(snapshot.view)
				for _, generic := range []bool{false, true, false, true, false} {
					frame.PreserveParameterDemand = generic
					demand, presentation := wrapper.BoxExecutionRoute(generic)
					wantPresentation := functions.EarthBoxStatement
					if generic {
						wantPresentation = functions.EarthBoxHelperOnly
					}
					if demand != generic || presentation != wantPresentation {
						t.Fatal("command route did not honor captured generic policy", demand, presentation)
					}
					if _, changed, err := exec.FoldStrictParameterNull(frame, wrapper); err != nil || generic && changed {
						t.Fatal("generic strict proof erased helper demand", changed, err)
					}
					value, err := wrapper.Eval(frame)
					var d *sqlstate.Error
					if generic {
						want := `SQL function "` + snapshot.helper + `" statement 1`
						if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value out of range: underflow" || d.Context != want {
							t.Fatal("generic helper-only NULL demand", err, want)
						}
						bare := wrapper
						bare.Procedure = nil
						if _, _, err := exec.FoldStrictParameterNull(frame, bare); err == nil {
							t.Fatal("generic policy admitted unauthenticated demand")
						}
						origin := &sqlstate.Error{Code: "23514", Message: "child-owned", Context: "original child"}
						broken := wrapper
						broken.Args = append([]expr.Expr(nil), wrapper.Args...)
						broken.Args[0] = earthDiagnosticArgumentExpr{Info: expr.Info{Usage: earth.SQLType.Usage(), Property: tc.first}, failure: origin}
						if _, err := broken.Eval(frame); err != origin {
							t.Fatal("generic policy took ownership of child failure", err)
						}
					} else if err != nil || !value.IsNull() || value.Type() != base {
						t.Fatal("custom execution inherited previous generic demand", err)
					}
					if wrapper.BoxConversionDemand || wrapper.BoxContext != functions.EarthBoxStatement || wrapper.ValidateStoredProvider() != nil {
						t.Fatal("command policy mutated stored provenance or authority")
					}
				}
			}
		}
		for _, rewrite := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return wrapper.Rebuild(wrapper.Info, nullArgs) },
			func() (expr.Expr, error) { return wrapper.Substitute(wrapper.Info, nullArgs) },
			func() (expr.Expr, error) {
				next, _, err := wrapper.Retag(wrapper.Info, nullArgs, false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
				return next, err
			},
		} {
			next, err := rewrite()
			n, ok := next.(plan.InstalledEarthExpr)
			if err != nil || !ok || n.BoxContext != tc.mode || n.BoxConversionDemand != tc.demand || n.ValidateStoredProvider() != nil {
				t.Fatal("rewrite lost independent demand/presentation authority", err)
			}
		}
		for _, snapshot := range []struct {
			view        catalog.Read
			box, helper string
		}{{view, "earth_box", "gc_to_sec"}, {current, "box_now", "chord_now"}} {
			c := evaluation(snapshot.view)
			_, err := call.Eval(c)
			want := `SQL function "` + snapshot.helper + `" statement 1`
			if tc.outer != "" {
				want += "\nSQL function \"" + snapshot.box + "\"" + tc.outer
			}
			var d *sqlstate.Error
			if !errors.As(err, &d) || d.Code != "22003" || d.Message != "value out of range: underflow" || d.Context != want {
				t.Fatal("authenticated current error context", err, want)
			}
			origin := &sqlstate.Error{Code: "23514", Message: "child-owned", Context: "original child"}
			broken := call
			broken.Args = append([]expr.Expr(nil), args...)
			broken.Args[0] = earthDiagnosticArgumentExpr{Info: expr.Info{Usage: earth.SQLType.Usage()}, failure: origin}
			if _, err := broken.Eval(c); err != origin {
				t.Fatal("query took ownership of child failure", err)
			}
			bad := call
			bad.RoutineReference = nil
			if _, err := bad.Eval(c); err == nil || errors.As(err, &d) {
				t.Fatal("missing identity gained query/helper presentation", err)
			}
		}
		badMode := call
		badMode.BoxContext = (call.BoxContext + 1) % 3
		if err := badMode.Validate(); err == nil {
			t.Fatal("forged wrapper mode accepted")
		}
		badDemand := wrapper
		badDemand.BoxConversionDemand = !wrapper.BoxConversionDemand
		if err := badDemand.ValidateStoredProvider(); err == nil {
			t.Fatal("forged semantic demand accepted")
		}
		if wrapper.SameGroup(badDemand, func(expr.Expr, expr.Expr) bool { return true }, false) {
			t.Fatal("grouping erased semantic demand distinction")
		}
		if tc.demand {
			bare := wrapper
			bare.Procedure = nil
			if _, _, err := exec.FoldStrictParameterNull(c, bare); err == nil {
				t.Fatal("unauthenticated wrapper acquired independent demand")
			}
		}
	}
}
