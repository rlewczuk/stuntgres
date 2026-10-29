package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestConnectbyCompletionRebuildAndRemap(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tablefunc")
	enginetest.RoleExecute(t, e, op, "CREATE USER cb_remap_current")
	done()
	variants := []struct {
		args            string
		ordered, branch bool
		slot            int
	}{
		{"'missing','k','p','root',0", false, false, 2},
		{"'missing','k','p','root',0,'~'", false, true, 1},
		{"'missing','k','p','ord','root',0", true, false, 4},
		{"'missing','k','p','ord','root',0,'~'", true, true, 3},
	}
	for _, v := range variants {
		b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		raw := enginetest.Statement(t, "SELECT connectby("+v.args+")").(*parser.Select).Targets[0].(parser.FunctionExpr)
		bound, err := b.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(plan.InstalledConnectbyExpr)
		if n.Defined || n.Ordered != v.ordered || n.Branch != v.branch || n.Properties().Volatility != expr.Stable {
			t.Fatal("selected variant", n)
		}
		read := e.CatalogRead()
		ext := read.MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "tablefunc"}))
		if n.Procedure != read.MustProcedure(ext.Members[v.slot]) {
			t.Fatal("incorrect member authority", n.Ref, v.slot)
		}
		if _, err := b.CompleteFunctionDefinitions(raw, n, nil); err == nil {
			t.Fatal("missing record definitions admitted")
		}
		defs := []parser.FunctionColumnDefinition{
			{Name: parser.Identifier{Text: "Node Name"}, Type: parser.TypeName{Name: "text"}},
			{Name: parser.Identifier{Text: "parent"}, Type: parser.TypeName{Name: "text"}},
			{Name: parser.Identifier{Text: "level"}, Type: parser.TypeName{Name: "integer"}},
		}
		if v.branch {
			defs = append(defs, parser.FunctionColumnDefinition{Name: parser.Identifier{Text: "branch"}, Type: parser.TypeName{Name: "text"}})
		}
		if v.ordered {
			defs = append(defs, parser.FunctionColumnDefinition{Name: parser.Identifier{Text: "serial"}, Type: parser.TypeName{Name: "integer"}})
		}
		completed, err := b.CompleteFunctionDefinitions(raw, n, defs)
		if err != nil {
			t.Fatal(err)
		}
		n = completed.(plan.InstalledConnectbyExpr)
		if !n.Defined || n.Usage.Type.CompositeLen() != len(defs) || n.Usage.Type.CompositeMember(0).Name != "Node Name" {
			t.Fatal("lost caller shape")
		}
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			other, err := rebuild(n.Info, n.Children())
			if err != nil || !n.SameSourceOccurrence(other) {
				t.Fatal("reconstruction lost identity", err)
			}
			got := other.(plan.InstalledConnectbyExpr)
			if got.Procedure != n.Procedure || got.Ref != n.Ref || got.Usage != n.Usage || got.Occurrence != n.Occurrence || got.Source != n.Source {
				t.Fatal("reconstruction replaced authority")
			}
		}
		forged := n
		forged.Ordered = !n.Ordered
		if err := stored.ValidateInstalledConnectby(forged, read); err == nil {
			t.Fatal("forged variant accepted")
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 990000}}
		for slot, address := range ext.Members {
			// This fixture moves procedure identities, not nominal type codecs.
			// Keep type addresses and their reciprocal backing/array graph intact;
			// identity entries still remap each type's extension membership.
			destination := address
			if address.ClassID == ids.ProcedureClassID {
				destination.ObjectID = ids.OID(990001 + slot)
			}
			mapping[address] = destination
		}
		state := catalog.CloneState(read.State())
		for address := range mapping {
			state.UnbindName(catalog.DescriptorNameKey(read.Get(address)))
			state.RemoveDescriptor(address)
		}
		remapDescriptor := extension.DescriptorRemapper(mapping)
		for address, destination := range mapping {
			descriptor := remapDescriptor(read.Get(address))
			state.SetDescriptor(destination, descriptor)
			state.BindName(catalog.DescriptorNameKey(descriptor), destination)
		}
		// A current owner/revision COW must not replace the captured original.
		// Routine declarations remain executable-equivalent across this change.
		currentOwner, ok := e.Roles().Lookup("cb_remap_current")
		if !ok {
			t.Fatal("missing actual remap recipient")
		}
		current := *state.MustProcedure(mapping[n.Ref.Address])
		current.Owner = currentOwner.Ref
		current.ObjectRevision++
		state.SetDescriptor(current.Address(), &current)
		state.SetEpoch(state.Epoch() + 1)
		destination := e.Catalog.Read(state)
		if err := extension.StockTablefunc.ValidateExtension(destination, destination.MustExtension(mapping[ext.Address()])); err != nil {
			t.Fatal("remapped full provider graph", err)
		}
		remap := func(ref ids.ObjectRef) ids.ObjectRef {
			ref.Epoch = state.Epoch()
			if address, ok := mapping[ref.Address]; ok {
				ref.Address = address
			}
			return ref
		}
		mapper := stored.ViewQueryRemapper{Catalog: destination, Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(remapDescriptor, nil, nil)}
		query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "walk", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
		remapped, err := mapper.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		m := remapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledConnectbyExpr)
		if m.Ref != remap(n.Ref) || m.Procedure == n.Procedure || m.Procedure == destination.MustProcedure(m.Ref.Address) || !exec.SameRoutineDeclaration(m.Procedure, destination.MustProcedure(m.Ref.Address)) || m.Procedure.Owner != n.Procedure.Owner || m.Procedure.Revision() != n.Procedure.Revision() || n.Procedure != read.Get(n.Ref.Address) || !sqltype.SameRecordUsage(m.Usage, n.Usage) || m.RoutineReference != m.Procedure {
			t.Fatal("remap lost original declaration, destination authority, or caller record shape")
		}
		if &m.Args[0] == &n.Args[0] {
			t.Fatal("remap borrowed mutable operand storage")
		}
		text, err := (stored.Deparser{View: destination}).SQL(context.Background(), m, stored.DeparseParent{})
		if err != nil || !strings.Contains(text, "connectby") {
			t.Fatal("remapped rendering", text, err)
		}
		state.RemoveDescriptor(m.Ref.Address)
		if err := stored.ValidateInstalledConnectby(m, destination); err == nil {
			t.Fatal("removed member admitted")
		}
	}
}

func TestConnectbyStrictNullAndCanceledSource(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION tablefunc")
	done()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := enginetest.Statement(t, "SELECT connectby(NULL::text,'k','p','root',0)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	bound, err := b.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	// An intentionally wrong runtime descriptor remains legal at completion;
	// STRICT must produce empty rows without borrowing any statement host.
	completed, err := b.CompleteFunctionDefinitions(raw, bound, []parser.FunctionColumnDefinition{{Name: parser.Identifier{Text: "only"}, Type: parser.TypeName{Name: "text"}}})
	if err != nil {
		t.Fatal(err)
	}
	n := completed.(plan.InstalledConnectbyExpr)
	cursor, err := n.Connectby.OpenSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal("strict NULL demanded runtime/shape", err)
	}
	cursor.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = n.Connectby.OpenSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("ordinary source cancellation", err)
	}
}
