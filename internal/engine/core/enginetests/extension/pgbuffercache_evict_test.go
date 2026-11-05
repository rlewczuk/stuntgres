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

func evictionExpression(t *testing.T, e *core.Engine, kind functions.PgBuffercacheEvictionKind) plan.InstalledPgBuffercacheEvictionExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	member := extension.PgBuffercacheEvictionMembers[int(kind)]
	var args []parser.Expr
	if kind == functions.EvictBuffer {
		args = []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}
	} else if kind == functions.EvictRelation {
		args = []parser.Expr{parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "eviction_identity_target"}}}}
	}
	n, err := binder.BindFunctionSource(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: member.Name}}}, Args: args})
	if err != nil { t.Fatal(err) }
	return n.(plan.InstalledPgBuffercacheEvictionExpr)
}

func TestPgBuffercacheEvictionProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	view := e.CatalogRead()
	if len(ext.Members) != 7 || len(ext.Installation.Members) != 4 { t.Fatal("recipe/ordinary cohort", ext) }
	for _, address := range ext.Installation.Members {
		p := view.MustProcedure(address)
		if p.Origin.Declaration == 0 { continue }
		member := extension.PgBuffercacheEvictionMembers[p.Origin.Declaration-1]
		if p.Signature != member.Signature || p.Result != 2249 || p.Strict != member.Strict || p.Volatility != expr.Volatile || p.Installed != member.Declaration || p.Installed.ReturnsSet || p.SecurityDefiner { t.Fatal("released eviction identity", p) }
		public := !p.ACL.Explicit
		for _, acl := range p.ACL.Entries { public = public || acl.Grantee.Public && acl.Privileges != 0 }
		if !public { t.Fatal("eviction incorrectly inherited pages PUBLIC revoke", p) }
		if view.State().InitialPrivilege(p.Address()) != nil { t.Fatal("invented script ACL history", p) }
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Result = 16 },
			func(p *catalog.Procedure) { p.Installed.ReturnsSet = true },
			func(p *catalog.Procedure) { p.Strict = !p.Strict },
			func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
			func(p *catalog.Procedure) { p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{16}, "fake") },
			func(p *catalog.Procedure) { p.Implementation = new(extension.PgBuffercacheProvider) },
			func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
		} {
			bad := *p
			mutate(&bad)
			state := catalog.CloneState(view.State())
			state.SetDescriptor(p.Address(), &bad)
			if extension.StockPgBuffercache.ValidateProcedure(e.Catalog.Read(state), &bad) == nil { t.Fatal("forged eviction declaration accepted") }
		}
	}
}

func TestPgBuffercacheEvictionExpressionIdentity(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	enginetest.Run(t, e, "CREATE TABLE eviction_identity_target(k integer)")
	foreign := engine.New()
	buffercacheInstalled(t, foreign)
	view := e.CatalogRead()
	for kind := functions.EvictBuffer; kind <= functions.EvictAll; kind++ {
		n := evictionExpression(t, e, kind)
		if err := stored.ValidateInstalledPgBuffercacheEviction(n, view); err != nil { t.Fatal(err) }
		if stored.ValidateInstalledPgBuffercacheEviction(n, foreign.CatalogRead()) == nil { t.Fatal("foreign catalog accepted") }
		if exec.PlannedVolatility(n) != expr.Volatile || !n.Properties().StageDependent || plan.GenerationImmutable(n) || !analyze.ClosedPgstattupleDefault(n, view) { t.Fatal("lost invocation/closed scalar properties") }
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			value, err := rebuild(expr.InfoOf(n), n.Children())
			if err != nil { t.Fatal(err) }
			copy := value.(plan.InstalledPgBuffercacheEvictionExpr)
			if copy.Kind != n.Kind || copy.Ref != n.Ref || copy.Procedure != n.Procedure || !n.SameSourceOccurrence(copy) || len(n.Args) != 0 && &copy.Args[0] == &n.Args[0] { t.Fatal("reconstruction lost immutable owned operands/authority") }
		}
		otherOperation := n
		otherOperation.Kind = functions.PgBuffercacheEvictionKind((int(kind)+1)%3)
		alwaysSame := func(expr.Expr, expr.Expr) bool { return true }
		if n.SameGroup(otherOperation, alwaysSame, false) || n.SameSourceOccurrence(otherOperation) || !n.SameGroup(n, alwaysSame, false) {
			t.Fatal("group/source identity discarded the sealed operation")
		}
		otherOccurrence := n
		otherOccurrence.Occurrence++
		if n.SameSourceOccurrence(otherOccurrence) {
			t.Fatal("distinct volatile occurrence matched the original source")
		}
		if len(n.Args) != 0 && n.SameGroup(n, func(expr.Expr, expr.Expr) bool { return false }, false) {
			t.Fatal("group matching ignored the actual operand comparison")
		}
		for _, mutate := range []func(*plan.InstalledPgBuffercacheEvictionExpr){
			func(n *plan.InstalledPgBuffercacheEvictionExpr) { n.Ref.Epoch++ },
			func(n *plan.InstalledPgBuffercacheEvictionExpr) { n.Ref.Epoch = 0 },
			func(n *plan.InstalledPgBuffercacheEvictionExpr) { n.RoutineReference = nil },
			func(n *plan.InstalledPgBuffercacheEvictionExpr) { n.Kind = 255 },
			func(n *plan.InstalledPgBuffercacheEvictionExpr) { p := *n.Procedure; p.Implementation = new(extension.PgBuffercacheProvider); n.Procedure, n.RoutineReference = &p, &p },
		} {
			bad := n
			mutate(&bad)
			if stored.ValidateInstalledPgBuffercacheEviction(bad, view) == nil { t.Fatal("forged eviction carrier accepted") }
		}
		members := make([]sqltype.CompositeMember, n.Usage.Type.CompositeLen())
		for i := range members { members[i] = n.Usage.Type.CompositeMember(i) }
		forged := *members[0].Usage.Type
		for i := range members {
			members[i].Usage.Type = &forged
		}
		shape, err := sqltype.RecordShape(members)
		if err != nil { t.Fatal(err) }
		bad := n
		bad.Usage = shape.Usage()
		if stored.ValidateInstalledPgBuffercacheEviction(bad, view) == nil { t.Fatal("nonresident OUT type generation accepted") }
		if len(n.Args) != 0 {
			bad = n
			bad.Args = slices.Clone(n.Args)
			bad.Args[0] = nil
			if stored.ValidateInstalledPgBuffercacheEviction(bad, view) == nil { t.Fatal("invalid typed child accepted") }
		}
		text, err := (stored.Deparser{View: view}).SQL(t.Context(), n, stored.DeparseParent{})
		if err != nil || !strings.Contains(text, n.Procedure.Name()) { t.Fatal("ordinary deparse", text, err) }
		var dependencies []ids.ObjectRef
		scalar, err := (stored.ScalarRenderer{Dependencies: &dependencies}).SQL(t.Context(), view, n)
		if err != nil || !strings.Contains(scalar, n.Procedure.Name()) || !slices.Contains(dependencies, n.Ref) { t.Fatal("scalar renderer/dependencies", scalar, dependencies, err) }
		refs := stored.DependRefs{Context: context.Background(), Catalog: view}
		refs.Expression(n)
		if refs.Err != nil { t.Fatal(refs.Err) }
		if _, ok := refs.Addresses[n.Ref.Address]; !ok { t.Fatal("missing actual routine dependency") }
		if kind == functions.EvictRelation {
			address := enginetest.CatalogRef(t, e, view, "eviction_identity_target").Address
			if _, ok := refs.Addresses[address]; !ok { t.Fatal("missing regclass operand dependency") }
		}
		retagged, _, err := stored.RetagTypes(view, n, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
		if err != nil || stored.ValidateInstalledPgBuffercacheEviction(retagged.(plan.InstalledPgBuffercacheEvictionExpr), view) != nil { t.Fatal("retag lost resident carrier", err) }
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
		mapped := query.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgBuffercacheEvictionExpr)
		if mapped.Ref != remap(n.Ref) || mapped.CallRoutineReference() != mapped.Procedure || n.Procedure != view.Get(n.Ref.Address) || len(n.Args) != 0 && &mapped.Args[0] == &n.Args[0] { t.Fatal("remap borrowed source ownership/authority") }
		if err := stored.ValidateInstalledPgBuffercacheEviction(mapped, e.Catalog.Read(state)); err != nil { t.Fatal(err) }
	}
	evictionPreparedOwner(t, e)
	evictionProtocolOwner(t, e)
}
