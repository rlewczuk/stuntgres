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

func TestPageinspectHeaderProviderIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	view := e.CatalogRead()
	for _, checksum := range []bool{false, true} {
		name, source, signature, slot, result := "page_header", "page_header", "17", 9, ids.OID(2249)
		if checksum {
			name, source, signature, slot, result = "page_checksum", "page_checksum_1_9", "17,20", 10, 21
		}
		for _, named := range []bool{false, true} {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
			slots := [6]uint8{}
			if checksum {
				raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "0"}})
				slots = [6]uint8{0, 1}
			}
			if named {
				raw.ArgNames = []parser.Identifier{{Text: "page"}}
				if checksum {
					raw.Args[0], raw.Args[1] = raw.Args[1], raw.Args[0]
					raw.ArgNames = []parser.Identifier{{Text: "blkno"}, {Text: "page"}}
					slots = [6]uint8{1, 0}
				}
			}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledPageinspectExpr)
			p := n.Procedure
			if n.Member != name || p.Display.Source != source || string(p.Signature) != signature || p.Result != result || p.Origin.Declaration != slot || n.RoutineReference != p || n.Ref.Address != p.Address() || n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() {
				t.Fatal("selected released identity", n)
			}
			if p.Origin.Installation.Declarations[slot].Name() != name {
				t.Fatal("immutable recipe slot", p.Origin)
			}
			if n.Slots != slots || len(n.Args) != len(raw.Args) {
				t.Fatal("written/formal argument mapping", n.Slots, slots)
			}
			for i, arg := range n.Args {
				want := sqltype.Bytea
				if n.Slots[i] == 1 {
					want = sqltype.Int8
				}
				if arg.ResultUsage().Type.ID() != want {
					t.Fatal("formal argument type", i, arg.ResultUsage())
				}
			}
			if checksum {
				if n.ResultUsage() != view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 21}).SQLType.Usage() {
					t.Fatal("checksum result descriptor")
				}
			} else {
				names := []string{"lsn", "checksum", "flags", "lower", "upper", "special", "pagesize", "version", "prune_xid"}
				types := []sqltype.ID{3220, 21, 21, 23, 23, 23, 23, 21, 28}
				d := n.ResultUsage().Type
				if d.CompositeLen() != 9 {
					t.Fatal("header OUT width")
				}
				for i, id := range types {
					m := d.CompositeMember(i)
					want := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).SQLType.Usage()
					if m.Attribute != int32(i+1) || m.Name != names[i] || m.Usage != want {
						t.Fatal("owned OUT descriptor", i, m, want)
					}
				}
				// Reconstructing a same-shape OUT capability is not the released declaration.
				bad := *p
				bad.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{3220, 21, 21, 23, 23, 23, 23, 21, 28}, "page", "lsn", "checksum", "flags", "lower", "upper", "special", "pagesize", "version", "prune_xid")
				state := catalog.CloneState(view.State())
				state.SetDescriptor(p.Address(), &bad)
				if _, err := extension.ProcedureSymbol(view.WithState(state), &bad); err == nil {
					t.Fatal("reconstructed OUT authority accepted")
				}
			}
			allowed, set := n.SourceAdmission()
			if !allowed || set || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || !n.Properties().StageDependent {
				t.Fatal("scalar execution contract")
			}
			if a, z := util.AllocationsPerCall(64, func() {
				if err := n.ValidateArguments(n.Args); err != nil {
					panic(err)
				}
			}); a != 0 || z != 0 {
				t.Fatal("argument validation allocated", a, z)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != len(n.Args) || !reflect.DeepEqual(capture.Args, n.Args) {
				t.Fatal("capture lost written operands", capture, err)
			}
			count := 0
			plan.ExpressionChildren(n, func(child expr.Expr) {
				if !reflect.DeepEqual(child, n.Args[count]) {
					t.Fatal("walker changed child order")
				}
				count++
			})
			if count != len(n.Args) {
				t.Fatal("walker omitted operand")
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
					t.Fatal("reconstruction lost immutable owner/backing")
				}
				copy.Args[0] = nil
				if n.Args[0] == nil {
					t.Fatal("reconstruction mutated predecessor")
				}
			}
			refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
			if err != nil || !slices.Contains(refs, n.Ref.Address) {
				t.Fatal("stored dependency", refs, err)
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
			if got.Ref != n.Ref || got.Procedure != p || got.Member != name || got.Slots != slots || got.ResultUsage() != n.ResultUsage() {
				t.Fatal("retag/remap lost selected owner")
			}
			if err := stored.ValidateInstalledPageinspect(got, view); err != nil {
				t.Fatal(err)
			}
			rendered, err := (stored.Deparser{View: view}).SQL(context.Background(), got, stored.DeparseParent{})
			if err != nil || !strings.Contains(rendered, name) || !strings.Contains(rendered, "public") {
				t.Fatal("stored renderer", rendered, err)
			}
			for _, mutate := range []func(*plan.InstalledPageinspectExpr){
				func(x *plan.InstalledPageinspectExpr) { x.Member = "brin_page_type" },
				func(x *plan.InstalledPageinspectExpr) { x.RoutineReference = nil },
				func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.SubID = 1 },
				func(x *plan.InstalledPageinspectExpr) {
					x.Usage = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType.Usage()
				},
			} {
				bad := n
				mutate(&bad)
				if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
					t.Fatal("forged carrier accepted")
				}
			}
			stale := n
			stale.Ref.Epoch++
			if err := extension.ValidatePageinspectIdentity(view, p, stale.Ref); err == nil {
				t.Fatal("foreign epoch accepted")
			}
			if err := stored.ValidateInstalledPageinspect(n, engine.New().CatalogRead()); err == nil {
				t.Fatal("foreign domain accepted")
			}
			for _, mutate := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Display.Source = "forged_source" }, func(p *catalog.Procedure) { p.Signature = "17,23" }, func(p *catalog.Procedure) { p.Installed.ArgumentNames[0] = "foreign" }} {
				bad := *p
				mutate(&bad)
				state := catalog.CloneState(view.State())
				state.SetDescriptor(p.Address(), &bad)
				if _, err := extension.ProcedureSymbol(view.WithState(state), &bad); err == nil {
					t.Fatal("forged declaration accepted")
				}
			}
			if checksum {
				invalid := n.Pageinspect
				invalid.Slots[1] = invalid.Slots[0]
				if err := invalid.ValidateArguments(invalid.Args); err == nil {
					t.Fatal("duplicate formal accepted")
				}
				wrong := append([]expr.Expr(nil), n.Args...)
				for i := range wrong {
					if slots[i] == 1 {
						wrong[i] = plan.LiteralExpr{Info: expr.Info{Usage: view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 23}).SQLType.Usage()}}
					}
				}
				if err := n.ValidateArguments(wrong); err == nil {
					t.Fatal("int4 accepted as retained int8 formal")
				}
			}
			if err := stored.ValidateInstalledPageinspect(n, view); err != nil {
				t.Fatal("rejection mutated original", err)
			}
		}
	}
}
