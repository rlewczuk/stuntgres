package core_test

import (
	"context"
	"encoding/binary"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"testing"
)

func ltreeLtxtqueryPreparedBindRecovery(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE EXTENSION ltree")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) error {
		t.Helper()
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartProtocolCommand(op, cycle)
		if err != nil {
			t.Fatal(err)
		}
		return scope.Finish(fn(scope))
	}
	syncCycle := func() {
		t.Helper()
		op, _, err := s.AcquireCommand(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		err = e.SyncProtocolCycle(op, cycle)
		op.Done()
		if err != nil {
			t.Fatal(err)
		}
		cycle = s.NewProtocolCycle()
	}
	if err := command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("lxq_params", "SELECT 'Europe.Russia'::ltree @ 'Europe & Russia'::ltxtquery AS retained,$1::ltxtquery @ 'Europe.Russia'::ltree AS dynamic", nil)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	input := func(value *string) *protocolUnitInput {
		data := []byte{0, 0, 0, 1}
		if value == nil {
			data = binary.BigEndian.AppendUint32(data, ^uint32(0))
		} else {
			data = binary.BigEndian.AppendUint32(data, uint32(len(*value)))
			data = append(data, (*value)...)
		}
		data = append(data, 0, 0)
		return &protocolUnitInput{data: data}
	}
	bind := func(name string, value *string) error {
		return command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "lxq_params", input(value))
			return err
		})
	}
	good := "Europe & Russia"
	if err := bind("lxq_params_good", &good); err != nil {
		t.Fatal(err)
	}
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("lxq_params")
	accepted := registry.Binding("lxq_params_good")
	if accepted == nil || accepted.Resource == nil || accepted.Resource.Root == nil || accepted.Folds == nil || len(accepted.Folds.Entries) == 0 {
		t.Fatal("actual accepted parameter graph/folds missing")
	}
	root, folds := accepted.Resource.Root, accepted.Folds
	expressions := append([]expr.Expr(nil), root.Expressions...)
	retained := append([]exec.FoldConstant(nil), folds.Entries...)
	generation := folds.Generation
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "ltree"}))
	procedure := view.MustProcedure(installation.Installation.Members[extension.LtreeLtxtqueryProcedureStart])
	found := false
	for _, entry := range retained {
		if entry.Callable == procedure {
			found = true
		}
	}
	if !found {
		t.Fatal("selected provider missing from actual accepted folds")
	}
	assertRetained := func() {
		t.Helper()
		if folds.Generation != generation || len(folds.Entries) != len(retained) || !reflect.DeepEqual(root.Expressions, expressions) {
			t.Fatal("Bind changed accepted graph or generation")
		}
		for i, entry := range retained {
			if !samePublishedFold(entry, folds.Entries[i]) || len(entry.Inputs) != len(folds.Entries[i].Inputs) {
				t.Fatal("Bind changed accepted fold")
			}
			if len(entry.Inputs) > 0 && &entry.Inputs[0] != &folds.Entries[i].Inputs[0] {
				t.Fatal("Bind replaced accepted operand backing")
			}
		}
	}
	assertRetained()
	syncCycle()
	assertRetained()
	bad := "foo@bar"
	err := bind("lxq_params_bad", &bad)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "modifiers syntax error" {
		t.Fatal("malformed Bind diagnostic", err)
	}
	if registry.Binding("lxq_params_bad") != nil {
		t.Fatal("failed Bind published candidate")
	}
	assertRetained()
	syncCycle()
	assertRetained()
	for _, value := range []*string{nil, &good} {
		if err := bind("lxq_params_recovered", value); err != nil {
			t.Fatal("Bind recovery", err)
		}
		next := registry.Binding("lxq_params_recovered")
		if next == nil || next.Resource == nil || next.Resource.Root == root {
			t.Fatal("recovery inherited mutable accepted graph")
		}
		assertRetained()
		syncCycle()
	}
	if registry.Lookup("lxq_params") != definition {
		t.Fatal("failed Bind replaced accepted definition")
	}
	assertRetained()
}
