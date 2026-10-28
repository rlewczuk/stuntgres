package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

// Inspect real accepted definition, fold and portal backing, independently of the
// public held-query/Reset/Restore observations in reference/locking_transactions.
func TestLegacySnapshotPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { preparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := preparedFoldPoint + ",'12:16:14,14'::txid_snapshot AS s,txid_current_snapshot() AS live"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("legacy_owned", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("legacy_owned")
	owner := definition.Description.Folds
	if owner == nil || !owner.Live() || owner.Current().Generation != 0 {
		t.Fatal("Parse lost definition owner")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "legacy_owned", &protocolUnitInput{data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("legacy_first")
	first := registry.Binding("legacy_first")
	accepted := owner.Current()
	entries := append([]exec.FoldConstant(nil), accepted.Entries...)
	if len(entries) == 0 || first == nil || first.Resource == nil || first.Resource.Root == nil {
		t.Fatal("missing actual accepted graph/folds")
	}
	originalRoot := first.Resource.Root
	analysis := definition.Description.Analysis
	firstSnapshot := func(binding *core.PreparedBinding) *sqltype.Snapshot {
		t.Helper()
		literal, ok := binding.Resource.Root.Expressions[1].(plan.LiteralExpr)
		if !ok || literal.Value.Type().ID() != sqltype.TxidSnapshot {
			t.Fatalf("prepared typed snapshot literal %T", binding.Resource.Root.Expressions[1])
		}
		payload, err := literal.Value.Snapshot()
		if err != nil || payload.String() != "12:16:14" {
			t.Fatal("snapshot literal payload", payload, err)
		}
		return payload
	}
	// Direct snapshot literals are ordinary typed binding products, not
	// definition-level fold entries (InputConstant decodes them per Bind).
	// Retain each actual accepted portal's payload before later operations;
	// immutable backing must survive within that portal, not alias a sibling's.
	payload := firstSnapshot(first)
	assertBinding := func(binding *core.PreparedBinding, expectedPayload *sqltype.Snapshot) {
		t.Helper()
		if binding == nil || binding.Resource == nil || binding.Resource.Root == nil || binding.Entry.Description.Folds != owner || binding.Folds == nil || binding.Folds.Generation != accepted.Generation || &binding.Folds.Entries[0] != &accepted.Entries[0] {
			t.Fatal("accepted owner/generation/backing changed")
		}
		if firstSnapshot(binding) != expectedPayload {
			t.Fatal("accepted portal's immutable literal backing replaced")
		}
		node := binding.Resource.Root.Expressions[2]
		declaration, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || declaration == nil || declaration.Address().ObjectID != 2944 || node.ResultUsage().Type.ID() != sqltype.TxidSnapshot || expr.InfoOf(node).RoutineReference == nil {
			t.Fatal("live legacy inquiry captured or relabeled", node, err)
		}
	}
	assertUnchanged := func() {
		t.Helper()
		current := owner.Current()
		if definition.Description.Folds != owner || definition.Description.Analysis != analysis || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current, accepted) {
			t.Fatal("definition/accepted fold backing changed")
		}
		for i, entry := range entries {
			if !samePublishedFold(entry, current.Entries[i]) {
				t.Fatal("immutable accepted fold changed")
			}
		}
		assertBinding(first, payload)
	}
	assertUnchanged()
	bind("legacy_second")
	second := registry.Binding("legacy_second")
	secondPayload := firstSnapshot(second)
	assertUnchanged()
	assertBinding(second, secondPayload)
	if second.Resource.Root == originalRoot {
		t.Fatal("portals shared mutable graph")
	}
	for _, name := range []string{"legacy_first", "legacy_second"} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		assertUnchanged()
		assertBinding(second, secondPayload)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var environment core.PreparedEnvironment
	if err == nil {
		environment, err = e.PreparedEnvironmentLocked(auth, true)
	}
	var publication *core.FoldPublication
	if err == nil {
		publication, err = core.BeginFoldPublication(&definition.Description, definition.Body, environment, auth.Catalog, nil)
	}
	if err == nil && publication != nil {
		publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(ctx, auth.Catalog, environment, e.Catalog.State.Epoch())
	}
	e.Mu.Unlock()
	op.Done()
	if publication == nil || !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("rejected publication admitted", err)
	}
	assertUnchanged()
	assertBinding(second, secondPayload)
	for _, name := range []string{"legacy_first", "legacy_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("legacy_owned") })
	op, _, err = s.AcquireCommand(context.Background())
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
