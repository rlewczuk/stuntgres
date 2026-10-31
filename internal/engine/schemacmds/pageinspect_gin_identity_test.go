package schemacmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Part of TestPageinspectBrinNamedValidation: extend the existing composed
// installed-provider proof rather than introducing a second setup convention.
func assertGinPageinspectIdentity(t *testing.T, e *core.Engine, b *analyze.Binder) {
	t.Helper()
	view := e.CatalogRead()
	for index, tc := range []struct {
		name  string
		ids   []sqltype.ID
		names []string
	}{
		{"gin_metapage_info", []sqltype.ID{20, 20, 23, 20, 20, 20, 20, 20, 20, 23}, []string{"pending_head", "pending_tail", "tail_free_size", "n_pending_pages", "n_pending_tuples", "n_total_pages", "n_entry_pages", "n_data_pages", "n_entries", "version"}},
		{"gin_page_opaque_info", []sqltype.ID{20, 23, 1009}, []string{"rightlink", "maxoff", "flags"}},
		{"gin_leafpage_items", []sqltype.ID{27, 21, 1010}, []string{"first_tid", "nbytes", "tids"}},
	} {
		for _, named := range []bool{false, true} {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "\\x"}}}}
			if named {
				raw.ArgNames = []parser.Identifier{{Text: "page"}}
			}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(err)
			}
			n := node.(plan.InstalledPageinspectExpr)
			if n.Member != tc.name || n.Procedure.Display.Source != tc.name || n.Procedure.Signature != "17" || n.RoutineReference != n.Procedure || n.Ref.Address != n.Procedure.Address() || n.Ref.Domain() != view.Owner().Domain || n.Ref.Epoch != view.Epoch() {
				t.Fatal("GIN selected authority", n)
			}
			if len(n.Args) != 1 || n.Slots != ([6]uint8{}) || n.Args[0].ResultUsage().Type.ID() != sqltype.Bytea {
				t.Fatal("GIN argument contract", n)
			}
			allowed, set := n.SourceAdmission()
			if !allowed || set != (index == 2) || n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || !n.Properties().StageDependent || n.Properties().Volatility != expr.Volatile {
				t.Fatal("GIN evaluation contract", n)
			}
			d := n.ResultUsage().Type
			if d.CompositeLen() != len(tc.ids) {
				t.Fatal("GIN record width", d.CompositeLen())
			}
			for i, id := range tc.ids {
				m := d.CompositeMember(i)
				expected := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}).SQLType.Usage()
				if m.Attribute != int32(i+1) || m.Name != tc.names[i] || m.Usage != expected {
					t.Fatal("GIN OUT field", i, m, expected)
				}
			}
			if allocations := testing.AllocsPerRun(100, func() {
				if err := n.ValidateArguments(n.Args); err != nil {
					panic(err)
				}
			}); allocations != 0 {
				t.Fatal("GIN argument validation allocated", allocations)
			}
			for _, children := range [][]expr.Expr{nil, {nil}, {n.Args[0], n.Args[0]}} {
				if err := n.ValidateArguments(children); err == nil {
					t.Fatal("invalid GIN operands accepted", children)
				}
			}
			invalid := n.Pageinspect
			invalid.Slots[0] = 1
			if err := invalid.ValidateArguments(invalid.Args); err == nil {
				t.Fatal("foreign formal accepted")
			}
			wrongType := n.Pageinspect
			wrongType.Usage = view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 16}).SQLType.Usage()
			if err := n.ValidateArguments([]expr.Expr{wrongType}); err == nil {
				t.Fatal("non-BYTEA operand accepted")
			}
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
				children := append([]expr.Expr(nil), n.Args...)
				rebuilt, err := rebuild(n.Info, children)
				if err != nil {
					t.Fatal(err)
				}
				copy := rebuilt.(plan.InstalledPageinspectExpr)
				children[0] = nil
				if copy.Args[0] != n.Args[0] || &copy.Args[0] == &n.Args[0] || copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.RoutineReference != n.RoutineReference || copy.Member != n.Member {
					t.Fatal("GIN rebuild changed immutable authority")
				}
				if err := stored.ValidateInstalledPageinspect(copy, view); err != nil {
					t.Fatal(err)
				}
				copy.Args[0] = nil
				if n.Args[0] == nil {
					t.Fatal("GIN children alias")
				}
			}
			stale := n
			stale.Ref.Epoch++
			if err := extension.ValidatePageinspectIdentity(view, stale.Procedure, stale.Ref); err == nil {
				t.Fatal("foreign GIN epoch accepted")
			}
			members := make([]sqltype.CompositeMember, len(tc.ids))
			for i := range members {
				members[i] = d.CompositeMember(i)
			}
			members[0].Name = "forged"
			shape, err := sqltype.RecordShape(members)
			if err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func(*plan.InstalledPageinspectExpr){
				func(x *plan.InstalledPageinspectExpr) { x.Member = "brin_page_type" },
				func(x *plan.InstalledPageinspectExpr) { x.Ref.Address.SubID = 1 },
				func(x *plan.InstalledPageinspectExpr) { x.RoutineReference = nil },
				func(x *plan.InstalledPageinspectExpr) { x.Usage = shape.Usage() },
			} {
				bad := n
				mutate(&bad)
				if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
					t.Fatal("forged GIN carrier accepted")
				}
			}
			if err := stored.ValidateInstalledPageinspect(n, engine.New().CatalogRead()); err == nil {
				t.Fatal("foreign GIN domain accepted")
			}
			if err := stored.ValidateInstalledPageinspect(n, view); err != nil {
				t.Fatal("rejection mutated GIN definition", err)
			}
		}
	}
}
