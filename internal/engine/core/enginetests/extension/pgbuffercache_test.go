package extension_test

import (
	"context"
	"reflect"
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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func buffercacheInstalled(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION pg_buffercache")
	return e.CatalogRead().MustExtension(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_buffercache"}))
}
func buffercacheExpression(t *testing.T, e *core.Engine, defined bool) plan.InstalledPgBuffercacheExpr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "FROM"
	binder.Query.Namespace = &analyze.QueryNamespace{}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_buffercache_pages"}}}}
	node, err := binder.BindFunctionSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if defined {
		definitions := make([]parser.FunctionColumnDefinition, 9)
		for i, typ := range []string{"integer", "oid", "oid", "oid", "smallint", "bigint", "boolean", "smallint", "integer"} {
			definitions[i] = parser.FunctionColumnDefinition{Name: parser.Identifier{Text: string(rune('a' + i))}, Type: parser.TypeName{Name: typ}}
		}
		node, err = binder.CompleteFunctionDefinitions(raw, node, definitions)
		if err != nil {
			t.Fatal(err)
		}
	}
	return node.(plan.InstalledPgBuffercacheExpr)
}

func TestPgBuffercacheProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	view := e.CatalogRead()
	control := extension.StockPgBuffercache.Control()
	if control.Version != "1.6" || !control.Superuser || control.Trusted || !control.Relocatable || len(ext.Members) != 9 || len(ext.Installation.Members) != 6 {
		t.Fatal("provider and ordinary consumer authority mixed", control, ext)
	}
	if err := extension.StockPgBuffercache.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	var procedure *catalog.Procedure
	var relation *catalog.Relation
	for _, address := range ext.Members {
		switch address.ClassID {
		case ids.ProcedureClassID:
			if candidate := view.MustProcedure(address); candidate.Name() == "pg_buffercache_pages" {
				procedure = candidate
			}
		case ids.RelationClassID:
			if candidate := view.MustRelation(address); candidate.Name() == "pg_buffercache" {
				relation = candidate
			}
		}
	}
	if procedure == nil || relation == nil {
		t.Fatal("missing class-qualified pages/view members", ext.Members)
	}
	if procedure.Result != 2249 || procedure.Signature != "" || procedure.OutputDeclaration() != nil || !procedure.Installed.ReturnsSet || procedure.Installed.Parallel != 's' || procedure.Strict || procedure.SecurityDefiner || procedure.Volatility != expr.Volatile {
		t.Fatal("released anonymous declaration", procedure)
	}
	if relation.RelationKind != catalog.ViewRelation || relation.VirtualView || relation.View == nil || relation.RowType == 0 {
		t.Fatal("view bypassed ordinary stored ownership", relation)
	}
	row := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(relation.RowType)})
	array := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if row.Address() == array.Address() || row.OwningRelation != relation.Address() || array.OwningRelation != relation.Address() || row.Extension != ext.Address() || array.Extension != ext.Address() || !slices.Contains(ext.Members, row.Address()) || !slices.Contains(ext.Members, array.Address()) {
		t.Fatal("ordinary reciprocal rowtype/array closure", row, array)
	}
	monitor := view.Owner().Principals.Principal(3373)
	for _, acl := range []authorization.ACLValue{procedure.ACL, relation.Security.ACL} {
		granted := false
		for _, entry := range acl.Entries {
			if entry.Grantee.Public && entry.Privileges != 0 {
				t.Fatal("PUBLIC script revoke missing", acl)
			}
			granted = granted || entry.Grantee.Role == monitor && entry.Privileges != 0
		}
		if !granted {
			t.Fatal("missing real pg_monitor ACL", acl)
		}
	}
	for _, address := range []ids.ObjectAddress{procedure.Address(), relation.Address()} {
		initial := view.State().InitialPrivilege(address)
		if initial == nil || !initial.ACL.Explicit {
			t.Fatal("missing genuine script history", address)
		}
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Implementation = new(extension.PgBuffercacheProvider) },
		func(p *catalog.Procedure) { p.Display.Source = "forged" },
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Signature = "23" },
		func(p *catalog.Procedure) { p.Strict = true },
		func(p *catalog.Procedure) { p.Installed.ReturnsSet = false },
		func(p *catalog.Procedure) { p.Installed.Outputs = catalog.NewRoutineOutputs([]ids.OID{23}, "fake") },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration++; p.Origin = &origin },
	} {
		bad := *procedure
		mutate(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(bad.Address(), &bad)
		if extension.StockPgBuffercache.ValidateProcedure(e.Catalog.Read(state), &bad) == nil {
			t.Fatal("forged declaration accepted")
		}
	}
	// Every existing consumer provider uses the same explicit policy carrier.
	views, err := extension.StockPgStatStatements.ConsumerViews("1.12")
	if err != nil || len(views) != 2 {
		t.Fatal(views, err)
	}
	for _, declaration := range views {
		if declaration.Statement == nil || len(declaration.ACL) != 1 || !declaration.ACL[0].Public || declaration.ACL[0].Privileges != authorization.SelectPrivilege || declaration.ACL[0].Revoke {
			t.Fatal("statistics PUBLIC policy changed", declaration)
		}
	}
}

func TestPgBuffercacheExpressionIdentity(t *testing.T) {
	e := engine.New()
	ext := buffercacheInstalled(t, e)
	foreign := engine.New()
	buffercacheInstalled(t, foreign)
	for _, defined := range []bool{false, true} {
		n := buffercacheExpression(t, e, defined)
		view := e.CatalogRead()
		if stored.ValidateInstalledPgBuffercache(n, view) != nil || stored.ValidateInstalledPgBuffercache(n, foreign.CatalogRead()) == nil {
			t.Fatal("foreign domain accepted or resident carrier rejected")
		}
		if n.Usage.Type.IsComposite() != defined || exec.PlannedVolatility(n) != expr.Volatile || plan.GenerationImmutable(n) {
			t.Fatal("record completion/fold identity", n)
		}
		for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
			copy, err := rebuild(n.Info, n.Children())
			if err != nil || !reflect.DeepEqual(copy, n) || !n.SameSourceOccurrence(copy) {
				t.Fatal("reconstruction lost immutable declaration", copy, err)
			}
		}
		for _, mutate := range []func(*plan.InstalledPgBuffercacheExpr){
			func(n *plan.InstalledPgBuffercacheExpr) { n.Ref.Epoch++ },
			func(n *plan.InstalledPgBuffercacheExpr) { n.Ref.Epoch = 0 },
			func(n *plan.InstalledPgBuffercacheExpr) { n.RoutineReference = nil },
			func(n *plan.InstalledPgBuffercacheExpr) {
				p := *n.Procedure
				p.Implementation = new(extension.PgBuffercacheProvider)
				n.Procedure, n.RoutineReference = &p, &p
			},
		} {
			bad := n
			mutate(&bad)
			if stored.ValidateInstalledPgBuffercache(bad, view) == nil {
				t.Fatal("forged carrier accepted")
			}
		}
		if defined {
			members := make([]sqltype.CompositeMember, n.Usage.Type.CompositeLen())
			for i := range members {
				members[i] = n.Usage.Type.CompositeMember(i)
			}
			forged := *members[5].Usage.Type
			// INT8 occurs once: the anonymous shape remains internally coherent,
			// but its field is not the catalog's resident type generation.
			members[5].Usage.Type = &forged
			shape, err := sqltype.RecordShape(members)
			if err != nil {
				t.Fatal(err)
			}
			bad := n
			bad.Usage = shape.Usage()
			if stored.ValidateInstalledPgBuffercache(bad, view) == nil {
				t.Fatal("forged record member generation accepted")
			}
		}
		text, err := (stored.Deparser{View: view}).SQL(context.Background(), n, stored.DeparseParent{})
		if err != nil || !strings.Contains(text, "pg_buffercache_pages") {
			t.Fatal("stored rendering", text, err)
		}
		mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 920000}, n.Ref.Address: {ClassID: ids.ProcedureClassID, ObjectID: 920001}}
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
		// Every other recipe procedure and the ordinary view/type closure
		// share the remapped extension cohort, regardless of member order.
		for _, address := range ext.Members {
			if _, mapped := mapping[address]; mapped {
				continue
			}
			member := descriptor(view.Get(address))
			state.SetDescriptor(member.Address(), member)
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
		root := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "cache", Usage: n.Usage}}, Child: &plan.ValuesNode{Rows: [][]expr.Expr{{}}}, Expressions: []expr.Expr{n}}
		mapped, err := mapper.Query(root)
		if err != nil {
			t.Fatal(err)
		}
		next := mapped.(*plan.ProjectNode).Expressions[0].(plan.InstalledPgBuffercacheExpr)
		if next.Ref != remap(n.Ref) || next.Procedure.Address() != mapping[n.Ref.Address] || next.RoutineReference != next.Procedure || n.Procedure != view.Get(n.Ref.Address) || stored.ValidateInstalledPgBuffercache(next, e.Catalog.Read(state)) != nil {
			t.Fatal("destination remap lost provenance")
		}
	}
	// Accepted immutable backing accompanies the distinguishing public held
	// statement scenarios; the volatile provider itself is never a fold entry.
	s := enginetest.StartSession(t, e, nil)
	ast, description := enginetest.PreparedDescription(t, e, s, "SELECT bufferid,COALESCE(-(7::integer),0) AS marker FROM pg_buffercache LIMIT 1")
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	owner := description.Folds
	accepted := owner.Current()
	if !owner.Live() || accepted.Generation == 0 || len(accepted.Entries) == 0 {
		t.Fatal("missing accepted definition owner")
	}
	snapshot := slices.Clone(accepted.Entries)
	enginetest.PreparedFoldExecute(t, e, s, ast, &description)
	if description.Folds != owner || owner.Current().Generation != accepted.Generation || &owner.Current().Entries[0] != &accepted.Entries[0] {
		t.Fatal("unchanged prepared execution replaced accepted backing")
	}
	for i := range snapshot {
		if !enginetest.SamePublishedFold(owner.Current().Entries[i], snapshot[i]) {
			t.Fatal("immutable accepted entry mutated")
		}
	}
}
