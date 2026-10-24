package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type plExceptionFaultTx struct {
	fdwUnitTransaction
	saveError, rollbackError error
	canceledRollback         bool
}

func (x *plExceptionFaultTx) Savepoint(_ context.Context, name string) error {
	x.record("save:" + name)
	return x.saveError
}
func (x *plExceptionFaultTx) RollbackTo(ctx context.Context, name string) error {
	x.canceledRollback = ctx.Err() != nil
	x.record("rollback:" + name)
	return x.rollbackError
}

func plExceptionUnlocked(t *testing.T, e *Engine, s *StatementScope) func() {
	return func() {
		t.Helper()
		if !e.mu.TryLock() {
			t.Error("provider callback holds engine publication lock")
		} else {
			e.mu.Unlock()
		}
		if !s.owner.Mu.TryLock() {
			t.Error("provider callback holds owner lock")
		} else {
			s.owner.Mu.Unlock()
		}
		if !s.op.Session.Mu.TryLock() {
			t.Error("provider callback holds session lock")
		} else {
			s.op.Session.Mu.Unlock()
		}
		if !e.replacementMu.TryLock() {
			t.Error("provider callback holds replacement reservation")
		} else {
			e.replacementMu.Unlock()
		}
	}
}

func TestPLExceptionParticipants(t *testing.T) {
	t.Run("late_enlistment_and_balanced_marks", func(t *testing.T) {
		e, s := plExceptionUnitScope(t)
		outer, err := s.beginPLException()
		if err != nil {
			t.Fatal(err)
		}
		inner, err := s.beginPLException()
		if err != nil {
			t.Fatal(err)
		}
		provider := &fdwUnitLifecycle{tx: &fdwUnitTransaction{}}
		provider.tx.check = plExceptionUnlocked(t, e, s)
		request := catalog.ForeignRequest{HandlerOID: 1, ServerOID: 2, MappingOID: 3, RoleOID: 4}
		if _, err := s.enlistForeign(s.op.Context(), provider, request, true); err != nil {
			t.Fatal(err)
		}
		if err := s.endPLException(inner, true, errors.New("body")); err != nil {
			t.Fatal(err)
		}
		if err := s.endPLException(outer, false, nil); err != nil {
			t.Fatal(err)
		}
		want := []string{"save:" + fdw.MarkName(outer.serial), "save:" + fdw.MarkName(inner.serial), "begin-statement", "rollback:" + fdw.MarkName(inner.serial), "release:" + fdw.MarkName(inner.serial), "release:" + fdw.MarkName(outer.serial)}
		if !reflect.DeepEqual(provider.tx.calls, want) {
			t.Fatal("late participant did not replay chronological unnamed marks", provider.tx.calls, want)
		}
	})
	t.Run("acquisition_failure_skips_body", func(t *testing.T) {
		e, s := plExceptionUnitScope(t)
		failure := errors.New("savepoint acquisition failure")
		tx := &plExceptionFaultTx{saveError: failure}
		tx.check = plExceptionUnlocked(t, e, s)
		ownerExtensionOf(s.owner).foreign.Enlist(&fdw.Participant{Tx: tx, Capabilities: catalog.ForeignTransactionCapabilities{Savepoints: true}})
		frame := &proc.PLFrame{Program: &proc.PLProgram{}}
		block := &proc.PLBlock{Statements: []proc.PLInstruction{{Syntax: &parser.PLStatement{Return: true}}}, Handlers: []proc.PLHandler{{Conditions: []proc.PLCondition{{Others: true}}}}}
		_, flow, err := s.executePLBlock(nil, frame, block, e.CatalogRead())
		if flow.kind != plNormal || !errors.Is(err, failure) || len(s.owner.Savepoints) != 0 || !reflect.DeepEqual(tx.calls, []string{"save:stuntgres_sql_1", "rollback"}) {
			t.Fatal("failed acquisition ran body/retained provider mark", flow, err, tx.calls)
		}
	})
	t.Run("rollback_failure_preserves_primary_and_skips_handler", func(t *testing.T) {
		e, s := plExceptionUnitScope(t)
		failure := errors.New("provider rollback failure")
		tx := &plExceptionFaultTx{rollbackError: failure}
		tx.check = plExceptionUnlocked(t, e, s)
		ownerExtensionOf(s.owner).foreign.Enlist(&fdw.Participant{Tx: tx, Capabilities: catalog.ForeignTransactionCapabilities{Savepoints: true}})
		text, _ := e.CatalogRead().LookupType(sqltype.Text)
		boolean, ok := e.CatalogRead().LookupType(sqltype.Bool)
		if !ok {
			t.Fatal("missing boolean")
		}
		program := &proc.PLProgram{FoundSlot: 2, Variables: []proc.PLVariable{
			{Usage: text.Usage()},
			{Usage: text.Usage()},
			{Name: "found", Usage: boolean.Usage(), Role: proc.PLFoundSlot},
		}}
		frame := proc.NewPLFrame(program, &proc.Definition{}, &proc.Frame{}, sqltype.CollationRef{})
		defer frame.Close()
		scope := &proc.PLBlock{Bindings: map[string]int{"sqlstate": 0, "sqlerrm": 1}}
		original := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero", Detail: "original detail", Context: "original context"}
		frame.EnterHandler(scope, original)
		block := &proc.PLBlock{Statements: []proc.PLInstruction{{Syntax: &parser.PLStatement{Rethrow: true}}}, HandlerScope: scope, Handlers: []proc.PLHandler{{Conditions: []proc.PLCondition{{Others: true}}, Statements: []proc.PLInstruction{{Syntax: &parser.PLStatement{Return: true}}}}}}
		enclosing := &proc.PLBlock{Statements: []proc.PLInstruction{{Block: block}}, HandlerScope: scope, Handlers: []proc.PLHandler{{Conditions: []proc.PLCondition{{Others: true}}, Statements: []proc.PLInstruction{{Syntax: &parser.PLStatement{Return: true}}}}}}
		_, flow, err := s.executePLBlock(nil, frame, enclosing, e.CatalogRead())
		var diagnostic *sqlstate.Error
		var cleanup *plExceptionCleanupError
		if flow.kind != plNormal || !errors.As(err, &cleanup) || !errors.As(err, &diagnostic) || diagnostic != original || !errors.Is(err, failure) {
			t.Fatal("cleanup failure swallowed/replaced origin or ran handler", flow, err)
		}
	})
	t.Run("cancellation_independent_cleanup", func(t *testing.T) {
		e, s := plExceptionUnitScope(t)
		tx := &plExceptionFaultTx{}
		tx.check = plExceptionUnlocked(t, e, s)
		ownerExtensionOf(s.owner).foreign.Enlist(&fdw.Participant{Tx: tx, Capabilities: catalog.ForeignTransactionCapabilities{Savepoints: true}})
		mark, err := s.beginPLException()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(s.op.Context())
		cancel()
		s.op.Ctx = ctx
		if err := s.endPLException(mark, true, context.Canceled); err != nil {
			t.Fatal(err)
		}
		if tx.canceledRollback {
			t.Fatal("provider rollback inherited cancellation")
		}
	})
	t.Run("ordinary_errors_not_caught", func(t *testing.T) {
		e, s := plExceptionUnitScope(t)
		ctx, cancel := context.WithCancel(s.op.Context())
		cancel()
		s.op.Ctx = ctx
		frame := &proc.PLFrame{Program: &proc.PLProgram{}}
		block := &proc.PLBlock{Statements: []proc.PLInstruction{{Syntax: &parser.PLStatement{Return: true}}}, Handlers: []proc.PLHandler{{Conditions: []proc.PLCondition{{Others: true}}, Statements: []proc.PLInstruction{{Syntax: &parser.PLStatement{Return: true}}}}}}
		_, flow, err := s.executePLBlock(nil, frame, block, e.CatalogRead())
		if flow.kind != plNormal || !errors.Is(err, context.Canceled) || len(s.owner.Savepoints) != 0 {
			t.Fatal("OTHERS swallowed ordinary context error", flow, err)
		}
	})
}
