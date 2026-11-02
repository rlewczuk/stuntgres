package extension_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func freespaceInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pg_freespacemap")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_freespacemap"}))
}
func freespaceExpression(t *testing.T, e *core.Engine, name string, block bool) plan.InstalledPgFreespaceExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	args := []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "-"}}}
	if block {
		args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 0}})
	}
	node, err := binder.BindFunctionSource(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args})
	if err != nil {
		t.Fatal(err)
	}
	return node.(plan.InstalledPgFreespaceExpr)
}
func TestPgFreespaceProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := freespaceInstalled(t, e)
	view := e.CatalogRead()
	control := extension.StockPgFreespace.Control()
	if control.Version != "1.3" || !control.Superuser || control.Trusted || !control.Relocatable || len(ext.Members) != 2 {
		t.Fatal(control, ext)
	}
	if err := extension.StockPgFreespace.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	for i, address := range ext.Members {
		p := view.MustProcedure(address)
		signature := catalog.ProcedureSignature("2205,20")
		result := ids.OID(21)
		language := catalog.CRoutineLanguage
		if i == 1 {
			signature = "2205"
			result = 2249
			language = catalog.SQLRoutineLanguage
		}
		if p.Signature != signature || p.Result != result || p.Display.Language != language || p.Strict != (i == 0) || p.Volatility != expr.Volatile || p.Installed.Parallel != 's' || p.Installed.ReturnsSet != (i == 1) || (p.DeclarationBody != nil) != (i == 1) {
			t.Fatal("released declaration", i, p)
		}
		if i == 0 && (p.Display.Source != "pg_freespace" || p.Display.Binary != "$libdir/pg_freespacemap" || p.OutputDeclaration() != nil) {
			t.Fatal("scalar release identity", p)
		}
		if i == 1 && (p.OutputDeclaration().Name(0) != "rel" || p.OutputDeclaration().Name(1) != "blkno" || p.OutputDeclaration().Name(2) != "avail" || p.Display.Source != "" || p.Display.HasBinary) {
			t.Fatal("SQL release identity", p)
		}
		scan := view.Owner().Principals.Principal(authorization.StatScanTablesRoleID)
		granted := false
		for _, entry := range p.ACL.Entries {
			if entry.Grantee.Public && entry.Privileges&authorization.ExecutePrivilege != 0 {
				t.Fatal("PUBLIC authority")
			}
			granted = granted || entry.Grantee.Role == scan && entry.Privileges&authorization.ExecutePrivilege != 0
		}
		if !granted {
			t.Fatal("missing actual scan-role grant")
		}
		for _, mutate := range []func(*catalog.Procedure){
			func(p *catalog.Procedure) { p.Implementation = new(extension.PgFreespaceProvider) },
			func(p *catalog.Procedure) { p.Display.Source = "forged" },
			func(p *catalog.Procedure) { p.Strict = !p.Strict },
			func(p *catalog.Procedure) { p.Result = 23 },
			func(p *catalog.Procedure) { p.Signature = "23" },
			func(p *catalog.Procedure) { p.Installed.Parallel = 'u' },
			func(p *catalog.Procedure) { p.Installed.ReturnsSet = !p.Installed.ReturnsSet },
			func(p *catalog.Procedure) { p.DeclarationBody = view.MustProcedure(ext.Members[1-i]).DeclarationBody },
			func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1 - i; p.Origin = &origin },
		} {
			state := catalog.CloneState(view.State())
			bad := *p
			mutate(&bad)
			state.SetDescriptor(address, &bad)
			if err := extension.StockPgFreespace.ValidateProcedure(e.Catalog.Read(state), &bad); err == nil {
				t.Fatal("forged member accepted", i)
			}
		}
	}
	wrapper := view.MustProcedure(ext.Members[1])
	scalar, err := extension.PgFreespaceScalar(view, wrapper)
	if err != nil || scalar != view.MustProcedure(ext.Members[0]) {
		t.Fatal("body lost actual scalar identity", err)
	}
	var dependencies []ids.ObjectAddress
	extension.StockPgFreespace.VisitProcedureDependencies(view, wrapper, func(a ids.ObjectAddress) bool { dependencies = append(dependencies, a); return true })
	if len(dependencies) != 1 || dependencies[0] != scalar.Address() {
		t.Fatal("body dependency", dependencies)
	}
	for _, missing := range []bool{false, true} {
		state := catalog.CloneState(view.State())
		if missing {
			state.RemoveDescriptor(scalar.Address())
		} else {
			forged := *scalar
			forged.Implementation = nil
			state.SetDescriptor(scalar.Address(), &forged)
		}
		if _, err := extension.PgFreespaceScalar(e.Catalog.Read(state), wrapper); err == nil {
			t.Fatal("wrapper accepted missing/forged actual scalar slot", missing)
		}
	}
}

func TestPgFreespaceExpressionIdentity(t *testing.T) {
	e := engine.New()
	ext := freespaceInstalled(t, e)
	view := e.CatalogRead()
	for _, spec := range []struct {
		name  string
		block bool
	}{{"pg_freespace", true}, {"pg_freespace", false}} {
		n := freespaceExpression(t, e, spec.name, spec.block)
		if n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent || n.CallRoutineReference() != n.Procedure {
			t.Fatal("invocation properties", n)
		}
		for _, rewrite := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			value, err := rewrite(expr.InfoOf(n), n.Children())
			if err != nil {
				t.Fatal(err)
			}
			copy := value.(plan.InstalledPgFreespaceExpr)
			if copy.Ref != n.Ref || copy.Procedure != n.Procedure || copy.Kind != n.Kind || copy.Slots != n.Slots || &copy.Args[0] == &n.Args[0] {
				t.Fatal("reconstruction borrowed children or changed authority")
			}
		}
		if exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) {
			t.Fatal("volatile call became foldable")
		}
		for _, tc := range []struct {
			name   string
			mutate func(*plan.InstalledPgFreespaceExpr)
		}{
			{"routine_reference", func(n *plan.InstalledPgFreespaceExpr) { n.RoutineReference = nil }},
			{"future_epoch", func(n *plan.InstalledPgFreespaceExpr) { n.Ref.Epoch++ }},
			{"zero_epoch", func(n *plan.InstalledPgFreespaceExpr) { n.Ref.Epoch = 0 }},
			{"operation", func(n *plan.InstalledPgFreespaceExpr) { n.Kind = functions.PgFreespaceKind(255) }},
			{"formal_slot", func(n *plan.InstalledPgFreespaceExpr) { n.Slots[0] = 2 }},
		} {
			bad := n
			tc.mutate(&bad)
			if stored.ValidateInstalledPgFreespace(bad, view) == nil {
				t.Fatal("forged expression authority accepted", spec.name, spec.block, tc.name)
			}
		}
		if !n.Kind.Set() && !analyze.ClosedPgstattupleDefault(n, view) {
			t.Fatal("authenticated scalar closure rejected")
		}
		if n.Kind.Set() && analyze.ClosedPgstattupleDefault(n, view) {
			t.Fatal("set producer admitted to scalar default")
		}
		text, err := (stored.Deparser{View: view}).SQL(context.Background(), n, stored.DeparseParent{})
		if err != nil || strings.Contains(text, "\"\" =>") {
			t.Fatal("unnamed formal rendering", text, err)
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 920000}}
		for i, address := range ext.Members {
			mapping[address] = ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(920001 + i)}
		}
		state := catalog.CloneState(view.State())
		descriptor := extension.DescriptorRemapper(mapping)
		for old := range mapping {
			state.UnbindName(catalog.DescriptorNameKey(view.Get(old)))
			state.RemoveDescriptor(old)
		}
		for old, address := range mapping {
			next := descriptor(view.Get(old))
			state.SetDescriptor(address, next)
			state.BindName(catalog.DescriptorNameKey(next), address)
		}
		state.SetEpoch(state.Epoch() + 1)
		remap := func(ref ids.ObjectRef) ids.ObjectRef {
			ref.Epoch = state.Epoch()
			if address, ok := mapping[ref.Address]; ok {
				ref.Address = address
			}
			return ref
		}
		mapper := stored.ViewQueryRemapper{Catalog: e.Catalog.Read(state), Ref: remap, Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }, Procedure: extension.ProcedureRemapper(descriptor, nil, nil)}
		query := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "s", Usage: n.ResultUsage()}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
		mappedQuery, err := mapper.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		mapped := mappedQuery.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgFreespaceExpr)
		if mapped.Ref != remap(n.Ref) || mapped.CallRoutineReference() != mapped.Procedure || mapped.Procedure.Address() != mapping[n.Procedure.Address()] || n.Procedure != view.Get(n.Ref.Address) || &mapped.Args[0] == &n.Args[0] {
			t.Fatal("destination remap lost independent authority or mutated old backing")
		}
		if err := stored.ValidateInstalledPgFreespace(mapped, e.Catalog.Read(state)); err != nil {
			t.Fatal(err)
		}
	}
	freespacePreparedOwner(t, e)
	freespaceProtocolOwner(t, e)
}

func TestPgFreespaceExpressionStrictNull(t *testing.T) {
	e := engine.New()
	freespaceInstalled(t, e)
	for _, block := range []bool{true, false} {
		n := freespaceExpression(t, e, "pg_freespace", block)
		if exec.StrictScalarFunctionSource(n) != block {
			t.Fatal("scalar/set strictness lost")
		}
		children := append([]expr.Expr(nil), n.Args...)
		info := expr.InfoOf(children[0])
		children[0] = plan.LiteralExpr{Info: info, Value: sqltype.Null(info.Usage.Type)}
		node, err := n.Rebuild(expr.InfoOf(n), children)
		if err != nil {
			t.Fatal(err)
		}
		folded, changed, err := exec.FoldStrictParameterNull(&exec.Context{}, node)
		if err != nil || changed != block {
			t.Fatal("strict scalar/non-STRICT SQL boundary", block, changed, err)
		}
		if block && (!exec.RandomLiteralNull(folded) || folded.ResultUsage() != n.ResultUsage()) {
			t.Fatal("strict replacement lost INT2 usage")
		}
	}
}

func freespacePreparedOwner(t *testing.T, e *core.Engine) {
	t.Helper()
	enginetest.Run(t, e, "CREATE TABLE freespace_owned(n integer)")
	s := enginetest.StartSession(t, e, nil)
	// A demanded immutable child supplies distinguishing accepted fold backing;
	// the freespace invocation itself must remain volatile and symbolic.
	ast, d := enginetest.PreparedDescription(t, e, s, "SELECT pg_freespace('freespace_owned'::regclass,COALESCE(-(7::integer),0)::bigint+7::bigint)")
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	owner := d.Folds
	accepted := owner.Current()
	if !owner.Live() || accepted.Generation == 0 || len(accepted.Entries) == 0 {
		t.Fatal("missing real accepted definition generation")
	}
	snapshot := slices.Clone(accepted.Entries)
	for i := range snapshot {
		snapshot[i].Inputs = slices.Clone(snapshot[i].Inputs)
	}
	enginetest.PreparedFoldExecute(t, e, s, ast, &d)
	if d.Folds != owner || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("unchanged execution replaced accepted immutable backing")
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	local := enginetest.CloneFoldDescription(t, d)
	scope.Expected = &local
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	environment, err := e.PreparedEnvironmentLocked(auth, true)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	publication, err := core.BeginFoldPublication(&local, ast, environment, auth.Catalog, nil)
	if err != nil {
		e.Mu.Unlock()
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
	publication.Group.Private = true
	publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
	ctx, cancel := context.WithCancel(op.Ctx)
	cancel()
	err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	e.Mu.Unlock()
	scope.Finish(err)
	op.Done()
	if !errors.Is(err, context.Canceled) || publication.Group.Sealed || len(owner.Current().Entries) != len(snapshot) || len(local.Folds.Current().Entries) != len(snapshot) || local.Folds.Current().Generation != accepted.Generation || &local.Folds.Current().Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("rejected publication changed accepted owner/generation/backing", err)
	}
	for i, want := range snapshot {
		if !enginetest.SamePublishedFold(owner.Current().Entries[i], want) {
			t.Fatal("rejected publication mutated accepted immutable entry", i)
		}
	}
}

func freespaceProtocolOwner(t *testing.T, e *core.Engine) {
	t.Helper()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := "SELECT pg_freespace('freespace_owned'::regclass,COALESCE(-(7::integer),0)::bigint+7::bigint) AS flags"
	parse := func() {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ParseProtocol("freespace_definition", query, nil)
			return err
		})
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "freespace_definition", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	parse()
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("freespace_definition")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual protocol definition owner")
	}
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated producer or children")
	}
	bind("freespace_first")
	first := registry.Binding("freespace_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || first.Entry.Description.Folds != owner || len(accepted.Entries) == 0 || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("accepted binding lost definition generation/backing")
	}
	assertGraph := func(root *plan.ProjectNode) {
		t.Helper()
		if root == nil {
			t.Fatal("missing accepted private graph")
		}
		found := 0
		var visit func(expr.Expr)
		visit = func(node expr.Expr) {
			if call, ok := node.(plan.InstalledPgFreespaceExpr); ok {
				found++
				if call.Kind != functions.FreespaceBlock || call.CallRoutineReference() != call.Procedure || call.Ref.Address != call.Procedure.Address() || call.Properties().Volatility != expr.Volatile || !call.Properties().StageDependent || call.ResultUsage().Type.ID() != sqltype.Int2 {
					t.Fatal("accepted graph lost symbolic operation authority")
				}
				dependency := false
				for _, dep := range definition.Description.Analysis.Dependencies {
					dependency = dependency || dep.Ref == call.Ref && dep.Revision == call.Procedure.Revision()
				}
				if !dependency {
					t.Fatal("analysis omitted genuine executable dependency")
				}
				for _, entry := range accepted.Entries {
					if entry.Callable == call.Procedure {
						t.Fatal("volatile freespace producer was folded")
					}
				}
			}
			plan.ExpressionChildren(node, visit)
		}
		for _, expression := range root.Expressions {
			visit(expression)
		}
		if found != 1 {
			t.Fatal("private graph lost selected freespace occurrence", found)
		}
	}
	if first.Resource == nil {
		t.Fatal("accepted binding omitted query resource")
	}
	firstRoot := first.Resource.Root
	assertGraph(firstRoot)
	bind("freespace_second")
	second := registry.Binding("freespace_second")
	if second == nil || second == first || second.Entry.Description.Folds != owner || owner.Current().Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeated binding replaced accepted owner/backing")
	}
	assertGraph(firstRoot)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("freespace_definition") })
	if owner.Live() {
		t.Fatal("retired definition kept publication authority")
	}
	parse()
	bind("freespace_new")
	replacement := registry.Lookup("freespace_definition").Description.Folds
	replacementAccepted := replacement.Current()
	if replacement == owner || replacementAccepted.Generation == 0 || len(replacementAccepted.Entries) == 0 {
		t.Fatal("replacement reused retired definition authority")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("freespace_first", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	if replacement.Current().Generation != replacementAccepted.Generation || &replacement.Current().Entries[0] != &replacementAccepted.Entries[0] || owner.Live() || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("old accepted binding published into replacement or changed retained backing")
	}
	assertGraph(firstRoot)
	for _, name := range []string{"freespace_first", "freespace_second", "freespace_new"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("freespace_definition") })
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
