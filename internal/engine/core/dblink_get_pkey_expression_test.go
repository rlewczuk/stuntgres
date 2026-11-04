package core_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// The value is genuine typed input; only argument demand is observed here.
type pkeyDemandArgument struct {
	plan.LiteralExpr
	demands *int
}

func (a pkeyDemandArgument) Eval(expr.Context) (sqltype.Value, error) {
	(*a.demands)++
	return a.Value, nil
}

func TestDblinkGetPkeyExpression(t *testing.T) {
	e := engine.New()
	ext := dblinkUnitInstall(t, e)
	view := e.CatalogRead()
	n := pkeyUnitExpression(t, e)
	if n.Procedure != view.MustProcedure(ext.Members[5]) || n.ResultUsage().Type != view.MustType(ext.Members[3]).SQLType || n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
		t.Fatal("carrier lost immutable installed source contract", n)
	}
	if source, target := n.SourceAdmission(); !source || !target {
		t.Fatal("source admission lost")
	}
	if err := stored.ValidateInstalledDblinkGetPkey(n, view); err != nil {
		t.Fatal(err)
	}
	anonymous, err := sqltype.RecordShape([]sqltype.CompositeMember{n.Usage.Type.CompositeMember(0), n.Usage.Type.CompositeMember(1)})
	if err != nil {
		t.Fatal(err)
	}
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		next, err := rebuild(n.Info, n.Children())
		if err != nil || !reflect.DeepEqual(next, n) {
			t.Fatal("immutable reconstruction changed identity", next, err)
		}
		bad := n.Info
		bad.RoutineReference = nil
		if _, err := rebuild(bad, n.Children()); err == nil {
			t.Fatal("reconstruction accepted lost declaration")
		}
		if _, err := rebuild(n.Info, nil); err == nil {
			t.Fatal("reconstruction accepted absent argument")
		}
		bad = n.Info
		bad.Usage = anonymous.Usage()
		if _, err := rebuild(bad, n.Children()); err == nil {
			t.Fatal("reconstruction accepted anonymous nominal substitute")
		}
	}
	copy, err := stored.RemapExpression(n, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil || !reflect.DeepEqual(copy, n) {
		t.Fatal("identity remap changed source", copy, err)
	}
	var refs []ids.ObjectRef
	canonical, err := (stored.ScalarRenderer{Dependencies: &refs}).ScalarFunctionSQL(context.Background(), view, n)
	if err != nil || !strings.Contains(canonical, `"public"."dblink_get_pkey"(`) {
		t.Fatal("nominal source did not render", canonical, err)
	}
	deps := stored.DependRefs{Context: context.Background(), Catalog: view}
	deps.Expression(n)
	if deps.Err != nil {
		t.Fatal(deps.Err)
	}
	for _, address := range []ids.ObjectAddress{n.Ref.Address, ext.Members[3]} {
		if _, ok := deps.Addresses[address]; !ok {
			t.Fatal("missing genuine callable/nominal dependency", address)
		}
	}
	for address := range deps.Addresses {
		if address.ClassID == ids.RelationClassID && address != view.MustType(ext.Members[3]).OwningRelation {
			t.Fatal("literal runtime relation acquired static dependency", address)
		}
	}
	for _, mutate := range []func(*plan.InstalledDblinkGetPkeyExpr){
		func(n *plan.InstalledDblinkGetPkeyExpr) { n.Ref.Epoch++ },
		func(n *plan.InstalledDblinkGetPkeyExpr) { n.RoutineReference = nil },
		func(n *plan.InstalledDblinkGetPkeyExpr) { n.Usage = n.Arg.ResultUsage() },
		func(n *plan.InstalledDblinkGetPkeyExpr) {
			n.Procedure = view.MustProcedure(ext.Members[0])
			n.RoutineReference = n.Procedure
			n.Ref = view.Owner().Domain.Ref(view.Epoch(), n.Procedure.Address())
		},
	} {
		bad := n
		mutate(&bad)
		if stored.ValidateInstalledDblinkGetPkey(bad, view) == nil {
			t.Fatal("forged carrier admitted", bad)
		}
	}
	state := catalog.CloneState(view.State())
	textAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Text)}
	textOwner := *view.MustType(textAddress)
	generation := *textOwner.SQLType
	textOwner.SQLType = &generation
	state.SetDescriptor(textAddress, &textOwner)
	if changed, err := typedef.ReownDomainTypes(state, nil, false, nil, nil); err != nil || !changed {
		t.Fatal("new element generation did not rebuild nominal companions", changed, err)
	}
	// Production reowns scalar declarations after rebuilding nominal codecs
	// (core.ReownStagedTypeGenerationsLocked). Keep the backing relation's
	// column generation coherent with the result codec before validating it.
	if err := stored.ReownScalarTypes(state, nil, nil); err != nil {
		t.Fatal("new generation did not reown backing declarations", err)
	}
	typed := e.Catalog.Read(state)
	next, changed, err := stored.RetagTypes(typed, n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d == nil {
			return nil
		}
		current, _ := typed.LookupType(d.ID())
		return current
	})
	if err != nil || !changed {
		t.Fatal("actual type-generation retag failed", changed, err)
	}
	fresh := next.(plan.InstalledDblinkGetPkeyExpr)
	if fresh.Procedure != n.Procedure || fresh.Ref != n.Ref || fresh.ResultUsage().Type == n.ResultUsage().Type || fresh.Arg.ResultUsage().Type != textOwner.SQLType || fresh.ResultUsage().Type != typed.MustType(ext.Members[3]).SQLType || typed.MustType(ext.Members[4]).SQLType.Element() != fresh.ResultUsage().Type {
		t.Fatal("retag lost nominal/array or installed authority")
	}
	if err := stored.ValidateInstalledDblinkGetPkey(fresh, typed); err != nil {
		t.Fatal(err)
	}
	if got, err := (stored.ScalarRenderer{}).ScalarFunctionSQL(context.Background(), typed, fresh); err != nil || got != canonical {
		t.Fatal("retag changed canonical source", got, err)
	}
	enginetest.Run(t, e, "CREATE TABLE pkey_demand(a integer,b integer,PRIMARY KEY(b,a))")
	reader, _ := blockingUnitOwner(t, e)
	scope := pgrowlocksUnitScope(t, e, reader)
	e.Mu.Lock()
	auth, err := scope.RoutineAuthorizationLocked()
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	scope.RoutineAuth = auth
	scope.RoutineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
	evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: scope.Op.Context(), Host: core.HostOf(scope), Catalog: auth.Catalog, Input: scope.Input, Output: scope.Output}}
	if err := exec.BeginRoutineInitialization(&evaluation); err != nil {
		t.Fatal("real source initializer", err)
	}
	var retained []sqltype.Value
	for _, null := range []bool{false, true} {
		demands := 0
		value := sqltype.String(n.Arg.ResultUsage().Type, "pkey_demand")
		if null {
			value = sqltype.Null(value.Type())
		}
		demand := n
		demand.Arg = pkeyDemandArgument{LiteralExpr: plan.LiteralExpr{Info: expr.InfoOf(n.Arg), Value: value}, demands: &demands}
		if err := exec.AdmitRoutineCall(&evaluation, demand, false, false); err != nil {
			t.Fatal("real source initialization", err)
		}
		cursor, err := demand.OpenSource(&evaluation)
		if err != nil {
			t.Fatal("real installed source open", err)
		}
		if cursor.Type != n.Usage.Type || cursor.State != nil {
			t.Fatal("source lost nominal metadata or retained runtime authority")
		}
		count := 0
		for {
			row, present, err := demand.AdvanceSource(context.Background(), &evaluation, &cursor)
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				if row.Type() != n.Usage.Type || !row.IsNull() {
					t.Fatal("empty source lost typed EOF")
				}
				break
			}
			position, positionErr := row.CompositeField(0).Integer()
			name, nameErr := row.CompositeField(1).Text()
			if row.Type() != n.Usage.Type || positionErr != nil || nameErr != nil || count >= 2 || position != int64(count+1) || name != []string{"b", "a"}[count] {
				t.Fatal("source lost selected ordered key metadata", row, positionErr, nameErr)
			}
			retained = append(retained, row)
			count++
			if count == 1 {
				enginetest.Run(t, e, "ALTER TABLE pkey_demand RENAME a TO renamed_a")
			}
		}
		cursor.Close()
		want := 2
		if null {
			want = 0
		}
		if demands != 1 || count != want {
			t.Fatal("source argument repeated or strict cardinality changed", demands, count)
		}
	}
	for i, row := range retained {
		position, positionErr := row.CompositeField(0).Integer()
		name, nameErr := row.CompositeField(1).Text()
		if positionErr != nil || nameErr != nil || position != int64(i+1) || name != []string{"b", "a"}[i] {
			t.Fatal("source close or NULL execution changed old output", row, positionErr, nameErr)
		}
	}
}
