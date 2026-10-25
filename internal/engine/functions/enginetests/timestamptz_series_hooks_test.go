package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestTimestamptzSeriesHooks(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	other, _, _ := strictNullUnitFixture(t)
	for _, text := range []string{
		"generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day')",
		"generate_series(TIMESTAMPTZ '2024-03-09 12:00-05',TIMESTAMPTZ '2024-03-11 13:00-04',INTERVAL '1 day','America/New_York')",
	} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, text)
		s := node.(plan.SeriesExpr)
		before := s
		before.Args = append([]expr.Expr(nil), s.Args...)
		intrinsic := expr.Stable
		if len(s.Args) == 4 {
			intrinsic = expr.Immutable
		}
		if s.IntrinsicVolatility() != intrinsic || s.PlanningFold() != expr.FoldNever || s.PreflightFold() != expr.PreflightChildren || !s.Properties().StageDependent {
			t.Fatal("intrinsic protocol", s)
		}
		rebuilt, err := s.Rebuild(s.Info, append([]expr.Expr(nil), s.Args...))
		if err != nil || !reflect.DeepEqual(s, rebuilt) {
			t.Fatal(rebuilt, err)
		}
		if _, err := s.Substitute(s.Info, s.Args[:len(s.Args)-1]); err == nil {
			t.Fatal("changed arity")
		}
		if !s.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) || !s.SameSourceOccurrence(rebuilt) {
			t.Fatal("lost occurrence")
		}
		rendered, err := s.Render()
		if err != nil || len(rendered.Inputs) != len(s.Args) || len(rendered.Args) != len(s.Args) {
			t.Fatal(rendered, err)
		}
		deparsed, err := (stored.Deparser{View: e.CatalogRead()}).SQL(context.Background(), s, stored.DeparseParent{})
		if err != nil {
			t.Fatal(err)
		}
		rebound, _, _ := strictNullUnitBind(t, e, op, scope, deparsed)
		if len(rebound.(plan.SeriesExpr).Args) != len(s.Args) || rebound.(plan.SeriesExpr).IntrinsicVolatility() != intrinsic {
			t.Fatal("deparse arity", deparsed, rebound)
		}
		remapped, _, err := stored.RetagTypes(other.CatalogRead(), s, func(old *sqltype.Descriptor) *sqltype.Descriptor {
			if old == nil {
				return nil
			}
			d, _ := other.CatalogRead().LookupType(old.ID())
			return d
		})
		if err != nil {
			t.Fatal(err)
		}
		next := remapped.(plan.SeriesExpr)
		if len(next.Args) != len(s.Args) || next.Usage.Type == s.Usage.Type {
			t.Fatal("retag result/arity")
		}
		for i := range s.Args {
			if next.Args[i].ResultUsage().Type == s.Args[i].ResultUsage().Type || next.Args[i].ResultUsage().Type.ID() != s.Args[i].ResultUsage().Type.ID() {
				t.Fatal("incomplete retag", i)
			}
		}
		for i := 2; i < len(s.Args); i++ {
			bad := append([]expr.Expr(nil), s.Args...)
			bad[i] = s.Args[0]
			if _, err := s.Rebuild(s.Info, bad); err == nil {
				t.Fatal("wrong heterogeneous type", i)
			}
			foreign := append([]expr.Expr(nil), s.Args...)
			foreign[i] = next.Args[i]
			mixed, err := s.Rebuild(s.Info, foreign)
			if err != nil {
				t.Fatal("shape invented authority", err)
			}
			if err := exec.AdmitRoutineCall(&evaluation, mixed, false, false); err != nil {
				t.Fatal("lost original routine authority", err)
			}
			current := e.CatalogRead()
			if stored.ValidExpressionUsage(current, mixed.(plan.SeriesExpr).Args[i].ResultUsage()) || !stored.ValidExpressionUsage(current, s.Args[i].ResultUsage()) {
				t.Fatal("foreign child accepted", i)
			}
			later := current.WithState(catalog.CloneState(current.State()))
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(s.Args[i].ResultUsage().Type.ID())}
			replacement := *later.State().MustType(address)
			replacement.SQLType = next.Args[i].ResultUsage().Type
			later.State().SetDescriptor(address, &replacement)
			authority := *core.RoutineAuthorityView(&evaluation)
			authority.View.Recapture(later)
			probe := evaluation
			probe.Catalog, probe.RoutineAuthority = later, &authority
			if err := exec.AdmitRoutineCall(&probe, s, false, false); err != nil {
				t.Fatal("child-only generation changed routine identity", err)
			}
			if stored.ValidExpressionUsage(later, s.Args[i].ResultUsage()) || !stored.ValidExpressionUsage(later, next.Args[i].ResultUsage()) || !stored.ValidExpressionUsage(later, s.ResultUsage()) {
				t.Fatal("stale child not isolated", i)
			}
		}
		if len(s.Args) == 4 {
			changed := append([]expr.Expr(nil), s.Args...)
			d := s.Args[3].ResultUsage().Type
			changed[3] = plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: sqltype.String(d, "UTC")}
			variant, err := s.Rebuild(s.Info, changed)
			if err != nil {
				t.Fatal(err)
			}
			if s.SameGroup(variant, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
				t.Fatal("fourth formal omitted from identity")
			}
		}
		// Evaluate each formal exactly once in source order even when strict NULL
		// suppresses provider validation, and never suppress a sibling error.
		var visits []int
		args := make([]expr.Expr, len(s.Args))
		for i, arg := range s.Args {
			i, arg := i, arg
			args[i] = queryProbe(arg.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
				visits = append(visits, i)
				if i == 0 {
					return sqltype.Null(arg.ResultUsage().Type), nil
				}
				return arg.Eval(c)
			})
		}
		probe := s
		probe.Args = args
		cursor, err := probe.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		wantVisits := []int{0, 1, 2}
		if len(args) == 4 {
			wantVisits = append(wantVisits, 3)
		}
		if !reflect.DeepEqual(visits, wantVisits) {
			t.Fatal("formal evaluation order", visits)
		}
		value, ok, err := cursor.Next(context.Background())
		if err != nil || ok || !value.IsNull() || value.Type() != s.Usage.Type {
			t.Fatal(value, ok, err)
		}
		cursor.Close()
		cursor.Close()
		sentinel := errors.New("sibling")
		probe.Args[1] = queryProbe(s.Args[1].ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, sentinel })
		if _, err := probe.OpenSource(&evaluation); err != sentinel {
			t.Fatal("strict NULL suppressed sibling", err)
		}
		if !reflect.DeepEqual(before, s) {
			t.Fatal("mutated original")
		}
	}
	volatile, _, _ := strictNullUnitBind(t, e, op, scope, "generate_series(TIMESTAMPTZ '2024-01-01+00',TIMESTAMPTZ '2024-01-02+00',INTERVAL '1 day',timeofday())")
	if volatile.(plan.SeriesExpr).IntrinsicVolatility() != expr.Immutable || volatile.Properties().Volatility != expr.Volatile || exec.PlannedVolatility(volatile) != expr.Volatile {
		t.Fatal("lowered volatile fourth operand", volatile)
	}
}
