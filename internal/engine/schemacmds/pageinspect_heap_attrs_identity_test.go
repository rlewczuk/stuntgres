package schemacmds_test

import (
	"context"
	"strings"
	"testing"

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
)

func TestPageinspectHeapAttrsIdentity(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pageinspect")
	view := e.CatalogRead()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	for _, tc := range []struct {
		name           string
		slot           int
		labels, values []string
		result         sqltype.ID
	}{
		{"tuple_data_split", 17, []string{"rel_oid", "t_data", "t_infomask", "t_infomask2", "t_bits"}, []string{"0", "\\x", "0", "0", ""}, 1001},
		{"tuple_data_split", 18, []string{"rel_oid", "t_data", "t_infomask", "t_infomask2", "t_bits", "do_detoast"}, []string{"0", "\\x", "0", "0", "", "false"}, 1001},
		{"heap_page_item_attrs", 19, []string{"page", "rel_oid", "do_detoast"}, []string{"\\x", "0", "false"}, 2249},
		{"heap_page_item_attrs", 20, []string{"page", "rel_oid"}, []string{"\\x", "0"}, 2249},
	} {
		for _, named := range []bool{false, true} {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}}
			var slots [6]uint8
			for i := range tc.values {
				formal := i
				if named {
					formal = len(tc.values) - 1 - i
					raw.ArgNames = append(raw.ArgNames, parser.Identifier{Text: tc.labels[formal]})
				}
				raw.Args = append(raw.Args, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: tc.values[formal]}})
				slots[i] = uint8(formal)
			}
			node, err := b.BindFunctionProvider(raw)
			if err != nil {
				t.Fatal(tc.name, tc.slot, named, err)
			}
			n := node.(plan.InstalledPageinspectExpr)
			p := n.Procedure
			if p.Origin.Declaration != tc.slot || p.Result != ids.OID(tc.result) || p.Strict || p.Installed.Parallel != 'r' || n.Slots != slots || n.Member != tc.name || n.RoutineReference != p || n.PlanningFold() != expr.FoldNever || n.IntrinsicVolatility() != expr.Volatile {
				t.Fatal("selected declaration", n)
			}
			if tc.result == 2249 {
				if p.Display.Language != catalog.SQLRoutineLanguage || p.Display.Source != "" || p.Display.HasBinary || p.DeclarationBody == nil || p.Installed.Outputs.Len() != 14 || n.ResultUsage().Type.CompositeMember(13).Usage.Type.ID() != 1001 {
					t.Fatal("genuine wrapper declaration", p)
				}
			} else if p.InputNames.Len() != len(tc.values) || p.InputNames.At(len(tc.values)-1) != tc.labels[len(tc.values)-1] {
				t.Fatal("full split formal names", p)
			}
			if err := stored.ValidateInstalledPageinspect(n, view); err != nil {
				t.Fatal(err)
			}
			refs, err := stored.ExpressionDependRefs(context.Background(), view, n)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, ref := range refs {
				found = found || ref == n.Ref.Address
			}
			if !found {
				t.Fatal("installed dependency omitted", refs)
			}
			retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
			if err != nil {
				t.Fatal(err)
			}
			mapped, err := stored.RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil {
				t.Fatal(err)
			}
			selected := mapped.(plan.InstalledPageinspectExpr)
			if selected.Ref != n.Ref || selected.Procedure != p || selected.RoutineReference != p || selected.Slots != n.Slots || selected.ResultUsage() != n.ResultUsage() {
				t.Fatal("retag/remap changed selected authority")
			}
			rebuilt, err := n.Rebuild(n.Info, n.Args)
			if err != nil {
				t.Fatal(err)
			}
			copy := rebuilt.(plan.InstalledPageinspectExpr)
			if copy.Ref != n.Ref || copy.Procedure != p || copy.Slots != n.Slots || &copy.Args[0] == &n.Args[0] || copy.Args[0] != n.Args[0] {
				t.Fatal("rebuild ownership", copy)
			}
			copy.Args[0] = nil
			if n.Args[0] == nil {
				t.Fatal("rebuild leaked mutable operand slice")
			}
			rendered, err := (stored.Deparser{View: view}).SQL(context.Background(), n, stored.DeparseParent{})
			if err != nil || !strings.Contains(rendered, tc.name) {
				t.Fatal("stored call", rendered, err)
			}
			for _, label := range tc.labels {
				if !strings.Contains(rendered, label) {
					t.Fatal("stored call lost formal", label, rendered)
				}
			}
			for _, mutate := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Strict = true },
				func(p *catalog.Procedure) { p.Display.Language = catalog.InternalRoutineLanguage },
				func(p *catalog.Procedure) { p.Display.Source = "forged" },
				func(p *catalog.Procedure) {
					p.DeclarationBody = nil
					if tc.result != 2249 {
						p.Display.HasBinary = false
					}
				},
				func(p *catalog.Procedure) { p.InputNames = catalog.NewRoutineInputNames(tc.labels...) },
				func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
			} {
				bad := *p
				mutate(&bad)
				state := catalog.CloneState(view.State())
				state.SetDescriptor(p.Address(), &bad)
				if _, err := extension.ProcedureSymbol(view.WithState(state), &bad); err == nil {
					t.Fatal("forged attrs declaration admitted", tc.slot)
				}
			}
			bad := n
			bad.Slots[1] = bad.Slots[0]
			if err := stored.ValidateInstalledPageinspect(bad, view); err == nil {
				t.Fatal("duplicate slot admitted")
			}
			bad = n
			bad.Ref.Epoch++
			if err := extension.ValidatePageinspectIdentity(view, p, bad.Ref); err == nil {
				t.Fatal("foreign epoch admitted")
			}
			if err := stored.ValidateInstalledPageinspect(n, engine.New().CatalogRead()); err == nil {
				t.Fatal("foreign domain admitted")
			}
		}
	}
}
