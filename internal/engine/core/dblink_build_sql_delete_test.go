package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func dblinkUnitInstall(t *testing.T, e *core.Engine) *catalog.Extension {
	t.Helper()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE EXTENSION dblink")
	view := e.CatalogRead()
	return view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
}

func TestDblinkBuildSQLDeleteProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := dblinkUnitInstall(t, e)
	view := e.CatalogRead()
	control := extension.StockDblink.Control()
	if control.Version != "1.2" || !control.Superuser || control.Trusted || !control.Relocatable || control.Comment == nil || *control.Comment != "connect to other PostgreSQL databases from within a database" {
		t.Fatal("stock control changed", control)
	}
	if len(ext.Members) != 13 || ext.Installation == nil || len(ext.Installation.Members) != 13 || len(ext.Installation.Declarations) != 13 || !ext.Installation.Valid() {
		t.Fatal("dblink must own thirteen sealed declaration slots", ext)
	}
	if err := extension.StockDblink.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	p := view.MustProcedure(ext.Members[0])
	if p.Name() != "dblink_build_sql_delete" || p.Signature != "25,22,23,1009" || p.Result != 25 || !p.Strict || p.Volatility != expr.Volatile || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'r'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "dblink_build_sql_delete", Binary: "$libdir/dblink", HasBinary: true}) || p.Origin.Declaration != 0 || p.Origin.Installation != ext.Installation {
		t.Fatal("released constructor declaration changed", p)
	}
	ref := e.Catalog.Domain.Ref(view.Epoch(), p.Address())
	if err := extension.ValidateDblinkIdentity(view, p, ref); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Strict = false },
		func(p *catalog.Procedure) { p.Volatility = expr.Immutable },
		func(p *catalog.Procedure) { p.Result = 23 },
		func(p *catalog.Procedure) { p.Signature = "25,22,23,1007" },
		func(p *catalog.Procedure) { p.Installed.Parallel = 's' },
		func(p *catalog.Procedure) { p.Installed.ReturnsSet = true },
		func(p *catalog.Procedure) { p.Display.Source = "dblink_build_sql_update" },
		func(p *catalog.Procedure) { p.Implementation = new(extension.DblinkProvider) },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 1; p.Origin = &origin },
		func(p *catalog.Procedure) {
			origin := *p.Origin
			recipe := *origin.Installation
			origin.Installation = &recipe
			p.Origin = &origin
		},
	} {
		state := catalog.CloneState(view.State())
		bad := *p
		mutate(&bad)
		state.SetDescriptor(p.Address(), &bad)
		if extension.StockDblink.ValidateProcedure(e.Catalog.Read(state), &bad) == nil {
			t.Fatal("forged constructor authority accepted", bad)
		}
	}
	stale := ref
	stale.Epoch++
	if extension.ValidateDblinkIdentity(view, p, stale) == nil {
		t.Fatal("stale epoch accepted")
	}
	foreign := engine.New()
	if extension.ValidateDblinkIdentity(view, p, foreign.Catalog.Domain.Ref(view.Epoch(), p.Address())) == nil {
		t.Fatal("foreign domain accepted")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 940000}, p.Address(): {ClassID: ids.ProcedureClassID, ObjectID: 940001}}
	remap := extension.DescriptorRemapper(mapping)
	mappedExt := remap(ext).(*catalog.Extension)
	mappedProc := remap(p).(*catalog.Procedure)
	if mappedExt.Installation == ext.Installation || !mappedExt.Installation.Valid() || mappedProc.Origin.Installation != mappedExt.Installation || !mappedProc.Origin.Valid(mapping[p.Address()]) || mappedExt.Installation.Declarations[0].Address() != mapping[p.Address()] || ext.Installation.Declarations[0].Address() != p.Address() {
		t.Fatal("remap split or mutated sealed three-slot recipe")
	}
}

func TestDblinkBuildSQLDeleteConstruction(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, `CREATE TABLE "D""T"(gone text,"K""id" text,k2 text)`)
	enginetest.Run(t, e, `ALTER TABLE "D""T" DROP COLUMN gone`)
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "D\"T"})
	target := view.MustRelation(address)
	if len(target.Columns) != 2 || target.Attributes[0] != 2 {
		t.Fatal("missing genuine dropped-column fixture")
	}
	vector, _ := view.LookupType(sqltype.Int2Vector)
	array, _ := view.LookupType(1009)
	integer, _ := view.LookupType(sqltype.Int4)
	input := func(d *sqltype.Descriptor, text string) sqltype.Value {
		t.Helper()
		value, err := d.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	// Retire every live catalog/storage object. The actual captured descriptor
	// and typed payload generations remain sufficient for construction.
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ keys, count, values, want string }{
		{"1 2", "2", `{"O'Brien",NULL}`, `DELETE FROM "D""T" WHERE "K""id" = 'O''Brien' AND k2 IS NULL`},
		{"1 999", "1", `{a}`, `DELETE FROM "D""T" WHERE "K""id" = 'a'`},
		{"2 1", "99", `[0:1]={b,a}`, `DELETE FROM "D""T" WHERE k2 = 'b' AND "K""id" = 'a'`},
		{"1 2 1 2", "4", `{{a,NULL},{c,d}}`, `DELETE FROM "D""T" WHERE "K""id" = 'a' AND k2 IS NULL AND "K""id" = 'c' AND k2 = 'd'`},
		{"1", "1", `{"a\\b'c"}`, `DELETE FROM "D""T" WHERE "K""id" = E'a\\b''c'`},
	} {
		got, err := contrib.DblinkDeleteSQL(context.Background(), target, "", false, input(vector, tc.keys), input(integer, tc.count), input(array, tc.values), sqltype.OutputEnvironment{})
		if err != nil || got != tc.want {
			t.Fatal("detached construction", got, tc.want, err)
		}
	}
	for _, tc := range []struct{ keys, count, values, code, message string }{
		{"", "1", "{}", "22023", "number of key attributes must be > 0"},
		{"3", "1", "{}", "22023", "invalid attribute number 3"},
		{"1 2", "2", "{a}", "2202E", "target key array length must match number of key attributes"},
	} {
		got, err := contrib.DblinkDeleteSQL(context.Background(), target, "", false, input(vector, tc.keys), input(integer, tc.count), input(array, tc.values), sqltype.OutputEnvironment{})
		if got != "" {
			t.Fatal("error published partial SQL", got)
		}
		enginetest.Diagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message})
	}
	keys, count, values := input(vector, "1 2"), input(integer, "2"), input(array, "{a,b}")
	got, err := contrib.DblinkDeleteSQL(context.Background(), target, "off_path", true, keys, count, values, sqltype.OutputEnvironment{QuoteAllIdentifiers: true})
	if err != nil || got != `DELETE FROM "off_path"."D""T" WHERE "K""id" = 'a' AND "k2" = 'b'` {
		t.Fatal(got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := contrib.DblinkDeleteSQL(ctx, target, "", false, keys, count, values, sqltype.OutputEnvironment{}); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled construction published", got, err)
	}
}

// The real protocol registry owns the recipe; neither Parse, Describe nor Bind
// may consult the relation named by a constant TEXT operand or retain SQL text.
func TestDblinkBuildSQLDeleteExpressionIdentity(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION dblink")
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const source = "SELECT dblink_build_sql_delete('not_present','1'::int2vector,1,'{a}'::text[]) AS sql"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("delete_owned", source, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("delete_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing real definition")
	}
	owner := definition.Description.Folds
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated constructor or lost owner")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("delete_owned")
		return err
	})
	analysis := definition.Description.Analysis
	view := e.CatalogRead()
	installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
	routine := view.MustProcedure(installed.Members[0])
	ref := e.Catalog.Domain.Ref(view.Epoch(), routine.Address())
	if definition.Description.CatalogOwner != view.Owner() || definition.Description.CatalogEpoch != view.Epoch() || definition.Description.PlanOwner != s.SessionToken || analysis.Domain != e.Catalog.Domain || analysis.Epoch != view.Epoch() {
		t.Fatal("definition lost actual owner/domain/generation")
	}
	found := false
	for _, dependency := range analysis.Dependencies {
		if dependency.Ref == ref && dependency.Revision == routine.Revision() {
			found = true
		}
	}
	if !found || analysis.FoldCallables[routine.Address()] != routine {
		t.Fatal("definition lost exact routine dependency")
	}
	bind := func(name string) *core.PreparedBinding {
		t.Helper()
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "delete_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Folds != owner || b.Entry.Description.Analysis != analysis {
			t.Fatal("missing actual accepted binding")
		}
		return b
	}
	first, second := bind("delete_first"), bind("delete_second")
	firstRoot, secondRoot := first.Resource.Root, second.Resource.Root
	firstFolds, secondFolds := *first.Folds, *second.Folds
	assert := func(b *core.PreparedBinding) plan.InstalledDblinkBuildSQLDeleteExpr {
		t.Helper()
		if len(b.Resource.Root.Expressions) != 1 {
			t.Fatal("missing recipe")
		}
		n, ok := b.Resource.Root.Expressions[0].(plan.InstalledDblinkBuildSQLDeleteExpr)
		if !ok || n.Procedure != routine || n.Ref != ref || n.CallRoutineReference() != routine || n.Source == (expr.FoldSourceOccurrence{}) || n.Occurrence == 0 || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
			t.Fatal("accepted graph lost genuine recipe", b.Resource.Root.Expressions[0])
		}
		ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009}
		for i, child := range n.Args {
			if child.ResultUsage().Type != analysis.FoldTypes[idsAddress(ids[i])] {
				t.Fatal("child lost definition type generation", i)
			}
		}
		return n
	}
	beforeFirst, beforeSecond := assert(first), assert(second)
	if first == second || first.Resource == second.Resource || firstRoot == secondRoot || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("bindings shared resources or folded volatile output")
	}
	// Reject an actual publication and retain the actual accepted graph and
	// immutable typed backing independently of live producer cleanup.
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer op.Done()
		e.Mu.Lock()
		defer e.Mu.Unlock()
		auth, err := e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		environment, err := e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		publication, err := core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
		if err != nil || publication == nil {
			t.Fatal("missing real publication", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := publication.Accept(ctx, auth.Catalog, environment, view.Epoch()); !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("canceled publication admitted", err)
		}
	}()
	if registry.Lookup("delete_owned") != definition || definition.Source != source || definition.Description.Analysis != analysis || definition.Description.Folds != owner || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot || firstFolds.Generation != first.Folds.Generation || secondFolds.Generation != second.Folds.Generation || len(owner.Current().Entries) != 0 {
		t.Fatal("rejected publication mutated retained ownership")
	}
	afterFirst, afterSecond := assert(first), assert(second)
	for _, pair := range [][2]plan.InstalledDblinkBuildSQLDeleteExpr{{beforeFirst, afterFirst}, {beforeSecond, afterSecond}} {
		if pair[0].Info != pair[1].Info {
			t.Fatal("accepted recipe provenance changed")
		}
		for i := range pair[0].Args {
			left, lok := pair[0].Args[i].(plan.LiteralExpr)
			right, rok := pair[1].Args[i].(plan.LiteralExpr)
			if lok != rok || lok && (!sqltype.SameDatum(left.Value, right.Value) || left.Info != right.Info) {
				t.Fatal("accepted immutable operand backing changed", i)
			}
		}
	}
}

func idsAddress(id sqltype.ID) ids.ObjectAddress {
	return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
}
