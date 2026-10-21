package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestUUIDOsspNamespaceExpressionIdentity(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	for _, name := range []string{"uuid_nil", "uuid_ns_dns", "uuid_ns_url", "uuid_ns_oid", "uuid_ns_x500"} {
		t.Run(name, func(t *testing.T) {
			binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			node, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}})
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledUUIDOsspExpr)
			if n.Name != name || !slices.Contains(ext.Members, n.Ref.Address) || n.Procedure.Name() != name || n.ResultUsage().Type.ID() != sqltype.UUID || exec.PlannedVolatility(n) != expr.Immutable || !plan.GenerationImmutable(n) || !analyze.ClosedUUIDDefault(n) {
				t.Fatal("constant shape/identity", n)
			}
			if carrier, err := exec.ExpressionRoutineDescriptor(n); err != nil || carrier != n.Procedure {
				t.Fatal("lost routine authority", carrier, err)
			}
			if err := ValidateInstalledUUIDOssp(n, view); err != nil {
				t.Fatal(err)
			}
			if err := validateGenerationPlan(view, nil, n); err != nil {
				t.Fatal(err)
			}
			refs, err := ExpressionDependRefs(context.Background(), view, n)
			if err != nil || !slices.Contains(refs, n.Ref.Address) {
				t.Fatal("missing installed dependency", refs, err)
			}
			retagged, _, err := RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, err := retagged.(plan.InstalledUUIDOsspExpr).Rebuild(n.Info, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := rebuilt.(plan.InstalledUUIDOsspExpr)
			if got.Name != name || got.Procedure != n.Procedure || got.Ref != n.Ref || exec.PlannedVolatility(got) != expr.Immutable {
				t.Fatal("rebuild/retag erased identity", got)
			}
			rendered, err := (scalarRenderer{}).SQL(context.Background(), view, got)
			if err != nil || !strings.Contains(rendered, name) || !strings.Contains(rendered, "public") {
				t.Fatal("symbolic constant", rendered, err)
			}
			foreign := n
			foreign.Ref.Epoch++
			if _, err := (scalarRenderer{}).SQL(context.Background(), view, foreign); err == nil {
				t.Fatal("renderer accepted foreign-epoch constant")
			}
			grouped, err := binder.RewriteGroup(n, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateInstalledUUIDOssp(grouped.(plan.InstalledUUIDOsspExpr), view); err != nil {
				t.Fatal("grouped constant", err)
			}
			for _, mutate := range []func(*plan.InstalledUUIDOsspExpr){
				func(x *plan.InstalledUUIDOsspExpr) { x.Name = "uuidv4" },
				func(x *plan.InstalledUUIDOsspExpr) { x.Property.Volatility = expr.Volatile },
				func(x *plan.InstalledUUIDOsspExpr) { x.RoutineReference = nil },
				func(x *plan.InstalledUUIDOsspExpr) { x.Ref.Epoch = 0 },
				func(x *plan.InstalledUUIDOsspExpr) { x.Usage.Type = nil },
				func(x *plan.InstalledUUIDOsspExpr) { copy := *x.Procedure; x.Procedure = &copy },
			} {
				bad := n
				mutate(&bad)
				if err := ValidateInstalledUUIDOssp(bad, view); err == nil {
					t.Fatal("forged constant accepted", bad)
				}
			}
		})
	}
}

func TestUUIDOsspNamespaceManifest(t *testing.T) {
	e, ext := uuidOsspUnitInstalled(t)
	view := e.CatalogRead()
	want := map[string]expr.Volatility{"uuid_generate_v4": expr.Volatile, "uuid_nil": expr.Immutable, "uuid_ns_dns": expr.Immutable, "uuid_ns_url": expr.Immutable, "uuid_ns_oid": expr.Immutable, "uuid_ns_x500": expr.Immutable}
	if len(ext.Members) != len(want) {
		t.Fatal("six-member manifest", len(ext.Members))
	}
	seen := make(map[string]bool)
	for _, address := range ext.Members {
		p := view.MustProcedure(address)
		vol, ok := want[p.Name()]
		if !ok || seen[p.Name()] || p.Volatility != vol || p.Signature != "" || p.Result != 2950 || !p.Strict || p.Namespace() != ext.TargetNamespace || p.Owner != ext.Owner {
			t.Fatal("member declaration", p)
		}
		seen[p.Name()] = true
		for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.ObjectName = "uuid_generate_v5" }, func(p *catalog.Procedure) { p.Volatility = expr.Stable }, func(p *catalog.Procedure) { p.Result = 25 }} {
			bad := *p
			mutate(&bad)
			view.State().SetDescriptor(address, &bad)
			err := extension.StockUUIDOssp.ValidateExtension(view, ext)
			view.State().SetDescriptor(address, p)
			if err == nil {
				t.Fatal("invalid selected member admitted", bad)
			}
		}
	}
	for _, members := range [][]ids.ObjectAddress{ext.Members[:5], append(slices.Clone(ext.Members), ext.Members[0]), append(slices.Clone(ext.Members[:5]), ext.Members[0])} {
		bad := *ext
		bad.Members = members
		if err := extension.StockUUIDOssp.ValidateExtension(view, &bad); err == nil {
			t.Fatal("missing/extra/duplicate manifest accepted")
		}
	}
}

func TestUUIDOsspNamespaceCollision(t *testing.T) {
	source, ext := uuidOsspUnitInstalled(t)
	for _, address := range ext.Members {
		proc := source.CatalogRead().MustProcedure(address)
		t.Run(proc.Name(), func(t *testing.T) {
			e := New()
			before := e.catalog.state
			before.SetDescriptor(address, proc)
			key := catalog.DescriptorNameKey(proc)
			before.BindName(key, address)
			descriptors, names := before.DescriptorCount(), before.NameCount()
			stage := e.BeginCatalogStage(e.CatalogRead())
			parent := *ext
			e.mu.Lock()
			err := extension.StockUUIDOssp.Stage(&extensionHost{engine: e, stage: stage}, &parent, ext.Owner)
			e.mu.Unlock()
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != "42723" || e.catalog.state != before || before.DescriptorCount() != descriptors || before.NameCount() != names || before.Get(address) != proc || before.NameAddress(key) != address {
				t.Fatal("selected collision published partial installation", err)
			}
		})
	}
}
