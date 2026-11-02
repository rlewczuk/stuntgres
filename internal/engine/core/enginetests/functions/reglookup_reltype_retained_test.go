package functions_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqltype"
)

func TestRegLookupRelTypeStoredInputCapture(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, sql := range []string{
		"CREATE SCHEMA rl_capture",
		"CREATE DOMAIN rl_capture.rl_type AS varchar(12)",
		"CREATE ROLE rl_capture_reader",
		"SET search_path TO rl_capture,public",
		`CREATE TABLE public.rl_retained(m integer DEFAULT to_regtypemod('rl_type'),q integer DEFAULT to_regtypemod('rl_capture.rl_type'),v integer DEFAULT to_regtypemod('varchar(12)'))`,
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	view := e.CatalogRead()
	relation := view.MustRelation(enginetest.CatalogRef(t, e, view, "rl_retained").Address)
	revision := relation.Revision()
	var recipes []expr.Expr
	for attribute := int32(1); attribute <= 3; attribute++ {
		node := relation.ColumnMeta.Defaults[attribute].Expression
		lookup, ok := node.(functions.RegLookup)
		if !ok || lookup.Kind != functions.RegLookupTypemod || lookup.ResultUsage().Type.ID() != sqltype.Int4 || lookup.ValidateStoredProvider() != nil {
			t.Fatal("catalog did not retain the genuine INT4 declaration recipe", node)
		}
		provider, err := exec.ExpressionRoutineDescriptor(lookup)
		if err != nil || provider == nil || provider.Address().ObjectID != 6317 || provider != view.MustProcedure(provider.Address()) {
			t.Fatal("stored recipe lost its actual routine descriptor generation", err)
		}
		recipes = append(recipes, lookup)
	}
	var first regobj.TypeInput
	var firstPath []string
	for i, phase := range []struct {
		setup           []string
		missing, denied bool
	}{
		{},
		{setup: []string{"SET search_path TO public"}, missing: true},
		{setup: []string{"SET search_path TO rl_capture,public", "SET ROLE rl_capture_reader"}, missing: true, denied: true},
		{setup: []string{"RESET ROLE"}},
	} {
		for _, sql := range phase.setup {
			enginetest.RoleExecute(t, e, op, sql)
		}
		b := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
		// INT4 alone cannot demand catalog input. Reuse the actual stored
		// expression through the retained writer, not a replacement SQL bind.
		e.Mu.Lock()
		err := b.CaptureTypeInput(recipes[0].ResultUsage().Type)
		if err == nil && b.Input.Regtype != nil {
			e.Mu.Unlock()
			t.Fatal("INT4 acquired implicit catalog authority")
		}
		if err == nil {
			err = b.CaptureRetainedWriteInput(recipes, nil, nil)
		}
		auth, authErr := e.AuthorizationLocked(op)
		path, pathErr := core.LookupPath(auth, analyze.TypeLookup)
		e.Mu.Unlock()
		if err != nil || authErr != nil || pathErr != nil {
			t.Fatal("retained current-input capture", err, authErr, pathErr)
		}
		input, ok := b.Input.Regtype.(regobj.TypeInput)
		if !ok || !reflect.DeepEqual(input.Path(), path) || input.Authority().View.Catalog.State() != auth.Catalog.State() {
			t.Fatal("retained INT4 recipe did not borrow the current coherent catalog/path")
		}
		environment, ok := input.Authority().Environment.(core.AuthorizationView)
		if !ok || environment.State.CurrentUser != auth.State.CurrentUser {
			t.Fatal("retained input did not capture the current principal")
		}
		if i == 0 {
			first = input
			for _, address := range input.Path() {
				firstPath = append(firstPath, auth.Catalog.MustNamespace(address).Name())
			}
		}
		c := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: b.Input}}
		for index, recipe := range recipes {
			value, err := recipe.Eval(c)
			if index == 1 && phase.denied {
				enginetest.State(t, err, "42501")
				continue
			}
			if err != nil || value.Type() != recipe.ResultUsage().Type || value.IsNull() != (index == 0 && phase.missing) {
				t.Fatal("real retained destination outcome", i, index, value, err)
			}
			if !value.IsNull() {
				want := int64(-1)
				if index == 2 {
					want = 16
				}
				n, err := value.Integer()
				if err != nil || n != want {
					t.Fatal("retained encoded typmod", n, err)
				}
			}
		}
		current := e.CatalogRead().MustRelation(relation.Address())
		if current != relation || current.Revision() != revision {
			t.Fatal("input capture replaced the owning stored definition")
		}
		for index, recipe := range recipes {
			if !reflect.DeepEqual(current.ColumnMeta.Defaults[int32(index+1)].Expression, recipe) {
				t.Fatal("input capture mutated recipe/provider generation")
			}
		}
	}
	for i, address := range first.Path() {
		if first.Authority().View.Catalog.MustNamespace(address).Name() != firstPath[i] {
			t.Fatal("later captures mutated the prior immutable path")
		}
	}
}

func TestRegLookupRelTypePreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("rl_owner", `SELECT to_regclass('pg_catalog.pg_class'),to_regnamespace('pg_catalog'),to_regtype('integer'),to_regtypemod('varchar(12)'),-(7::integer)`, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("rl_owner")
	if definition == nil || definition.Description.Analysis == nil || definition.Description.Folds == nil {
		t.Fatal("missing actual prepared definition owner")
	}
	owner := definition.Description.Folds
	if owner.Current().Generation != 0 || len(owner.Current().Entries) != 0 {
		t.Fatal("Parse evaluated runtime recipes")
	}
	bind := func(name string) *core.PreparedBinding {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "rl_owner", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
		return registry.Binding(name)
	}
	first := bind("rl_first")
	accepted := owner.Current()
	if first == nil || first.Folds == nil || len(accepted.Entries) == 0 || first.Entry.Description.Folds != owner || first.Folds.Generation != accepted.Generation || &first.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("Bind lost accepted definition generation/backing")
	}
	retained := append([]exec.FoldConstant(nil), accepted.Entries...)
	assertGraph := func(binding *core.PreparedBinding) {
		t.Helper()
		if binding.Resource == nil || binding.Resource.Root == nil || len(binding.Resource.Root.Expressions) != 5 {
			t.Fatal("missing actual accepted private graph")
		}
		for i, oid := range []uint32{3495, 4086, 3493, 6317} {
			node, ok := binding.Resource.Root.Expressions[i].(functions.RegLookup)
			if !ok || node.ValidateStoredProvider() != nil || node.RoutineReference.RoutineOID() != oid || node.Properties().Volatility != expr.Stable || node.PlanningFold() != expr.FoldNever {
				t.Fatal("stable lookup was cached or lost routine provenance", i)
			}
			if node.ResultUsage().Type.ID() != []sqltype.ID{sqltype.Regclass, sqltype.Regnamespace, sqltype.Regtype, sqltype.Int4}[i] {
				t.Fatal("accepted lookup lost its exact result descriptor")
			}
		}
		literal, ok := binding.Resource.Root.Expressions[4].(plan.LiteralExpr)
		found := false
		for _, entry := range accepted.Entries {
			if ok && literal.Source == entry.Source && literal.Occurrence == entry.Occurrence && literal.ResultUsage() == entry.Result && literal.Value == entry.Value {
				found = true
			}
		}
		if !found {
			t.Fatal("control immutable fold lost genuine accepted provenance")
		}
	}
	assertGraph(first)
	root := first.Resource.Root
	second := bind("rl_second")
	if second == nil || second == first || second.Resource == first.Resource || second.Resource.Root == root || second.Entry.Description.Folds != owner || second.Folds == nil || second.Folds.Generation != accepted.Generation || &second.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("repeated Bind shared a mutable graph or replaced accepted immutable backing")
	}
	assertGraph(second)
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("rl_owner") })
	if owner.Live() || first.Retired || second.Retired || !reflect.DeepEqual(accepted.Entries, retained) {
		t.Fatal("definition retirement changed accepted binding artifacts")
	}
	assertGraph(first)
	assertGraph(second)
	for _, name := range []string{"rl_first", "rl_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	if !first.Retired || !second.Retired || !reflect.DeepEqual(accepted.Entries, retained) {
		t.Fatal("terminal binding cleanup changed saved immutable artifacts")
	}
	op, _, err := s.AcquireCommand(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	err = e.SyncProtocolCycle(op, cycle)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
}
