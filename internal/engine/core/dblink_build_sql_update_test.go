package core_test

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDblinkUpdateProviderIdentity(t *testing.T) {
	e := engine.New()
	ext := dblinkUnitInstall(t, e)
	view := e.CatalogRead()
	control := extension.StockDblink.Control()
	if control.Version != "1.2" || !control.Superuser || control.Trusted || !control.Relocatable || control.Comment == nil || *control.Comment != "connect to other PostgreSQL databases from within a database" {
		t.Fatal("stock control changed", control)
	}
	if len(ext.Members) != 6 || ext.Installation == nil || len(ext.Installation.Members) != 6 || len(ext.Installation.Declarations) != 6 || !ext.Installation.Valid() {
		t.Fatal("dblink must own six sealed declaration slots", ext)
	}
	if err := extension.StockDblink.ValidateExtension(view, ext); err != nil {
		t.Fatal(err)
	}
	for slot, name := range [...]string{functions.DblinkDeleteName, functions.DblinkInsertName, functions.DblinkUpdateName} {
		member := view.MustProcedure(ext.Members[slot])
		symbol, err := extension.ProcedureSymbol(view, member)
		if err != nil || symbol != name || member.Origin.Declaration != slot || member.Origin.Installation != ext.Installation || ext.Installation.Declarations[slot].Address() != member.Address() || member.Extension != ext.Address() {
			t.Fatal("append changed an incumbent sealed declaration", slot, member, symbol, err)
		}
		for previous := range slot {
			if ext.Members[previous] == member.Address() {
				t.Fatal("constructors share an allocated identity")
			}
		}
	}
	p := view.MustProcedure(ext.Members[2])
	if p.Name() != "dblink_build_sql_update" || p.Signature != "25,22,23,1009,1009" || p.Result != 25 || !p.Strict || p.Volatility != expr.Volatile || p.Installed != (catalog.InstalledRoutineDeclaration{Parallel: 'r'}) || p.Display != (catalog.RoutineDisplay{Language: catalog.CRoutineLanguage, Source: "dblink_build_sql_update", Binary: "$libdir/dblink", HasBinary: true}) || p.Origin.Declaration != 2 || p.Origin.Installation != ext.Installation {
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
		func(p *catalog.Procedure) { p.Display.Source = "dblink_build_sql_insert" },
		func(p *catalog.Procedure) { p.Implementation = new(extension.DblinkProvider) },
		func(p *catalog.Procedure) { p.Origin = nil },
		func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration = 0; p.Origin = &origin },
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
	foreignExt := dblinkUnitInstall(t, foreign)
	foreignView := foreign.CatalogRead()
	foreignMember := foreignView.MustProcedure(foreignExt.Members[2])
	foreignRef := foreign.Catalog.Domain.Ref(foreignView.Epoch(), foreignMember.Address())
	if foreignExt.Address() != ext.Address() || foreignMember.Address() != p.Address() || foreignRef.Epoch != ref.Epoch || foreignMember.Owner.ID() != p.Owner.ID() || foreignMember.Owner == p.Owner || foreignExt.Installation == ext.Installation {
		t.Fatal("independent installation fixture did not isolate matching numeric identities")
	}
	if err := extension.ValidateDblinkIdentity(foreignView, foreignMember, foreignRef); err != nil {
		t.Fatal("independent installation lost its own authority", err)
	}
	// A sealed recipe authenticates a logical declaration, not the caller's
	// catalog domain. Current descriptor, reference and principal admission own
	// that boundary even when both genuine installations allocated the same IDs.
	if extension.ValidateDblinkIdentity(view, foreignMember, ref) == nil {
		t.Fatal("independent descriptor borrowed local reference authority")
	}
	if extension.ValidateDblinkIdentity(foreignView, foreignMember, ref) == nil || extension.ValidateDblinkIdentity(view, p, foreignRef) == nil {
		t.Fatal("matching installed IDs admitted a foreign reference domain")
	}
	foreignState := catalog.CloneState(view.State())
	borrowed := *foreignMember
	foreignState.SetDescriptor(p.Address(), &borrowed)
	if extension.StockDblink.ValidateProcedure(e.Catalog.Read(foreignState), &borrowed) == nil {
		t.Fatal("imported installed descriptor borrowed local principal authority")
	}
	// Identical INSERT/UPDATE signatures are not interchangeable authority.
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	bound, err := binder.BindFunctionProvider(enginetest.FunctionCall(functions.DblinkUpdateName, enginetest.StringLiteral("absent"), enginetest.TypedLiteral("int2vector", "1"), enginetest.TypedLiteral("integer", "1"), enginetest.StringLiteral("{a}"), enginetest.StringLiteral("{b}")))
	if err != nil {
		t.Fatal(err)
	}
	call := bound.(plan.InstalledDblinkBuildSQLUpdateExpr).DblinkBuildSQLUpdate
	insert := view.MustProcedure(ext.Members[1])
	call.RoutineReference = insert
	if extension.ValidateDblinkBuildSQLUpdateCall(view, insert, call) == nil {
		t.Fatal("same-signature INSERT declaration authorized UPDATE")
	}
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address():  {ClassID: ids.ExtensionClassID, ObjectID: 940000},
		p.Address():    {ClassID: ids.ProcedureClassID, ObjectID: 940001},
		ext.Members[0]: {ClassID: ids.ProcedureClassID, ObjectID: 940002},
		ext.Members[1]: {ClassID: ids.ProcedureClassID, ObjectID: 940003},
	}
	remap := extension.DescriptorRemapper(mapping)
	mappedExt := remap(ext).(*catalog.Extension)
	mappedProc := remap(p).(*catalog.Procedure)
	if mappedExt.Installation == ext.Installation || !mappedExt.Installation.Valid() || mappedProc.Origin.Installation != mappedExt.Installation || !mappedProc.Origin.Valid(mapping[p.Address()]) || mappedExt.Installation.Declarations[2].Address() != mapping[p.Address()] || ext.Installation.Declarations[2].Address() != p.Address() {
		t.Fatal("remap split or mutated sealed recipe")
	}
	// This fixture relocates the three incumbent constructor addresses only.
	for slot, address := range ext.Members[:3] {
		member := remap(view.MustProcedure(address)).(*catalog.Procedure)
		if mappedExt.Members[slot] != mapping[address] || member.Extension != mapping[ext.Address()] || member.Origin.Installation != mappedExt.Installation || member.Origin.Declaration != slot || !member.Origin.Valid(member.Address()) || ext.Installation.Declarations[slot].Address() != address {
			t.Fatal("remapping broke sealed cohort or mutated source", slot)
		}
	}
}

func TestDblinkUpdateCarrierLifecycle(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION dblink")
	transaction := enginetest.TransactionBegin(t, s)
	defer enginetest.TransactionEnd(t, s, transaction, false)
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	const source = "SELECT dblink_build_sql_update('not_present','1'::int2vector,1,'{a}'::text[],'{b}'::text[]) AS sql,abs(-7) AS folded"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("update_owned", source, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("update_owned")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing real definition")
	}
	owner := definition.Description.Folds
	if !owner.Live() || owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated constructor or lost owner")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.DescribeProtocolStatement("update_owned")
		return err
	})
	analysis := definition.Description.Analysis
	view := e.CatalogRead()
	installed := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}))
	routine := view.MustProcedure(installed.Members[2])
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
			_, err := scope.BindProtocolPortal(name, "update_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		b := registry.Binding(name)
		if b == nil || b.Resource == nil || b.Resource.Root == nil || b.Folds == nil || b.Entry.Description.Folds != owner || b.Entry.Description.Analysis != analysis {
			t.Fatal("missing actual accepted binding")
		}
		return b
	}
	first, second := bind("update_first"), bind("update_second")
	firstResource, secondResource := first.Resource, second.Resource
	firstProducer, secondProducer := firstResource.Closer, secondResource.Closer
	firstRoot, secondRoot := firstResource.Root, secondResource.Root
	firstExpressions, secondExpressions := firstRoot.Expressions, secondRoot.Expressions
	firstFoldOwner, secondFoldOwner := first.Folds, second.Folds
	firstFolds, secondFolds := *first.Folds, *second.Folds
	// Capture actual accepted payloads while the definition is live. Closing
	// it retires publication rights, not the bindings' saved typed backing.
	accepted := owner.Current()
	retained := enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Int4, "7")
	assert := func(root *plan.ProjectNode) plan.InstalledDblinkBuildSQLUpdateExpr {
		t.Helper()
		if root == nil || len(root.Expressions) != 2 {
			t.Fatal("missing recipe")
		}
		n, ok := root.Expressions[0].(plan.InstalledDblinkBuildSQLUpdateExpr)
		if !ok || n.Procedure != routine || n.Ref != ref || n.CallRoutineReference() != routine || n.Source == (expr.FoldSourceOccurrence{}) || n.Occurrence == 0 || n.Properties().Volatility != expr.Volatile || !n.Properties().StageDependent {
			t.Fatal("accepted graph lost genuine recipe", root.Expressions[0])
		}
		ids := [...]sqltype.ID{sqltype.Text, sqltype.Int2Vector, sqltype.Int4, 1009, 1009}
		for i, child := range n.Args {
			if child.ResultUsage().Type != analysis.FoldTypes[idsAddress(ids[i])] {
				t.Fatal("child lost definition type generation", i)
			}
			info := expr.InfoOf(child)
			if info.Source == (expr.FoldSourceOccurrence{}) || info.Occurrence == 0 || info.Source == n.Source && info.Occurrence == n.Occurrence {
				t.Fatal("operand lost its distinct source expansion", i)
			}
			for _, previous := range n.Args[:i] {
				prior := expr.InfoOf(previous)
				if info.Source == prior.Source && info.Occurrence == prior.Occurrence {
					t.Fatal("lexical operands share one source expansion", i)
				}
			}
		}
		folded, ok := root.Expressions[1].(plan.LiteralExpr)
		if !ok || folded.Value.Type() == nil {
			t.Fatal("accepted graph lost real immutable sibling fold")
		}
		if !sqltype.SameDatum(folded.Value, retained.Value) || folded.Source != retained.Source || folded.Occurrence != retained.Occurrence || folded.ResultUsage() != retained.Result {
			t.Fatal("accepted graph stopped consuming actual fold backing/provenance")
		}
		return n
	}
	beforeFirst, beforeSecond := assert(firstResource.Root), assert(secondResource.Root)
	if first == second || firstResource == secondResource || firstProducer == nil || secondProducer == nil || firstProducer == secondProducer || firstRoot == secondRoot || &firstExpressions[0] == &secondExpressions[0] || firstFoldOwner == secondFoldOwner || first.Birth == 0 || first.Birth == second.Birth || first.Transaction != second.Transaction {
		t.Fatal("bindings shared accepted producer, graph or owner backing")
	}
	if len(accepted.Entries) == 0 || firstFolds.Generation != accepted.Generation || secondFolds.Generation != accepted.Generation || &firstFolds.Entries[0] != &accepted.Entries[0] || &secondFolds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("bindings lost shared immutable definition fold backing")
	}
	// Expansion ordinals are source-local binder stamps, not globally unique
	// portal identities. Distinct producers above may retain equal stamps.
	leftInfo, rightInfo := beforeFirst.Info, beforeSecond.Info
	leftInfo.Occurrence, rightInfo.Occurrence = 0, 0
	if leftInfo != rightInfo {
		t.Fatal("bindings changed original provider or logical provenance")
	}
	for i := range beforeFirst.Args {
		left, right := expr.InfoOf(beforeFirst.Args[i]), expr.InfoOf(beforeSecond.Args[i])
		if left.Occurrence == 0 || right.Occurrence == 0 || left.Source == (expr.FoldSourceOccurrence{}) || right.Source == (expr.FoldSourceOccurrence{}) {
			t.Fatal("operand lost actual capture provenance", i)
		}
		left.Occurrence, right.Occurrence = 0, 0
		if left != right {
			t.Fatal("binding changed operand semantic metadata", i)
		}
	}
	savedFolds := slices.Clone(accepted.Entries)
	for i := range savedFolds {
		savedFolds[i].Inputs = slices.Clone(savedFolds[i].Inputs)
	}
	savedInputs := make([][]exec.FoldInput, len(accepted.Entries))
	for i, entry := range accepted.Entries {
		savedInputs[i] = entry.Inputs
	}
	savedDependencies := slices.Clone(accepted.Dependencies)
	savedAnalysis := slices.Clone(analysis.Dependencies)
	savedTypes := maps.Clone(analysis.FoldTypes)
	savedCallables := maps.Clone(analysis.FoldCallables)
	assertFolds := func(actual exec.AcceptedFolds) {
		t.Helper()
		if actual.Generation != accepted.Generation || len(actual.Entries) != len(savedFolds) || &actual.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(actual.Dependencies, savedDependencies) {
			t.Fatal("rejected publication changed accepted fold backing")
		}
		for i, entry := range actual.Entries {
			if len(entry.Inputs) != len(savedInputs[i]) || len(entry.Inputs) != 0 && &entry.Inputs[0] != &savedInputs[i][0] {
				t.Fatal("accepted fold replaced immutable input backing", i)
			}
			if !enginetest.SamePublishedFold(entry, savedFolds[i]) {
				t.Fatal("accepted fold provenance/input/value changed", i)
			}
		}
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
		publication.Group.Entries = slices.Clone(publication.Group.Entries)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := publication.Accept(ctx, auth.Catalog, environment, view.Epoch()); !errors.Is(err, context.Canceled) || publication.Group.Sealed {
			t.Fatal("canceled publication admitted", err)
		}
	}()
	if registry.Lookup("update_owned") != definition || definition.Source != source || definition.Description.Analysis != analysis || definition.Description.Folds != owner || first.Resource != firstResource || second.Resource != secondResource || firstResource.Closer != firstProducer || secondResource.Closer != secondProducer || firstResource.Root != firstRoot || secondResource.Root != secondRoot || &firstRoot.Expressions[0] != &firstExpressions[0] || &secondRoot.Expressions[0] != &secondExpressions[0] || first.Folds != firstFoldOwner || second.Folds != secondFoldOwner || !reflect.DeepEqual(analysis.Dependencies, savedAnalysis) || !maps.Equal(analysis.FoldTypes, savedTypes) || !maps.Equal(analysis.FoldCallables, savedCallables) {
		t.Fatal("rejected publication mutated retained ownership")
	}
	assertFolds(owner.Current())
	assertFolds(*first.Folds)
	assertFolds(*second.Folds)
	if !enginetest.SamePublishedFold(retained, enginetest.PreparedFoldEntry(t, definition.Description, sqltype.Int4, "7")) {
		t.Fatal("rejected private replacement changed retained sibling datum")
	}
	assertUnchanged := func(before, after plan.InstalledDblinkBuildSQLUpdateExpr) {
		t.Helper()
		pair := [2]plan.InstalledDblinkBuildSQLUpdateExpr{before, after}
		if pair[0].Info != pair[1].Info {
			t.Fatal("accepted recipe provenance changed")
		}
		for i := range pair[0].Args {
			left, lok := pair[0].Args[i].(plan.LiteralExpr)
			right, rok := pair[1].Args[i].(plan.LiteralExpr)
			if !lok || !rok || !sqltype.SameDatum(left.Value, right.Value) || left.Info != right.Info {
				t.Fatal("accepted immutable operand backing changed", i)
			}
		}
	}
	assertUnchanged(beforeFirst, assert(firstResource.Root))
	assertUnchanged(beforeSecond, assert(secondResource.Root))
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("update_owned") })
	if registry.Lookup("update_owned") != nil || owner.Live() || owner.Current().Generation <= accepted.Generation || first.Retired || second.Retired || first.Folds != firstFoldOwner || second.Folds != secondFoldOwner || firstResource.Root != firstRoot || secondResource.Root != secondRoot {
		t.Fatal("definition retirement changed accepted portal lifetime")
	}
	assertFolds(*first.Folds)
	assertFolds(*second.Folds)
	assertUnchanged(beforeFirst, assert(firstResource.Root))
	assertUnchanged(beforeSecond, assert(secondResource.Root))
	for _, name := range []string{"update_first", "update_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !first.Retired || !second.Retired || first.Folds != nil || second.Folds != nil || firstResource.Root != nil || secondResource.Root != nil || registry.Binding("update_first") != nil || registry.Binding("update_second") != nil {
		t.Fatal("portal retirement leaked owned resources")
	}
	// Producer cleanup detaches its live graph/group, not objects retained
	// before retirement, including each independently accepted group holder.
	assertFolds(accepted)
	assertFolds(firstFolds)
	assertFolds(secondFolds)
	assertFolds(*firstFoldOwner)
	assertFolds(*secondFoldOwner)
	if &firstRoot.Expressions[0] != &firstExpressions[0] || &secondRoot.Expressions[0] != &secondExpressions[0] {
		t.Fatal("producer retirement replaced retained expression backing")
	}
	assertUnchanged(beforeFirst, assert(firstRoot))
	assertUnchanged(beforeSecond, assert(secondRoot))
}

// Failure at any inventory slot must leave the publication root untouched.
func TestDblinkUpdateProviderConflicts(t *testing.T) {
	for _, tc := range []struct{ name, signature string }{
		{"dblink_build_sql_delete", "(text,int2vector,integer,text[])"},
		{"dblink_build_sql_insert", "(text,int2vector,integer,text[],text[])"},
		{"dblink_build_sql_update", "(text,int2vector,integer,text[],text[])"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			enginetest.RoleExecute(t, e, op, "CREATE FUNCTION "+tc.name+tc.signature+" RETURNS text LANGUAGE SQL AS 'SELECT NULL::text'")
			before := e.Catalog.State
			view := e.CatalogRead()
			signature := functions.DblinkInsertSignature
			if tc.name == functions.DblinkDeleteName {
				signature = functions.DblinkDeleteSignature
			}
			// The conflict is an ordinary procedure, not an installed member.
			key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: tc.name, Signature: signature}
			address := view.NameAddress(key)
			original := view.MustProcedure(address)
			err := enginetest.ExtensionUnitExecute(t, e, op, enginetest.Statement(t, "CREATE EXTENSION dblink"))
			enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42723", Message: "function \"" + tc.name + "\" already exists with same argument types"})
			if e.Catalog.State != before || e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dblink"}) != (ids.ObjectAddress{}) {
				t.Fatal("failed install published staged declarations")
			}
			if e.CatalogRead().Get(address) != original || original.Origin != nil {
				t.Fatal("failed installation changed the pre-existing conflict")
			}
			for _, name := range []string{functions.DblinkDeleteName, functions.DblinkInsertName, functions.DblinkUpdateName} {
				candidate := key
				candidate.Name = name
				candidate.Signature = functions.DblinkInsertSignature
				if name == functions.DblinkDeleteName {
					candidate.Signature = functions.DblinkDeleteSignature
				}
				got := e.CatalogRead().NameAddress(candidate)
				if name != tc.name && got != (ids.ObjectAddress{}) {
					t.Fatal("failed installation published earlier staged member", name)
				}
			}
		})
	}
}
