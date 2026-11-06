package extension_test

import (
	"context"
	"errors"
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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func prewarmInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	enginetest.Run(t, e, "CREATE EXTENSION pg_prewarm")
	view := e.CatalogRead()
	return view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_prewarm"}))
}
func prewarmExpression(t *testing.T, e *core.Engine) plan.InstalledPgPrewarmExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	node, err := binder.BindFunctionSource(enginetest.FunctionCall("pg_prewarm", enginetest.StringLiteral("-")))
	if err != nil { t.Fatal(err) }
	return node.(plan.InstalledPgPrewarmExpr)
}
func TestPgPrewarmProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := prewarmInstalled(t, e)
	view := e.CatalogRead()
	if len(ext.Members) != 1 { t.Fatal("selected recipe includes unselected helpers", ext.Members) }
	p := view.MustProcedure(ext.Members[0])
	if err := extension.StockPgPrewarm.ValidateExtension(view, ext); err != nil { t.Fatal(err) }
	if p.Signature != extension.PgPrewarmSignature || p.Result != 20 || p.Strict || p.Volatility != expr.Volatile || p.Installed.Defaults != 4 || p.Installed.ReturnsSet || p.Installed.Parallel != 's' || p.InputNames.Len() != 5 || p.InputNames.At(0) != "" || p.InputNames.At(4) != "last_block" || p.Display.Source != "pg_prewarm" || p.Display.Binary != "$libdir/pg_prewarm" { t.Fatal("released declaration", p) }
	if p.ACL.Explicit { t.Fatal("ordinary default acquired a script ACL") }
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = new(extension.PgPrewarmProvider) },
		func(p *catalog.Procedure) { p.Strict = true },
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Signature = "2205" },
		func(p *catalog.Procedure) { p.Display.Source = "forged" },
		func(p *catalog.Procedure) { p.Installed.Defaults = 3 },
		func(p *catalog.Procedure) { p.LiteralDefaults = nil },
		func(p *catalog.Procedure) { p.InputNames = catalog.NewRoutineInputNames("relation", "mode", "fork", "first_block", "last_block") },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1; p.Origin = &origin },
	} {
		state := catalog.CloneState(view.State())
		bad := *p
		mutate(&bad)
		state.SetDescriptor(p.Address(), &bad)
		if err := extension.StockPgPrewarm.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil { t.Fatal("forged release authority accepted", bad) }
	}
	other := engine.New()
	prewarmInstalled(t, other)
	if extension.ValidatePgPrewarmIdentity(other.CatalogRead(), p, view.Owner().Domain.Ref(view.Epoch(), p.Address())) == nil { t.Fatal("cross-domain declaration accepted") }
}
func TestPgPrewarmExpressionIdentity(t *testing.T) {
	e := engine.New()
	prewarmInstalled(t, e)
	view := e.CatalogRead()
	n := prewarmExpression(t, e)
	if n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || n.CallRoutineReference() != n.Procedure || exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) || exec.StrictScalarFunctionSource(n) { t.Fatal("lost volatile nonstrict invocation", n) }
	for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		result, err := rebuild(expr.InfoOf(n), n.Children())
		if err != nil { t.Fatal(err) }
		copy := result.(plan.InstalledPgPrewarmExpr)
		if copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.Slots != n.Slots || &copy.Args[0] == &n.Args[0] { t.Fatal("reconstruction lost owned children or origin") }
	}
	for _, mutate := range []func(*plan.InstalledPgPrewarmExpr){func(n *plan.InstalledPgPrewarmExpr) { n.Ref.Epoch++ }, func(n *plan.InstalledPgPrewarmExpr) { n.RoutineReference = nil }, func(n *plan.InstalledPgPrewarmExpr) { n.Slots[0] = 4 }} {
		bad := n
		mutate(&bad)
		if stored.ValidateInstalledPgPrewarm(bad, view) == nil { t.Fatal("forged expression accepted") }
	}
	if !analyze.ClosedPgstattupleDefault(n, view) { t.Fatal("authenticated scalar closure rejected") }
	text, err := (stored.Deparser{View: view}).SQL(t.Context(), n, stored.DeparseParent{})
	if err != nil || strings.Contains(text, "\"\" =>") || !strings.Contains(text, "pg_prewarm") { t.Fatal("current-name/formal rendering", text, err) }
	prewarmRemap(t, e, n)
	prewarmPreparedOwner(t, e)
	prewarmProtocolOwner(t, e)
}

func prewarmPreparedOwner(t *testing.T, e *core.Engine) {
	t.Helper()
	enginetest.Run(t, e, "CREATE TABLE prewarm_owned(n integer)")
	enginetest.Run(t, e, "INSERT INTO prewarm_owned VALUES(1)")
	s := enginetest.StartSession(t, e, nil)
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT pg_prewarm('prewarm_owned'::regclass,'buffer','main',COALESCE(-(7::integer),0)::bigint+7::bigint,0::bigint)")
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	owner := description.Folds
	accepted := owner.Current()
	if !owner.Live() || accepted.Generation == 0 || len(accepted.Entries) == 0 { t.Fatal("missing accepted immutable child owner") }
	entries := slices.Clone(accepted.Entries)
	for i := range entries { entries[i].Inputs = slices.Clone(entries[i].Inputs) }
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	if description.Folds != owner || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] { t.Fatal("unchanged execution replaced definition backing") }
	op, err := s.Acquire(t.Context())
	if err != nil { t.Fatal(err) }
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil { t.Fatal(err) }
	local := enginetest.CloneFoldDescription(t, description)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil { e.Mu.Unlock(); scope.Finish(err); t.Fatal(err) }
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil { e.Mu.Unlock(); scope.Finish(err); t.Fatal(err) }
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil { e.Mu.Unlock(); scope.Finish(err); t.Fatal(err) }
	publication.Group.Entries = slices.Clone(publication.Group.Entries)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || &owner.Current().Entries[0] != &accepted.Entries[0] || owner.Current().Generation != accepted.Generation { t.Fatal("rejected publication changed definition authority", err) }
	for i, want := range entries { if !enginetest.SamePublishedFold(owner.Current().Entries[i], want) { t.Fatal("rejected publication mutated immutable entry", i) } }
}

func prewarmProtocolOwner(t *testing.T, e *core.Engine) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := "SELECT pg_prewarm('prewarm_owned'::regclass,'buffer','main',COALESCE(-(7::integer),0)::bigint+7::bigint,0::bigint)"
	parse := func() { command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol("prewarm_definition", query, nil); return err }) }
	bind := func(name string) { command(func(scope *core.StatementScope) error { _, err := scope.BindProtocolPortal(name, "prewarm_definition", &enginetest.ProtocolReader{Data: []byte{0,0,0,0,0,0}}); return err }) }
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("prewarm_definition")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil { t.Fatal("missing genuine definition owner") }
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 { t.Fatal("Parse performed invocation/fold work") }
	bind("prewarm_first")
	first := registry.Binding("prewarm_first")
	accepted := owner.Current()
	if first == nil || first.Resource == nil || first.Folds == nil || len(accepted.Entries) == 0 || first.Entry.Description.Folds != owner || &first.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("binding lost accepted definition backing") }
	firstRoot := first.Resource.Root
	assertGraph := func(root *plan.ProjectNode) {
		t.Helper()
		found := 0
		var visit func(expr.Expr)
		visit = func(node expr.Expr) {
			if call, ok := node.(plan.InstalledPgPrewarmExpr); ok {
				found++
				if call.CallRoutineReference() != call.Procedure || call.Ref.Address != call.Procedure.Address() || call.Properties().Volatility != expr.Volatile || !call.Properties().StageDependent || call.ResultUsage().Type.ID() != sqltype.Int8 || len(call.Args) != 5 { t.Fatal("accepted graph lost symbolic invocation authority") }
				dependency := false
				for _, dep := range definition.Description.Analysis.Dependencies { dependency = dependency || dep.Ref == call.Ref && dep.Revision == call.Procedure.Revision() }
				if !dependency { t.Fatal("analysis omitted genuine executable dependency") }
				for _, entry := range accepted.Entries { if entry.Callable == call.Procedure { t.Fatal("volatile preload folded into retained count") } }
			}
			plan.ExpressionChildren(node, visit)
		}
		for _, node := range root.Expressions { visit(node) }
		if found != 1 { t.Fatal("lost preload occurrence", found) }
	}
	assertGraph(firstRoot)
	bind("prewarm_second")
	second := registry.Binding("prewarm_second")
	if second == nil || second == first || second.Entry.Description.Folds != owner || owner.Current().Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] { t.Fatal("rebind replaced accepted backing") }
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("prewarm_definition") })
	if owner.Live() { t.Fatal("retired definition kept publication authority") }
	parse()
	bind("prewarm_replacement")
	replacement := registry.Lookup("prewarm_definition").Description.Folds
	replacementAccepted := replacement.Current()
	if replacement == owner || replacementAccepted.Generation == 0 { t.Fatal("replacement reused retired owner") }
	command(func(scope *core.StatementScope) error { _, err := scope.ExecuteProtocolPortal("prewarm_first", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{}); return err })
	if owner.Live() || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] || &replacement.Current().Entries[0] != &replacementAccepted.Entries[0] { t.Fatal("old binding published into replacement") }
	assertGraph(firstRoot)
	for _, name := range []string{"prewarm_first", "prewarm_second", "prewarm_replacement"} { command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) }) }
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("prewarm_definition") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}

func prewarmRemap(t *testing.T, e *core.Engine, n plan.InstalledPgPrewarmExpr) {
	t.Helper()
	view := e.CatalogRead()
	ext := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_prewarm"}))
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 920000}, n.Procedure.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 920001}}
	state := catalog.CloneState(view.State())
	descriptor := extension.DescriptorRemapper(mapping)
	for old := range mapping { state.UnbindName(catalog.DescriptorNameKey(view.Get(old))); state.RemoveDescriptor(old) }
	for old, address := range mapping { next := descriptor(view.Get(old)); state.SetDescriptor(address, next); state.BindName(catalog.DescriptorNameKey(next), address) }
	state.SetEpoch(state.Epoch()+1)
	remap := func(ref ids.ObjectRef) ids.ObjectRef { ref.Epoch = state.Epoch(); if address, ok := mapping[ref.Address]; ok { ref.Address = address }; return ref }
	mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(descriptor, nil, nil)}
	query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "preload", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
	mappedQuery, err := mapper.Query(query)
	if err != nil { t.Fatal(err) }
	mapped := mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgPrewarmExpr)
	if mapped.Ref != remap(n.Ref) || mapped.CallRoutineReference() != mapped.Procedure || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || n.Procedure != view.Get(n.Ref.Address) || &mapped.Args[0] == &n.Args[0] { t.Fatal("destination remap borrowed predecessor authority") }
	if err := stored.ValidateInstalledPgPrewarm(mapped, e.Catalog.Read(state)); err != nil { t.Fatal(err) }
	retagged, _, err := stored.RetagTypes(e.Catalog.Read(state), mapped, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d })
	if err != nil { t.Fatal(err) }
	if err := stored.ValidateInstalledPgPrewarm(retagged.(plan.InstalledPgPrewarmExpr), e.Catalog.Read(state)); err != nil { t.Fatal(err) }
	if stored.ValidateInstalledPgPrewarm(n, e.Catalog.Read(state)) == nil { t.Fatal("predecessor expression admitted to remapped epoch") }
}
