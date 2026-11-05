package logical

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

// The permanent public PreparedAuthority scenario owns changed operands and live
// privileges. This proof retains the actual accepted definition, graph, fold
// generation and backing, not a reconstructed binder or inspection registry.
func TestLogicalStreamPreparedOwnership(t *testing.T) {
	f := newFixture(t)
	s := f.session()
	f.must(s, "SET output_plugin_libraries=test_decoding")
	f.must(s, "CREATE TABLE t(id integer)")
	f.must(s, "SELECT pg_create_logical_replication_slot('owned','test_decoding')")
	f.must(s, "INSERT INTO t VALUES(1)")
	f.must(s, "BEGIN")
	defer f.must(s, "ROLLBACK")
	cycle := s.NewProtocolCycle()
	command := func(work func(*core.StatementScope) error) {
		enginetest.PreparedFoldProtocolCommand(t, f.e, s, cycle, work)
	}
	command(func(scope *core.StatementScope) error {
		_, err := scope.ParseProtocol("decode_owned", "SELECT COALESCE(-(7::integer),0) AS folded,data FROM pg_logical_slot_peek_changes('owned',NULL,NULL,VARIADIC ARRAY['include-xids','0','skip-empty-xacts','1'])", nil)
		return err
	})
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("decode_bound", "decode_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	registry := core.PayloadOf(s).PreparedSQL
	definition := registry.Lookup("decode_owned")
	portal := registry.Binding("decode_bound")
	if definition == nil || portal == nil || definition.Description.Folds == nil || definition.Description.Analysis == nil || portal.Resource == nil || portal.Resource.Root == nil || portal.Folds == nil {
		t.Fatal("missing actual accepted owners")
	}
	owner, analysis := definition.Description.Folds, definition.Description.Analysis
	accepted := owner.Current()
	if len(accepted.Entries) == 0 {
		t.Fatal("accepted fold backing absent")
	}
	savedEntries := slices.Clone(accepted.Entries)
	savedDependencies := slices.Clone(analysis.Dependencies)
	graph, group := portal.Resource.Root, *portal.Folds
	source, ok := graph.Child.(*plan.FunctionFromNode)
	if !ok || len(source.Calls) != 1 {
		t.Fatal("missing real accepted decoding source", graph.Child)
	}
	call, ok := source.Calls[0].(functions.LogicalDecoding)
	if !ok || len(call.Args) != 4 {
		t.Fatal("decoding operands not retained by real source", source.Calls)
	}
	selected, err := exec.ExpressionRoutineDescriptor(call)
	if err != nil || selected == nil || selected.Address().ObjectID != 3784 || expr.InfoOf(call).RoutineReference == nil {
		t.Fatal("accepted provider authority", selected, err)
	}
	operands := slices.Clone(call.Args)
	operandBacking := &call.Args[0]
	capture := func() (catalog.Read, core.PreparedEnvironment) {
		op, err := s.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		f.e.Mu.Lock()
		defer f.e.Mu.Unlock()
		auth, err := f.e.AuthorizationLocked(op)
		if err != nil {
			t.Fatal(err)
		}
		env, err := f.e.PreparedEnvironmentLocked(auth, true)
		if err != nil {
			t.Fatal(err)
		}
		return auth.Catalog, env
	}
	current, env := capture()
	candidate, err := core.BeginFoldPublication(&definition.Description, definition.Body, env, current, nil)
	if err != nil || candidate == nil {
		t.Fatal("real candidate absent", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err = candidate.Accept(ctx, current, env, current.Epoch()); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled fold publication accepted", err)
	}
	after := owner.Current()
	if definition.Description.Folds != owner || definition.Description.Analysis != analysis || after.Generation != accepted.Generation || &after.Entries[0] != &accepted.Entries[0] || !reflect.DeepEqual(accepted.Entries, savedEntries) || !reflect.DeepEqual(analysis.Dependencies, savedDependencies) {
		t.Fatal("rejected publication mutated accepted owner")
	}
	// A second real portal borrows only the same accepted immutable folds; its
	// executable graph is independently owned and old portal backing stays valid.
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal("decode_second", "decode_owned", &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	fresh := registry.Binding("decode_second")
	if fresh == nil || fresh.Resource == nil || fresh.Resource.Root == graph {
		t.Fatal("portal reused live graph")
	}
	if portal.Resource.Root != graph || portal.Folds.Generation != group.Generation || !reflect.DeepEqual(portal.Folds.Entries, group.Entries) || &portal.Folds.Entries[0] != &group.Entries[0] {
		t.Fatal("new binding changed retained graph/fold backing")
	}
	retained := graph.Child.(*plan.FunctionFromNode).Calls[0].(functions.LogicalDecoding)
	if &retained.Args[0] != operandBacking || !reflect.DeepEqual(retained.Args, operands) || retained.RoutineReference != call.RoutineReference {
		t.Fatal("retained provider/typed operand backing changed")
	}
}
