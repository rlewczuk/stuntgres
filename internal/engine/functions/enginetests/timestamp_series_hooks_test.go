package enginetests_test

import (
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

func TestTimestampSeriesHooks(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "generate_series(TIMESTAMP '2020-01-01',TIMESTAMP '2020-01-02',INTERVAL '1 day')")
	s := node.(plan.SeriesExpr)
	before := s
	before.Args = append([]expr.Expr(nil), s.Args...)
	rebuilt, err := s.Rebuild(s.Info, append([]expr.Expr(nil), s.Args...))
	if err != nil || !reflect.DeepEqual(s, rebuilt) {
		t.Fatal(rebuilt, err)
	}
	if _, err := s.Substitute(s.Info, s.Args[:2]); err == nil {
		t.Fatal("lost required third formal")
	}
	bad := append([]expr.Expr(nil), s.Args...)
	bad[2] = s.Args[0]
	if _, err := s.Rebuild(s.Info, bad); err == nil {
		t.Fatal("timestamp accepted as interval")
	}
	if _, err := s.Render(); err != nil {
		t.Fatal(err)
	}
	if !s.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) || s.PlanningFold() != expr.FoldNever || s.IntrinsicVolatility() != expr.Immutable {
		t.Fatal("lost protocol")
	}
	other, _, _ := strictNullUnitFixture(t)
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
	if next.Usage.Type == s.Usage.Type || next.Args[2].ResultUsage().Type == s.Args[2].ResultUsage().Type || next.Args[2].ResultUsage().Type.ID() != sqltype.Interval {
		t.Fatal("incomplete heterogeneous remap")
	}
	// Isolate the heterogeneous child's provenance: result, both timestamp
	// formals and original routine authority remain genuine and current.
	foreignArgs := append([]expr.Expr(nil), s.Args...)
	foreignArgs[2] = next.Args[2]
	mixedNode, err := s.Rebuild(s.Info, foreignArgs)
	if err != nil {
		t.Fatal("shape validation must not invent catalog authority", err)
	}
	mixed := mixedNode.(plan.SeriesExpr)
	if err := exec.AdmitRoutineCall(&evaluation, mixed, false, false); err != nil {
		t.Fatal("probe lost original routine authority", err)
	}
	current := e.CatalogRead()
	if !stored.ValidExpressionUsage(current, mixed.ResultUsage()) ||
		!stored.ValidExpressionUsage(current, mixed.Args[0].ResultUsage()) ||
		!stored.ValidExpressionUsage(current, mixed.Args[1].ResultUsage()) {
		t.Fatal("foreign-step probe changed current timestamp descriptors")
	}
	if mixed.Args[2].ResultUsage().Type.ID() != sqltype.Interval ||
		stored.ValidExpressionUsage(current, mixed.Args[2].ResultUsage()) {
		t.Fatal("common current-catalog validation admitted equal-ID foreign interval")
	}

	// A later catalog snapshot in the SAME domain can replace just INTERVAL's
	// SQL descriptor. The retained old step then becomes stale, although its
	// routine manifest, timestamps and original readable snapshot remain valid.
	later := current.WithState(catalog.CloneState(current.State()))
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Interval)}
	replacement := *later.State().MustType(address)
	replacement.SQLType = next.Args[2].ResultUsage().Type
	later.State().SetDescriptor(address, &replacement)
	authority := *core.RoutineAuthorityView(&evaluation)
	// Recapture the genuine paired catalog/role authority, not just its
	// Catalog field: common access checks reject an unpaired snapshot first.
	authority.View.Recapture(later)
	probe := evaluation
	probe.Catalog, probe.RoutineAuthority = later, &authority
	if err := exec.AdmitRoutineCall(&probe, s, false, false); err != nil {
		t.Fatal("step-only generation change invalidated routine authority", err)
	}
	if !stored.ValidExpressionUsage(later, s.ResultUsage()) ||
		!stored.ValidExpressionUsage(later, s.Args[0].ResultUsage()) ||
		!stored.ValidExpressionUsage(later, s.Args[1].ResultUsage()) ||
		stored.ValidExpressionUsage(later, s.Args[2].ResultUsage()) {
		t.Fatal("common validation did not isolate stale interval descriptor")
	}
	if !stored.ValidExpressionUsage(current, s.Args[2].ResultUsage()) ||
		!stored.ValidExpressionUsage(later, mixed.Args[2].ResultUsage()) {
		t.Fatal("generation probes invalidated original snapshot or coherent replacement")
	}
	if !reflect.DeepEqual(before, s) {
		t.Fatal("mutated original")
	}
}
