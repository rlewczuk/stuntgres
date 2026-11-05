package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// Public results are in reference/sessions; this observes actual retained definition,
// portal graph and fold owners, including rejected publication and failed Execute.
func TestBackupControlPreparedOwnership(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	query := preparedFoldPoint + ",pg_backup_stop($1::boolean) AS v"
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("backup_owned", query, nil)
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("backup_owned")
	owner := definition.Description.Folds
	analysis := definition.Description.Analysis
	manifest := slices.Clone(analysis.Dependencies)
	for _, tc := range []struct {
		name string
		data []byte
	}{{"backup_null", []byte{0, 0, 0, 1, 255, 255, 255, 255, 0, 0}}, {"backup_value", []byte{0, 0, 0, 1, 0, 0, 0, 1, 't', 0, 0}}} {
		command(func(scope *core.StatementScope) error {
			_, err := scope.BindProtocolPortal(tc.name, "backup_owned", &enginetest.ProtocolReader{Data: tc.data})
			return err
		})
	}
	a, b := registry.Binding("backup_null"), registry.Binding("backup_value")
	if a == nil || b == nil || a.Resource == b.Resource || a.Resource.Root == b.Resource.Root || a.Folds == nil || b.Folds == nil || len(a.Folds.Entries) == 0 || len(b.Folds.Entries) == 0 || &a.Folds.Entries[0] == &b.Folds.Entries[0] {
		t.Fatal("frames share graphs or immutable fold backing")
	}
	rootA, rootB := a.Resource.Root, b.Resource.Root
	callA, callB := rootA.Expressions[1], rootB.Expressions[1]
	for _, call := range []expr.Expr{callA, callB} {
		n, ok := call.(functions.BackupControl)
		if !ok || n.Kind != functions.BackupStop || n.Arity != 1 || n.Slots[0] != 0 || n.RoutineReference == nil {
			t.Fatal("lost immutable call recipe/provenance", call)
		}
		d, err := exec.ExpressionRoutineDescriptor(call)
		if err != nil || d == nil || d.Address().ObjectID != 2739 {
			t.Fatal("lost selected full declaration", d, err)
		}
	}
	foldsA, foldsB := a.Folds, b.Folds
	savedA, savedB := *foldsA, *foldsB
	entriesA, entriesB := slices.Clone(foldsA.Entries), slices.Clone(foldsB.Entries)
	// A separate constant definition makes the real definition publication owner
	// observable. Reject a private candidate at the final cancellation fence.
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("backup_constant", preparedFoldPoint+",pg_backup_stop(NULL::boolean)", nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("backup_constant_portal", "backup_constant", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	constant := registry.Lookup("backup_constant")
	constantOwner := constant.Description.Folds
	accepted := constantOwner.Current()
	constantBinding := registry.Binding("backup_constant_portal")
	if constantBinding == nil || constantBinding.Resource == nil || constantBinding.Resource.Root == nil {
		t.Fatal("missing constant portal graph")
	}
	drop, ok := constantBinding.Resource.Root.Expressions[1].(functions.BackupControl)
	if !ok || drop.Kind != functions.BackupStop || drop.ResultUsage().Type.ID() != sqltype.ID(2249) || drop.RoutineReference == nil {
		t.Fatal("strict NULL erased RECORD call admission")
	}
	descriptor, err := exec.ExpressionRoutineDescriptor(drop)
	if err != nil || descriptor == nil || descriptor.Address().ObjectID != 2739 {
		t.Fatal("strict NULL lost selected drop declaration", descriptor, err)
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
		publication, err = core.BeginFoldPublication(&constant.Description, constant.Body, environment, auth.Catalog, nil)
	}
	if err == nil && publication != nil {
		publication.Group.Entries = slices.Clone(publication.Group.Entries)
		publication.Group.Private = true
		publication.Group.Entries[0].Value = sqltype.Null(publication.Group.Entries[0].Result.Type)
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		err = publication.Accept(canceled, auth.Catalog, environment, e.Catalog.State.Epoch())
	}
	e.Mu.Unlock()
	op.Done()
	if publication == nil || !errors.Is(err, context.Canceled) || publication.Group.Sealed {
		t.Fatal("rejected publication", err)
	}
	current := constantOwner.Current()
	if current.Generation != accepted.Generation || &current.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(current, accepted) {
		t.Fatal("rejected candidate replaced accepted owner")
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("backup_null", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	err = enginetest.PreparedFoldProtocolTry(t, e, s, cycle, false, func(scope *core.StatementScope) error {
		_, err := scope.ExecuteProtocolPortal("backup_value", 0, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		return err
	})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "pg_backup_stop physical online backup lifecycle is unsupported in embedded mode" {
		t.Fatal("demanded boundary", err)
	}
	if a.Resource.Root != rootA || a.Folds != foldsA || b.Resource.Root != nil || b.Folds != nil {
		t.Fatal("live frame cleanup changed accepted artifacts")
	}
	if !reflect.DeepEqual(rootA.Expressions[1], callA) || !reflect.DeepEqual(rootB.Expressions[1], callB) || definition.Description.Folds != owner || !reflect.DeepEqual(analysis.Dependencies, manifest) {
		t.Fatal("accepted graph/definition mutated")
	}
	assertFolds := func(retained *exec.AcceptedFolds, saved exec.AcceptedFolds, entries []exec.FoldConstant) {
		t.Helper()
		if retained.Generation != saved.Generation || &retained.Entries[0] != &saved.Entries[0] || !reflect.DeepEqual(retained.Dependencies, saved.Dependencies) {
			t.Fatal("accepted generation/backing changed")
		}
		for i, entry := range entries {
			if !enginetest.SamePublishedFold(entry, retained.Entries[i]) {
				t.Fatal("accepted value/provenance changed")
			}
		}
	}
	assertFolds(foldsA, savedA, entriesA)
	assertFolds(foldsB, savedB, entriesB)
	found := false
	for _, dependency := range manifest {
		found = found || dependency.Ref.Address.ObjectID == 2739 && dependency.Ref.Address.ClassID == ids.ProcedureClassID
	}
	if !found {
		t.Fatal("missing selected full declaration dependency")
	}
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
