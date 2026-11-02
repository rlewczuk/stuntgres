package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDblinkInsertProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := dblinkUnitInstall(t, e)
	view := e.CatalogRead()
	control := extension.StockDblink.Control()
	if control.Version != "1.2" || !control.Superuser || control.Trusted || !control.Relocatable || control.Comment == nil || *control.Comment != "connect to other PostgreSQL databases from within a database" {
		t.Fatal("stock control changed", control)
	}
	if len(ext.Members) != 3 || ext.Installation == nil || len(ext.Installation.Members) != 3 || len(ext.Installation.Declarations) != 3 || !ext.Installation.Valid() {
		t.Fatal("constructors must own exactly three sealed slots", ext)
	}
	if err := extension.StockDblink.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	p := view.MustProcedure(ext.Members[1])
	if p.Name() != "dblink_build_sql_insert" || p.Signature != "25,22,23,1009,1009" || p.Result != 25 || !p.Strict || p.Volatility != expr.Volatile || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'r'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "dblink_build_sql_insert", Binary: "$libdir/dblink", HasBinary: true}) || p.Origin.Declaration != 1 || p.Origin.Installation != ext.Installation {
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
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
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
	if mappedExt.Installation == ext.Installation || !mappedExt.Installation.Valid() || mappedProc.Origin.Installation != mappedExt.Installation || !mappedProc.Origin.Valid(mapping[p.Address()]) || mappedExt.Installation.Declarations[1].Address() != mapping[p.Address()] || ext.Installation.Declarations[1].Address() != p.Address() {
		t.Fatal("remap split or mutated sealed three-slot recipe")
	}
}

func TestDblinkInsertCarrierLifecycle(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION dblink")
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const source = "SELECT dblink_build_sql_insert('not_present','1'::int2vector,1,'{a}'::text[],'{b}'::text[]) AS sql"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("insert_owned", source, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("insert_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing real definition")
	}
	owner := definition.Description.Folds
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated constructor or lost owner")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("insert_owned")
		return err
	})
	analysis := definition.Description.Analysis
	view := e.CatalogRead()
	installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
	routine := view.MustProcedure(installed.Members[1])
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
			_, err := scope.BindProtocolPortal(name, "insert_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Folds != owner || b.Entry.Description.Analysis != analysis {
			t.Fatal("missing actual accepted binding")
		}
		return b
	}
	first, second := bind("insert_first"), bind("insert_second")
	firstRoot, secondRoot := first.Resource.Root, second.Resource.Root
	firstFolds, secondFolds := *first.Folds, *second.Folds
	assert := func(b *core.PreparedBinding) plan.InstalledDblinkBuildSQLInsertExpr {
		t.Helper()
		if len(b.Resource.Root.Expressions) != 1 {
			t.Fatal("missing recipe")
		}
		n, ok := b.Resource.Root.Expressions[0].(plan.InstalledDblinkBuildSQLInsertExpr)
		if !ok || n.Procedure != routine || n.Ref != ref || n.CallRoutineReference() != routine || n.Source == (expr.FoldSourceOccurrence{}) || n.Occurrence == 0 || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
			t.Fatal("accepted graph lost genuine recipe", b.Resource.Root.Expressions[0])
		}
		ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009, 1009}
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
	if registry.Lookup("insert_owned") != definition || definition.Source != source || definition.Description.Analysis != analysis || definition.Description.Folds != owner || first.Resource.Root != firstRoot || second.Resource.Root != secondRoot || firstFolds.Generation != first.Folds.Generation || secondFolds.Generation != second.Folds.Generation || len(owner.Current().Entries) != 0 {
		t.Fatal("rejected publication mutated retained ownership")
	}
	afterFirst, afterSecond := assert(first), assert(second)
	for _, pair := range [][2]plan.InstalledDblinkBuildSQLInsertExpr{{beforeFirst, afterFirst}, {beforeSecond, afterSecond}} {
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

// Failure at either inventory slot must leave the publication root untouched.
func TestDblinkInsertProviderConflicts(t *testing.T) {
	for _, tc := range []struct{ name, signature string }{
		{"dblink_build_sql_delete", "(text,int2vector,integer,text[])"},
		{"dblink_build_sql_insert", "(text,int2vector,integer,text[],text[])"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE FUNCTION "+tc.name+tc.signature+" RETURNS text LANGUAGE SQL AS 'SELECT NULL::text'")
			before := e.Catalog.State
			err := enginetest.ExtensionUnitExecute(t, e, op, enginetest.Statement(t, "CREATE EXTENSION dblink"))
			enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42723", Message: "function \"" + tc.name + "\" already exists with same argument types"})
			if e.Catalog.State != before || e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}) != (ids.ObjectAddress{}) {
				t.Fatal("failed install published staged declarations")
			}
		})
	}
}
