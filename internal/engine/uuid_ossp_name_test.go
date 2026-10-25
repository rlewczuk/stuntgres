package engine

import (
	"context"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDOsspNameExpressionChildren(t *testing.T) {
	e, _ := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	for _, name := range []string{"uuid_generate_v3", "uuid_generate_v5"} {
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{literal("6ba7b810-9dad-11d1-80b4-00c04fd430c8"), literal("www.example.com")}}
		node, err := binder.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := node.(plan.InstalledUUIDOsspExpr)
		if len(n.Args) != 2 || n.Procedure.Signature != "2950,25" || !analyze.ValidInstalledUUIDOsspShape(n) || !analyze.ClosedUUIDDefault(n) || !plan.GenerationImmutable(n) || exec.PlannedVolatility(n) != expr.Immutable {
			t.Fatal("name shape/properties", n)
		}
		var children []expr.Expr
		plan.ExpressionChildren(n, func(child expr.Expr) { children = append(children, child) })
		if !reflect.DeepEqual(children, n.Args) {
			t.Fatal("name child walk", children)
		}
		if err := table.ValidateGenerationPlan(view, nil, n); err != nil {
			t.Fatal(err)
		}
		refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
		if err != nil || !slices.Contains(refs, n.Ref.Address) {
			t.Fatal("name dependency", refs, err)
		}
		raw.Args[1] = literal("www.widgets.com")
		other, err := binder.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		if analyze.SameGroupExpression(n, other) {
			t.Fatal("different names grouped as same leaf")
		}
		for _, rebuild := range []func() (expr.Expr, error){
			func() (expr.Expr, error) { return n.Rebuild(n.Info, n.Args) },
			func() (expr.Expr, error) { return n.Substitute(n.Info, n.Args) },
		} {
			next, err := rebuild()
			if err != nil {
				t.Fatal(err)
			}
			got, ok := next.(plan.InstalledUUIDOsspExpr)
			if !ok || got.Procedure != n.Procedure || got.Ref != n.Ref || got.RoutineReference != n.RoutineReference || len(got.Args) != 2 {
				t.Fatal("lost installed wrapper", next)
			}
		}
		retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil {
			t.Fatal(err)
		}
		if err := stored.ValidateInstalledUUIDOssp(retagged.(plan.InstalledUUIDOsspExpr), view); err != nil {
			t.Fatal(err)
		}
		mapped, err := stored.RemapExpression(n, func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch++; return ref }, nil)
		if err != nil {
			t.Fatal(err)
		}
		m := mapped.(plan.InstalledUUIDOsspExpr)
		if m.Ref.Epoch != n.Ref.Epoch+1 || m.Procedure != n.Procedure || len(m.Args) != 2 {
			t.Fatal("lost remapped name identity", m)
		}
		grouped, err := binder.RewriteGroup(n, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := stored.ValidateInstalledUUIDOssp(grouped.(plan.InstalledUUIDOsspExpr), view); err != nil {
			t.Fatal("grouped name shape", err)
		}
		if analyze.ClosedUUIDDefault(grouped) {
			t.Fatal("group-stage name accepted as closed default")
		}
		for _, mutate := range []func(*plan.InstalledUUIDOsspExpr){
			func(x *plan.InstalledUUIDOsspExpr) { x.Args = x.Args[:1] },
			func(x *plan.InstalledUUIDOsspExpr) { x.Args = []expr.Expr{x.Args[1], x.Args[0]} },
			func(x *plan.InstalledUUIDOsspExpr) { x.Name = "uuidv4" },
			func(x *plan.InstalledUUIDOsspExpr) { x.RoutineReference = nil },
			func(x *plan.InstalledUUIDOsspExpr) { x.Ref.Epoch = 0 },
			func(x *plan.InstalledUUIDOsspExpr) { p := *x.Procedure; p.Signature = "25,25"; x.Procedure = &p },
		} {
			bad := n
			mutate(&bad)
			if err := stored.ValidateInstalledUUIDOssp(bad, view); err == nil {
				t.Fatal("corrupt name expression accepted", bad)
			}
		}
		// A volatile child must survive binding and invalidate immutable consumers.
		raw.Args[1] = parser.CaseExpr{
			Arms: []parser.CaseArm{{
				Condition: parser.PredicateExpr{Kind: parser.IsNull, Left: parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "uuid_generate_v4"}}}}},
				Result:    literal("www.example.com"),
			}},
			Else: literal("www.widgets.com"),
		}
		volatile, err := binder.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		v := volatile.(plan.InstalledUUIDOsspExpr)
		if v.Properties().Volatility != expr.Volatile || v.IntrinsicVolatility() != expr.Immutable || exec.PlannedVolatility(v) != expr.Volatile || plan.GenerationImmutable(v) || analyze.ClosedUUIDDefault(v) || !analyze.ValidInstalledUUIDOsspShape(v) {
			t.Fatal("name child volatility lost", v)
		}
	}
}

func TestUUIDOsspNameProviderSignatures(t *testing.T) {
	source, ext := uuidOsspUnitInstalled(t)
	for _, address := range ext.Members {
		proc := source.CatalogRead().MustProcedure(address)
		if proc.Name() != "uuid_generate_v3" && proc.Name() != "uuid_generate_v5" {
			continue
		}
		if proc.Signature != "2950,25" || pgcatalog.ProcedureArity(proc) != 2 || proc.Volatility != expr.Immutable || !proc.Strict {
			t.Fatal("name declaration", proc)
		}
		for _, signature := range []catalog.ProcedureSignature{"", "25,25", "2950", "2950,25,25"} {
			bad := *proc
			bad.Signature = signature
			view := source.CatalogRead()
			view.State().SetDescriptor(address, &bad)
			err := extension.StockUUIDOssp.ValidateExtension(view, ext)
			view.State().SetDescriptor(address, proc)
			if err == nil {
				t.Fatal("wrong signature admitted", signature)
			}
		}
		// A genuine late two-argument collision must leave the published root
		// untouched, even though preceding members entered the private overlay.
		e := New()
		before := e.Catalog.State
		before.SetDescriptor(address, proc)
		key := catalog.DescriptorNameKey(proc)
		before.BindName(key, address)
		descriptors, names := before.DescriptorCount(), before.NameCount()
		stage := e.BeginCatalogStage(e.CatalogRead())
		parent := *ext
		e.Mu.Lock()
		err := extension.StockUUIDOssp.Stage(&extensionHost{engine: e, stage: stage}, &parent, ext.Owner)
		e.Mu.Unlock()
		state, ok := err.(interface{ SQLState() string })
		if !ok || state.SQLState() != "42723" || e.Catalog.State != before || before.DescriptorCount() != descriptors || before.NameCount() != names || before.Get(address) != proc || before.NameAddress(key) != address {
			t.Fatal("name collision published partial state", err)
		}
	}
}
