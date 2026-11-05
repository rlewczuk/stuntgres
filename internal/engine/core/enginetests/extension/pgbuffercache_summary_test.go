package extension_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func summaryExpression(t *testing.T, e *core.Engine, kind functions.PgBuffercacheAggregateKind) plan.InstalledPgBuffercacheAggregateExpr {
 t.Helper()
 binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
 binder.Clause.AggregateClause = "FROM"
 binder.Query.Namespace = &analyze.QueryNamespace{}
 member := extension.PgBuffercacheAggregateMembers[int(kind)]
 n, err := binder.BindFunctionSource(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: member.Name}}}})
 if err != nil { t.Fatal(err) }
 return n.(plan.InstalledPgBuffercacheAggregateExpr)
}

func TestPgBuffercacheSummaryExpressionIdentity(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	foreign := engine.New()
	buffercacheInstalled(t, foreign)
	view := e.CatalogRead()
	for kind := functions.BuffercacheSummary; kind <= functions.BuffercacheUsageCounts; kind++ {
		n := summaryExpression(t, e, kind)
		if err := stored.ValidateInstalledPgBuffercacheAggregate(n, view); err != nil { t.Fatal(err) }
		if stored.ValidateInstalledPgBuffercacheAggregate(n, foreign.CatalogRead()) == nil { t.Fatal("foreign catalog accepted") }
		if exec.PlannedVolatility(n) != expr.Volatile || !n.Properties().StageDependent || plan.GenerationImmutable(n) || analyze.ClosedPgstattupleDefault(n, view) == kind.Set() { t.Fatal("lost invocation/closed scalar properties") }
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			value, err := rebuild(expr.InfoOf(n), n.Children())
			if err != nil { t.Fatal(err) }
			copy := value.(plan.InstalledPgBuffercacheAggregateExpr)
			if copy.Kind != n.Kind || copy.Ref != n.Ref || copy.Procedure != n.Procedure || !n.SameSourceOccurrence(copy) { t.Fatal("reconstruction lost immutable owned operands/authority") }
		}
		otherOperation := n
		otherOperation.Kind = functions.PgBuffercacheAggregateKind((int(kind)+1)%2)
		alwaysSame := func(expr.Expr, expr.Expr) bool { return true }
		if n.SameGroup(otherOperation, alwaysSame, false) || n.SameSourceOccurrence(otherOperation) || !n.SameGroup(n, alwaysSame, false) {
			t.Fatal("group/source identity discarded the sealed operation")
		}
		otherOccurrence := n
		otherOccurrence.Occurrence++
		if n.SameSourceOccurrence(otherOccurrence) {
			t.Fatal("distinct volatile occurrence matched the original source")
		}
		for _, mutate := range []func(*plan.InstalledPgBuffercacheAggregateExpr){
			func(n *plan.InstalledPgBuffercacheAggregateExpr) { n.Ref.Epoch++ },
			func(n *plan.InstalledPgBuffercacheAggregateExpr) { n.Ref.Epoch = 0 },
			func(n *plan.InstalledPgBuffercacheAggregateExpr) { n.RoutineReference = nil },
			func(n *plan.InstalledPgBuffercacheAggregateExpr) { n.Kind = 255 },
			func(n *plan.InstalledPgBuffercacheAggregateExpr) { p := *n.Procedure; p.Implementation = new(extension.PgBuffercacheProvider); n.Procedure, n.RoutineReference = &p, &p },
		} {
			bad := n
			mutate(&bad)
			if stored.ValidateInstalledPgBuffercacheAggregate(bad, view) == nil { t.Fatal("forged aggregate carrier accepted") }
		}
		members := make([]sqltype.CompositeMember, n.Usage.Type.CompositeLen())
		for i := range members { members[i] = n.Usage.Type.CompositeMember(i) }
		forged := *members[0].Usage.Type
		for i := range members {
			if members[i].Usage.Type.ID() == forged.ID() { members[i].Usage.Type = &forged }
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil { t.Fatal(err) }
		bad := n
		bad.Usage = shape.Usage()
		if stored.ValidateInstalledPgBuffercacheAggregate(bad, view) == nil { t.Fatal("nonresident OUT type generation accepted") }
		text, err := (stored.Deparser{View: view}).SQL(t.Context(), n, stored.DeparseParent{})
		if err != nil || !strings.Contains(text, n.Procedure.Name()) { t.Fatal("ordinary deparse", text, err) }
		var dependencies []ids.ObjectRef
		scalar, err := (stored.ScalarRenderer{Dependencies: &dependencies}).SQL(t.Context(), view, n)
		if kind.Set() {
			if err == nil { t.Fatal("SETOF inquiry entered scalar default closure") }
		} else if err != nil || !strings.Contains(scalar, n.Procedure.Name()) || !slices.Contains(dependencies, n.Ref) {
			t.Fatal("scalar renderer/dependencies", scalar, dependencies, err)
		}
		refs := stored.DependRefs{Context: context.Background(), Catalog: view}
		refs.Expression(n)
		if refs.Err != nil { t.Fatal(refs.Err) }
		if _, ok := refs.Addresses[n.Ref.Address]; !ok { t.Fatal("missing actual routine dependency") }
		retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || stored.ValidateInstalledPgBuffercacheAggregate(retagged.(plan.InstalledPgBuffercacheAggregateExpr), view) != nil { t.Fatal("retag lost resident carrier", err) }
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 920000}}
		for i, address := range ext.Members {
			if address.ClassID == ids.ProcedureClassID { mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(920001+i)} }
		}
		state := catalog.CloneState(view.State())
		descriptor := extension.DescriptorRemapper(mapping)
		for old := range mapping { state.UnbindName(catalog.DescriptorNameKey(view.Get(old))); state.RemoveDescriptor(old) }
		for old, address := range mapping { next := descriptor(view.Get(old)); state.SetDescriptor(address, next); state.BindName(catalog.DescriptorNameKey(next), address) }
		for _, address := range ext.Members {
			if _, mapped := mapping[address]; !mapped { next := descriptor(view.Get(address)); state.SetDescriptor(next.Address(), next) }
		}
		state.SetEpoch(state.Epoch()+1)
		remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = state.Epoch(); if address, ok := mapping[ref.Address]; ok { ref.Address = address }; return ref }
		mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(descriptor, nil, nil)}
		root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "result", Usage: n.Usage}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
		query, err := mapper.Query(root)
		if err != nil { t.Fatal(err) }
		mapped := query.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgBuffercacheAggregateExpr)
		if mapped.Ref != remap(n.Ref) || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) { t.Fatal("remap borrowed source ownership/authority") }
		if err := stored.ValidateInstalledPgBuffercacheAggregate(mapped, e.Catalog.Read(state)); err != nil { t.Fatal(err) }
	}
}