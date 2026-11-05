package core_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestBackendCurrentPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := preparedFoldPoint + ",pg_stat_get_backend_wal(pg_backend_pid()) AS live"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("backend_owned", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("backend_owned")
	owner := definition.Description.Folds
	if owner == nil || !owner.Live() || owner.Current().Generation != 0 {
		t.Fatal("Parse lost actual definition owner")
	}
	reader := reflect.ValueOf(core.PayloadOf(s)).Elem().FieldByName("statsReader")
	if !reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() || !reader.FieldByName("backends").IsNil() {
		t.Fatal("Parse sampled monitoring state")
	}
	bind := func(name string) {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(name, "backend_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
			return err
		})
	}
	bind("backend_first")
	first := registry.Binding("backend_first")
	accepted := owner.Current()
	entries := append([]exec.FoldConstant(nil), accepted.Entries...)
	if len(accepted.Entries) == 0 || first == nil || first.Resource == nil || first.Resource.Root == nil {
		t.Fatal("missing accepted graph/fold backing")
	}
	root := first.Resource.Root
	analysis := definition.Description.Analysis
	call, ok := root.Expressions[1].(functions.BackendCurrent)
	if !ok || call.Kind != functions.BackendWAL || call.RoutineReference == nil || call.RoutineReference.RoutineOID() != 6313 || call.Argument == nil {
		t.Fatal("live typed recipe was folded or relabeled", root.Expressions[1])
	}
	if !reader.FieldByName("activity").IsNil() || !reader.FieldByName("stamp").IsZero() {
		t.Fatal("Bind sampled backend facts")
	}
	saved := call
	assertUnchanged := func() {
		t.Helper()
		current := owner.Current()
		if definition.Description.Folds != owner || definition.Description.Analysis != analysis || current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current, accepted) || first.Resource.Root != root || !reflect.DeepEqual(root.Expressions[1], saved) {
			t.Fatal("accepted ownership/provenance/backing changed")
		}
		for i, entry := range entries {
			if !enginetest.SamePublishedFold(entry, current.Entries[i]) {
				t.Fatal("immutable accepted fold payload changed", i)
			}
		}
	}
	bind("backend_second")
	second := registry.Binding("backend_second")
	if second.Resource.Root == root || second.Entry.Description.Folds != owner || &second.Folds.Entries[0] != &accepted.Entries[0] {
		t.Fatal("portal graph versus immutable definition backing")
	}
	assertUnchanged()
	for _, name := range []string{"backend_first", "backend_second"} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.ExecuteProtocolPortal(name, 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
			return err
		})
		assertUnchanged()
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var env core.PreparedEnvironment
	if err == nil {
		env, err = e.PreparedEnvironmentLocked(auth, true)
	}
	var publication *core.FoldPublication
	if err == nil {
		publication, err = core.BeginFoldPublication(&definition.Description, definition.Body, env, auth.Catalog, nil)
	}
	if err == nil && publication != nil {
		publication.Group.Entries = append([]exec.FoldConstant(nil), publication.Group.Entries...)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(ctx, auth.Catalog, env, e.Catalog.State.Epoch())
	}
	e.Mu.Unlock()
	op.Done()
	if publication == nil || !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("failed publication admitted", err)
	}
	assertUnchanged()
	for _, name := range []string{"backend_first", "backend_second"} {
		command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal(name) })
	}
	command(func(scope *core.StatementScope) error { return scope.CloseProtocolStatement("backend_owned") })
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
