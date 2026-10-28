package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The public provider witness exercises COPY's panic boundary. This composed
// scope witness inventories the very same failed-Finish disposal authority,
// including ownership that disappearance from the progress view cannot prove.
func TestCopyProgressForeignFinishPanicCleanup(t *testing.T) {
	e := engine.New()
	provider := &fdwUnitLifecycle{
		fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true, echo: true}},
		tx:            &fdwUnitTransaction{},
	}
	definition, err := fdw.ValidateWrapper(fdw.WrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "unit_finish_handler"}, Wrapper: provider})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.InstallForeignDefinitions([]fdw.WrapperDefinition{definition}, nil,
		func(_ context.Context, value driver.Value, usage sqltype.Usage, _ sqltype.InputEnvironment) (sqltype.Value, error) {
			n, ok := value.(int64)
			if !ok {
				return sqltype.Value{}, errors.New("expected int4 FDW datum")
			}
			return sqltype.Integer(usage.Type, n)
		},
		func(_ context.Context, value sqltype.Value, _ sqltype.OutputEnvironment) (driver.Value, error) {
			n, err := value.Integer()
			return n, err
		},
	); err != nil {
		t.Fatal(err)
	}
	worker := sessionStart(t, e, nil)
	for _, query := range []string{
		"CREATE FOREIGN DATA WRAPPER unit_finish HANDLER unit_finish_handler",
		"CREATE SERVER unit_finish_server FOREIGN DATA WRAPPER unit_finish",
		"CREATE USER MAPPING FOR CURRENT_USER SERVER unit_finish_server",
		"CREATE FOREIGN TABLE unit_finish_target(v integer) SERVER unit_finish_server",
		"CREATE TABLE unit_finish_log(v integer)",
		"CREATE FUNCTION unit_finish_after() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO unit_finish_log VALUES(NEW.v); RETURN NULL; END$$",
		"CREATE TRIGGER unit_finish_trigger AFTER INSERT ON unit_finish_target FOR EACH ROW EXECUTE FUNCTION unit_finish_after()",
	} {
		sessionSQL(t, worker, query)
	}
	observer := sessionStart(t, e, nil)
	observerOp := transactionOperation(t, observer)
	defer observerOp.Done()
	op := transactionOperation(t, worker)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("failed COPY after terminal callback panic")
	defer scope.Finish(failure)
	count, err := scope.Copy(enginetest.Statement(t, "COPY unit_finish_target FROM STDIN").(*parser.Copy), strings.NewReader("1\n"), nil)
	if err != nil || count != 1 || provider.calls != 1 || provider.begins != 1 {
		t.Fatal("real COPY provider acceptance", count, err, provider.calls, provider.begins)
	}
	if provider.ends != 0 || len(scope.Stage.Rows) != 0 {
		t.Fatal("COPY prematurely ran terminal or queued AFTER work", provider.ends, len(scope.Stage.Rows))
	}
	copyDone := scope.Owner.CopyDone
	if scope.Owner.CopyLease == nil || copyDone == nil {
		t.Fatal("COPY has no live reservation before Finish")
	}
	probe := core.ScopeCopyProgressProbe(scope)
	marker := &struct{ name string }{"actual Finish-owned EndForeignInsert panic"}
	provider.check = func() {
		if provider.ends == 0 {
			return
		}
		if len(scope.Stage.Rows) == 0 {
			t.Error("terminal hook entered before real queued AFTER effects")
		}
		if facts := copyProgressFacts(t, e, observerOp); len(facts) != 1 || facts[0].TuplesProcessed != 1 {
			t.Errorf("terminal callback progress=%+v", facts)
		}
		panic(marker)
	}
	var interrupted any
	func() {
		defer func() { interrupted = recover() }()
		_ = scope.Finish(nil)
	}()
	provider.check = nil
	if interrupted != marker || provider.ends != 1 || scope.Finished {
		t.Fatal("wrong terminal panic or premature completion", interrupted, provider.ends, scope.Finished)
	}
	// This is the existing failed-scope Finish used by the public COPY boundary,
	// not a new statement, provider retry, or test-only reservation cleanup.
	if err := scope.Finish(failure); !errors.Is(err, failure) {
		t.Fatal("failed Finish lost primary failure", err)
	}
	op.Done()
	if !scope.Finished || scope.Owner.State != session.TxIdle || scope.Owner.CopyLease != nil || scope.Owner.CopyDone != nil || scope.Owner.Ticket != nil || core.OwnerHasForeign(scope.Owner) {
		t.Fatal("failed Finish retained transaction/reservation/participant ownership")
	}
	if scope.View != nil || scope.Stage.Rows != nil || scope.Stage.Deletes != nil || scope.LockTicket != nil || scope.ReplacementRead {
		t.Fatal("failed Finish retained view/stage/lock/replacement fence")
	}
	select {
	case <-copyDone:
	default:
		t.Fatal("COPY reservation completion channel was not released")
	}
	if !scope.Owner.Admission.TryLock() {
		t.Fatal("failed Finish retained transaction admission")
	}
	scope.Owner.Admission.Unlock()
	if !e.ReplacementMu.TryLock() {
		t.Fatal("failed Finish retained replacement admission")
	}
	e.ReplacementMu.Unlock()
	worker.Mu.Lock()
	leases, operations := worker.Leases, len(worker.Operations)
	worker.Mu.Unlock()
	if leases != 0 || operations != 0 {
		t.Fatal("terminal panic retained operation lease", leases, operations)
	}
	if provider.ends != 1 || !reflect.DeepEqual(provider.tx.calls, []string{"begin-statement", "statement-failure", "rollback"}) {
		t.Fatal("terminal callback replay or success publication", provider.ends, provider.tx.calls)
	}
	if facts := copyProgressFacts(t, e, observerOp); len(facts) != 0 {
		t.Fatal("retained terminal progress", facts)
	}
	if probe().TuplesProcessed != 1 {
		t.Fatal("failed Finish replayed COPY acceptance")
	}
	transactionRows(t, worker, "SELECT count(*) FROM unit_finish_log", 0)
}
