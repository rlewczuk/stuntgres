package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
	"testing"
)

func TestPageinspectHashProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	view := e.CatalogRead()
	names := []string{"hash_page_type", "hash_page_stats", "hash_page_items", "hash_metapage_info"}
	outs := [][]string{nil, {"live_items", "dead_items", "page_size", "free_size", "hasho_prevblkno", "hasho_nextblkno", "hasho_bucket", "hasho_flag", "hasho_page_id"}, {"itemoffset", "ctid", "data"}, {"magic", "version", "ntuples", "ffactor", "bsize", "bmsize", "bmshift", "maxbucket", "highmask", "lowmask", "ovflpoint", "firstfree", "nmaps", "procid", "spares", "mapp"}}
	types := [][]ids.OID{nil, {23, 23, 23, 23, 20, 20, 20, 23, 23}, {23, 27, 20}, {20, 20, 701, 23, 23, 23, 23, 20, 20, 20, 20, 20, 20, 26, 1016, 1016}}
	for i, name := range names {
		for _, named := range []bool{false, true} {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
			if named {
				raw.ArgNames = []parser.Identifier{{Text: "page"}}
			}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledPageinspectExpr)
			p := n.Procedure
			if n.Member != name || p.Display.Source != name || p.Signature != "17" || p.Origin.Declaration != 11+i || n.RoutineReference != p || n.Ref.Address != p.Address() || n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() {
				t.Fatal("hash selected identity", n)
			}
			if p.Origin.Installation.Declarations[11+i].Name() != name || n.Slots != [6]uint8{} || len(n.Args) != 1 || n.Args[0].ResultUsage().Type.ID() != sqltype.Bytea {
				t.Fatal("hash declaration slot/formal")
			}
			if i == 0 {
				if n.ResultUsage().Type.ID() != sqltype.Text || p.Result != 25 {
					t.Fatal("hash scalar type")
				}
			} else {
				d := n.ResultUsage().Type
				if p.Result != 2249 || d.CompositeLen() != len(outs[i]) {
					t.Fatal("hash OUT width")
				}
				for j, id := range types[i] {
					m := d.CompositeMember(j)
					want := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}).SQLType.Usage()
					if m.Attribute != int32(j+1) || m.Name != outs[i][j] || m.Usage != want {
						t.Fatal("hash OUT authority", j, m)
					}
				}
				labels := append([]string{"page"}, outs[i]...)
				forged := *p
				forged.Installed.Outputs = catalog.NewRoutineOutputs(types[i], labels...)
				state := catalog.CloneState(view.State())
				state.SetDescriptor(p.Address(), &forged)
				if _, err := extension.ProcedureSymbol(view.WithState(state), &forged); err == nil {
					t.Fatal("reconstructed OUT capability accepted")
				}
			}
			allowed, set := n.SourceAdmission()
			if !allowed || set != (i == 2) || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || !n.Properties().StageDependent {
				t.Fatal("hash execution contract")
			}
			if a, z := util.AllocationsPerCall(64, func() {
				if err := n.ValidateArguments(n.Args); err != nil {
					panic(err)
				}
			}); a != 0 || z != 0 {
				t.Fatal("hash argument allocation", a, z)
			}
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
				children := append([]expr.Expr(nil), n.Args...)
				next, err := rebuild(n.Info, children)
				if err != nil {
					t.Fatal(err)
				}
				copy := next.(plan.InstalledPageinspectExpr)
				children[0] = nil
				if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.Ref != n.Ref || copy.Procedure != p || copy.RoutineReference != p || copy.ResultUsage() != n.ResultUsage() {
					t.Fatal("hash rebuild ownership")
				}
				copy.Args[0] = nil
				if n.Args[0] == nil {
					t.Fatal("hash predecessor mutated")
				}
			}
			refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
			if err != nil || !slices.Contains(refs, p.Address()) {
				t.Fatal("hash stored dependency", refs, err)
			}
			retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			mapped, err := stored.RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil {
				t.Fatal(err)
			}
			got := mapped.(plan.InstalledPageinspectExpr)
			if got.Ref != n.Ref || got.Procedure != p || got.Member != name || got.ResultUsage() != n.ResultUsage() {
				t.Fatal("hash retag/remap")
			}
			if err := stored.ValidateInstalledPageinspect(got, view); err != nil {
				t.Fatal(err)
			}
			sql, err := (stored.Deparser{View: view}).SQL(context.Background(), got, stored.DeparseParent{})
			if err != nil || !strings.Contains(sql, name) || !strings.Contains(sql, "public") {
				t.Fatal("hash renderer", sql, err)
			}
			for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Signature = "25" }, func(p *catalog.Procedure) { p.Display.Source = "hash_bitmap_info" }, func(p *catalog.Procedure) { p.Result = 16 }, func(p *catalog.Procedure) { p.Installed.Parallel = 'r' }} {
				bad := *p
				mutate(&bad)
				state := catalog.CloneState(view.State())
				state.SetDescriptor(p.Address(), &bad)
				if _, err := extension.ProcedureSymbol(view.WithState(state), &bad); err == nil {
					t.Fatal("hash forged declaration accepted")
				}
			}
			bad := n
			bad.Member = "brin_page_type"
			if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
				t.Fatal("hash forged symbol accepted")
			}
			bad = n
			bad.Ref.Epoch++
			if err := extension.ValidatePageinspectIdentity(view, p, bad.Ref); err == nil {
				t.Fatal("hash stale identity accepted")
			}
		}
	}
}
