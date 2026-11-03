package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
)

// These witnesses inspect actual retained protocol graph/source owners. They do
// not create substitute providers, fake source inspection APIs or SQL goldens.
func bufferUnitPortal(t *testing.T, e *core.Engine, s *session.Session, name, query string) (*core.ProtocolPortal, *exec.Source, func(func(*core.StatementScope) error)) {
	t.Helper()
	enginetest.SessionSQL(t, s, "BEGIN")
	cycle := s.NewProtocolCycle()
	command := func(fn func(*core.StatementScope) error) { enginetest.PreparedFoldProtocolCommand(t, e, s, cycle, fn) }
	command(func(scope *core.StatementScope) error { _, err := scope.ParseProtocol(name, query, nil); return err })
	command(func(scope *core.StatementScope) error {
		_, err := scope.BindProtocolPortal(name, name, &enginetest.ProtocolReader{Data: []byte{0, 0, 0, 0, 0, 0}})
		return err
	})
	binding := core.PayloadOf(s).PreparedSQL.Binding(name)
	portal := core.ProtocolPortalOf(binding)
	if portal == nil || binding.Resource == nil || binding.Resource.Root == nil {
		t.Fatal("real accepted portal lacks private graph")
	}
	command(func(scope *core.StatementScope) error {
		result, err := scope.ExecuteProtocolPortal(name, 1, func([]session.ProtocolField) error { return nil }, nil, nil, core.CopyProtocolHooks{})
		if err == nil && !result.Suspended {
			t.Fatal("actual portal did not suspend")
		}
		return err
	})
	producer := core.PortalProducer(portal)
	if producer == nil || producer.Source == nil {
		t.Fatal("suspended portal lost capture owner")
	}
	return portal, producer.Source, command
}
func bufferUnitReleased(t *testing.T, source *exec.Source) {
	t.Helper()
	if source.ReadLease != nil || source.View != nil || source.Host != nil || source.Authority != nil || source.NestedStage != nil || source.NestedRoot != nil || source.Sources != nil || source.HashIndexes != nil || source.BloomIndexes != nil {
		t.Fatal("terminal source retained borrowed/live resources")
	}
}

func TestPgBuffercacheSourceOwnership(t *testing.T) {
	t.Run("two_sessions_and_same_session_aliases", func(t *testing.T) {
		e := bufferUnitEngine(t)
		for _, q := range []string{"CREATE TABLE buffer_pins(k integer)", "INSERT INTO buffer_pins VALUES(1),(2)"} {
			enginetest.Run(t, e, q)
		}
		a, b, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		node := bufferUnitNode(t, e, "buffer_pins")
		_, left, closeA := bufferUnitPortal(t, e, a, "buffer_a", "SELECT a.k FROM buffer_pins a CROSS JOIN buffer_pins b")
		if len(left.Sources) != 2 || left.Sources[0].ReadLease == nil || left.Sources[1].ReadLease == nil || left.Sources[0].ReadLease == left.Sources[1].ReadLease {
			t.Fatal("real aliases share an invocation/capture")
		}
		alias0, alias1 := left.Sources[0], left.Sources[1]
		bufferUnitFact(t, e, observer, node, 2, 1)
		_, right, closeB := bufferUnitPortal(t, e, b, "buffer_b", "SELECT k FROM buffer_pins")
		bufferUnitFact(t, e, observer, node, 3, 2)
		detached := bufferUnitCursor(t, e, observer)
		defer detached.Close()
		closeA(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("buffer_a") })
		bufferUnitReleased(t, left)
		bufferUnitReleased(t, alias0)
		bufferUnitReleased(t, alias1)
		bufferUnitFact(t, e, observer, node, 3, 1)
		closeB(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("buffer_b") })
		bufferUnitReleased(t, right)
		bufferUnitFact(t, e, observer, node, 3, 0)
		found := false
		for {
			row, present, err := detached.Next(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			if row.CompositeField(1).IsNull() {
				continue
			}
			oid, err := row.CompositeField(1).OID()
			if err != nil {
				t.Fatal(err)
			}
			if uint32(node) == oid {
				pins, err := row.CompositeField(8).Integer()
				if err != nil || pins != 2 {
					t.Fatal("snapshot retained mutable lease state", pins, err)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("retained snapshot lost occupied scalar identity")
		}
		enginetest.SessionSQL(t, a, "ROLLBACK")
		enginetest.SessionSQL(t, b, "ROLLBACK")
	})
	for _, ending := range []string{"execution_error", "cancellation", "session_end"} {
		t.Run(ending, func(t *testing.T) {
			e := bufferUnitEngine(t)
			for _, q := range []string{"CREATE TABLE buffer_terminal(k integer)", "INSERT INTO buffer_terminal VALUES(1),(2)"} {
				enginetest.Run(t, e, q)
			}
			owner, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			portal, source, command := bufferUnitPortal(t, e, owner, "buffer_terminal", "SELECT k FROM buffer_terminal")
			root := source
			node := bufferUnitNode(t, e, "buffer_terminal")
			bufferUnitFact(t, e, observer, node, 1, 1)
			if ending == "execution_error" {
				failure := errors.New("buffercache source consumer failure")
				var observed error
				command(func(scope *core.StatementScope) error {
					_, observed = scope.ExecuteProtocolPortal("buffer_terminal", 0, func([]session.ProtocolField) error { return failure }, nil, nil, core.CopyProtocolHooks{})
					return nil
				})
				if !errors.Is(observed, failure) {
					t.Fatal("actual producer failure was replaced", observed)
				}
				bufferUnitReleased(t, root)
				bufferUnitFact(t, e, observer, node, 1, 0)
				command(func(scope *core.StatementScope) error { return scope.CloseProtocolPortal("buffer_terminal") })
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			} else if ending == "cancellation" {
				ctx, cancel := context.WithCancel(context.Background())
				op, _, err := owner.AcquireCommand(ctx)
				if err != nil {
					cancel()
					t.Fatal(err)
				}
				scope, err := e.StartProtocolCommand(op, owner.Transactions.ProtocolCycle)
				if err != nil {
					op.Done()
					cancel()
					t.Fatal(err)
				}
				_, err = scope.ExecuteProtocolPortal("buffer_terminal", 0, func([]session.ProtocolField) error { cancel(); return context.Canceled }, nil, nil, core.CopyProtocolHooks{})
				finished := scope.Finish(err)
				op.Done()
				cancel()
				if !errors.Is(err, context.Canceled) || !errors.Is(finished, context.Canceled) {
					t.Fatal("admitted cancellation lost ordinary boundary", err, finished)
				}
				bufferUnitReleased(t, root)
				enginetest.SessionSQL(t, owner, "ROLLBACK")
			} else {
				owner.RequestEnd(session.CleanClose)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := owner.WaitEnd(ctx)
				cancel()
				if err != nil {
					t.Fatal("registered physical session did not release portal", err)
				}
			}
			bufferUnitReleased(t, root)
			if core.PortalProducer(portal) != nil {
				t.Fatal("terminal portal retained producer")
			}
			bufferUnitFact(t, e, observer, node, 1, 0)
		})
	}
}
