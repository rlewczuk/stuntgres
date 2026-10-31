package schemacmds_test

import (
	"context"
	"reflect"
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

func TestPageinspectHeapProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	view := e.CatalogRead()
	for _, flags := range []bool{false, true} {
		for _, named := range []bool{false, true} {
			name, signature, slot := "heap_page_items", "17", 15
			labels := []parser.Identifier{{Text: "page"}}
			args := []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}
			slots := [2]uint8{}
			names := []string{"lp", "lp_off", "lp_flags", "lp_len", "t_xmin", "t_xmax", "t_field3", "t_ctid", "t_infomask2", "t_infomask", "t_hoff", "t_bits", "t_oid", "t_data"}
			types := []ids.OID{21, 21, 21, 21, 28, 28, 23, 27, 23, 23, 21, 25, 26, 17}
			if flags {
				name, signature, slot = "heap_tuple_infomask_flags", "23,23", 16
				args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "80"}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "8192"}}}
				labels = []parser.Identifier{{Text: "t_infomask"}, {Text: "t_infomask2"}}
				slots = [2]uint8{0, 1}
				names = []string{"raw_flags", "combined_flags"}
				types = []ids.OID{1009, 1009}
			}
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
			if named {
				raw.ArgNames = labels
				if flags {
					raw.Args[0], raw.Args[1] = raw.Args[1], raw.Args[0]
					raw.ArgNames = []parser.Identifier{{Text: "t_infomask2"}, {Text: "t_infomask"}}
					slots = [2]uint8{1, 0}
				}
			}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledPageinspectExpr)
			p := n.Procedure
			if n.Member != name || p.Display.Source != name || string(p.Signature) != signature || p.Result != 2249 || p.Origin.Declaration != slot || p.Origin.Installation.Declarations[slot].Name() != name || n.RoutineReference != p || n.Ref.Address != p.Address() || n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() {
				t.Fatal("released selected identity", n)
			}
			if n.Slots != slots {
				t.Fatal("formal projection", n.Slots, slots)
			}
			for _, child := range n.Args {
				want := sqltype.Bytea
				if flags {
					want = sqltype.Int4
				}
				if child.ResultUsage().Type.ID() != want {
					t.Fatal("formal type", child.ResultUsage())
				}
			}
			d := n.ResultUsage().Type
			if d.CompositeLen() != len(types) {
				t.Fatal("OUT width")
			}
			for i, id := range types {
				m := d.CompositeMember(i)
				want := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}).SQLType.Usage()
				if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage != want {
					t.Fatal("OUT identity", i, m)
				}
			}
			allowed, set := n.SourceAdmission()
			if !allowed || set == flags || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || !n.Properties().StageDependent {
				t.Fatal("execution contract")
			}
			if a, z := util.AllocationsPerCall(64, func() {
				if err := n.ValidateArguments(n.Args); err != nil {
					panic(err)
				}
			}); a != 0 || z != 0 {
				t.Fatal("validation allocated", a, z)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != len(n.Args) || !reflect.DeepEqual(capture.Args, n.Args) {
				t.Fatal("written capture", capture, err)
			}
			count := 0
			plan.ExpressionChildren(n, func(child expr.Expr) {
				if !reflect.DeepEqual(child, n.Args[count]) {
					t.Fatal("written walker")
				}
				count++
			})
			if count != len(n.Args) {
				t.Fatal("walker omitted child")
			}
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
				borrowed := append([]expr.Expr(nil), n.Args...)
				rebuilt, err := rebuild(n.Info, borrowed)
				if err != nil {
					t.Fatal(err)
				}
				copy := rebuilt.(plan.InstalledPageinspectExpr)
				borrowed[0] = nil
				if copy.Args[0] == nil || &copy.Args[0] == &n.Args[0] || copy.Ref != n.Ref || copy.Procedure != p || copy.RoutineReference != p || copy.Member != name || copy.Slots != slots || copy.ResultUsage() != n.ResultUsage() {
					t.Fatal("immutable reconstruction")
				}
				copy.Args[0] = nil
				if n.Args[0] == nil {
					t.Fatal("predecessor mutation")
				}
			}
			refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
			if err != nil || !slices.Contains(refs, n.Ref.Address) {
				t.Fatal("dependency", refs, err)
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
			if got.Ref != n.Ref || got.Procedure != p || got.RoutineReference != p || got.Member != name || got.Slots != slots || got.ResultUsage() != n.ResultUsage() {
				t.Fatal("retag/remap identity")
			}
			if err := stored.ValidateInstalledPageinspect(got, view); err != nil {
				t.Fatal(err)
			}
			rendered, err := (stored.Deparser{View: view}).SQL(context.Background(), got, stored.DeparseParent{})
			if err != nil || !strings.Contains(rendered, name) || !strings.Contains(rendered, "public") {
				t.Fatal("render", rendered, err)
			}
			for _, mutate := range []func(*plan.InstalledPageinspectExpr){func(x *plan.InstalledPageinspectExpr) { x.Member = "brin_page_type" }, func(x *plan.InstalledPageinspectExpr) { x.RoutineReference = nil }, func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.SubID = 1 }, func(x *plan.InstalledPageinspectExpr) {
				x.Usage = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType.Usage()
			}} {
				bad := n
				mutate(&bad)
				if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
					t.Fatal("forged carrier accepted")
				}
			}
			stale := n
			stale.Ref.Epoch++
			if err := extension.ValidatePageinspectIdentity(view, p, stale.Ref); err == nil {
				t.Fatal("stale epoch accepted")
			}
			if err := stored.ValidateInstalledPageinspect(n, engine.New().CatalogRead()); err == nil {
				t.Fatal("foreign domain accepted")
			}
			for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Display.Source = "forged" }, func(p *catalog.Procedure) { p.Signature = "17,23" }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) {
				labels := []string{"page"}
				if flags {
					labels = []string{"t_infomask", "t_infomask2"}
				}
				p.Installed.Outputs = catalog.NewRoutineOutputs(types, append(labels, names...)...)
			}} {
				bad := *p
				mutate(&bad)
				state := catalog.CloneState(view.State())
				state.SetDescriptor(p.Address(), &bad)
				if _, err := extension.ProcedureSymbol(view.WithState(state), &bad); err == nil {
					t.Fatal("forged declaration accepted")
				}
			}
			invalid := n.Pageinspect
			invalid.Slots[0] = 2
			if err := invalid.ValidateArguments(invalid.Args); err == nil {
				t.Fatal("out-of-range formal accepted")
			}
			if flags {
				invalid = n.Pageinspect
				invalid.Slots[1] = invalid.Slots[0]
				if err := invalid.ValidateArguments(invalid.Args); err == nil {
					t.Fatal("duplicate formal accepted")
				}
				for i := range n.Args {
					wrong := append([]expr.Expr(nil), n.Args...)
					wrong[i] = plan.LiteralExpr{Info: expr.Info{Usage: view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 17}).SQLType.Usage()}}
					if err := n.ValidateArguments(wrong); err == nil {
						t.Fatal("bytea accepted as int4", i)
					}
				}
			}
			if err := stored.ValidateInstalledPageinspect(n, view); err != nil {
				t.Fatal("rejection mutated genuine artifact", err)
			}
		}
	}
}
