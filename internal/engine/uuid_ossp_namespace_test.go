package engine

import (
	"context"
	"slices"
	"strings"
	"testing"

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
			node, err := binder.bindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}})
			if err != nil {
				t.Fatal(err)
			}
			n := node.(installedUUIDOsspFunctionExpression)
			if n.Name != name || !slices.Contains(ext.Members, n.ref.Address) || n.procedure.Name() != name || n.ResultUsage().Type.ID() != sqltype.UUID || plannedVolatility(n) != expr.Immutable || !generationImmutable(n) || !closedUUIDDefault(n) {
				t.Fatal("constant shape/identity", n)
			}
			if carrier, err := ExpressionRoutineDescriptor(n); err != nil || carrier != n.procedure {
				t.Fatal("lost routine authority", carrier, err)
			}
			if err := n.validate(view); err != nil {
				t.Fatal(err)
			}
			if err := validateGenerationPlan(view, nil, n); err != nil {
				t.Fatal(err)
			}
			refs, err := pgDependExpressionRefs(context.Background(), view, n)
			if err != nil || !slices.Contains(refs, n.ref.Address) {
				t.Fatal("missing installed dependency", refs, err)
			}
			retagged, _, err := retagBoundTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, err := retagged.(installedUUIDOsspFunctionExpression).Rebuild(n.Info, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := rebuilt.(installedUUIDOsspFunctionExpression)
			if got.Name != name || got.procedure != n.procedure || got.ref != n.ref || plannedVolatility(got) != expr.Immutable {
				t.Fatal("rebuild/retag erased identity", got)
			}
			rendered, err := (scalarRenderer{}).sql(context.Background(), view, got)
			if err != nil || !strings.Contains(rendered, name) || !strings.Contains(rendered, "public") {
				t.Fatal("symbolic constant", rendered, err)
			}
			foreign := n
			foreign.ref.Epoch++
			if _, err := (scalarRenderer{}).sql(context.Background(), view, foreign); err == nil {
				t.Fatal("renderer accepted foreign-epoch constant")
			}
			grouped, err := binder.rewriteGroup(n, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := grouped.(installedUUIDOsspFunctionExpression).validate(view); err != nil {
				t.Fatal("grouped constant", err)
			}
			for _, mutate := range []func(*installedUUIDOsspFunctionExpression){
				func(x *installedUUIDOsspFunctionExpression) { x.Name = "uuidv4" },
				func(x *installedUUIDOsspFunctionExpression) { x.Property.Volatility = expr.Volatile },
				func(x *installedUUIDOsspFunctionExpression) { x.RoutineReference = nil },
				func(x *installedUUIDOsspFunctionExpression) { x.ref.Epoch = 0 },
				func(x *installedUUIDOsspFunctionExpression) { x.Usage.Type = nil },
				func(x *installedUUIDOsspFunctionExpression) { copy := *x.procedure; x.procedure = &copy },
			} {
				bad := n
				mutate(&bad)
				if err := bad.validate(view); err == nil {
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
			err := stockUUIDOssp.ValidateExtension(view, ext)
			view.State().SetDescriptor(address, p)
			if err == nil {
				t.Fatal("invalid selected member admitted", bad)
			}
		}
	}
	for _, members := range [][]ids.ObjectAddress{ext.Members[:5], append(slices.Clone(ext.Members), ext.Members[0]), append(slices.Clone(ext.Members[:5]), ext.Members[0])} {
		bad := *ext
		bad.Members = members
		if err := stockUUIDOssp.ValidateExtension(view, &bad); err == nil {
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
			err := stockUUIDOssp.stage(e, stage, &parent, ext.Owner, AuthorizationView{})
			e.mu.Unlock()
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != "42723" || e.catalog.state != before || before.DescriptorCount() != descriptors || before.NameCount() != names || before.Get(address) != proc || before.NameAddress(key) != address {
				t.Fatal("selected collision published partial installation", err)
			}
		})
	}
}
