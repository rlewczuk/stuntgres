package engine

import (
	"context"
	"strconv"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Routine transactional utility admission is intentionally not widened. Exercise
// the reverse dependency race through the real internal catalog-stage/journal
// machinery, independently of the public cast-creator/autocommit-retirer proof.
func TestTransactionalDefinitionLockResources(t *testing.T) {
	e := New()
	setup, setupDone := roleOperation(t, e, "postgres")
	for _, query := range []string{
		"CREATE TYPE retirement_src AS ENUM ('red')",
		"CREATE FUNCTION retirement_score(retirement_src) RETURNS integer LANGUAGE SQL AS $$ SELECT 41 $$",
	} {
		domainUnitPublish(t, e, domainUnitStage(t, e, setup, query))
	}
	setupDone()
	base := e.CatalogRead()
	source := domainUnitType(t, base, "retirement_src")
	key := ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: source.Namespace(), Name: "retirement_score", Signature: strconv.FormatUint(uint64(source.SQLType.ID()), 10)}
	procedure := base.Get(base.NameAddress(key))
	if procedure == nil {
		t.Fatal("missing genuine function provider")
	}
	holder := sessionStart(t, e, nil)
	holderRef := transactionBegin(t, holder)
	holderEnded := false
	defer func() {
		if !holderEnded {
			transactionEnd(t, holder, holderRef, false)
		}
	}()
	op := transactionOperation(t, holder)
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	drop := statement(t, "DROP FUNCTION retirement_score(retirement_src)")
	if err := scope.Gate(drop); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	// Exercise candidate staging and journal admission independently of SQL dispatch.
	retirement := domainUnitStage(t, e, op, "DROP FUNCTION retirement_score(retirement_src)")
	retirement, err = admitDefinitionMutation(scope, retirement, drop, sqltype.InputEnvironment{}, len(op.Notices))
	if err == nil {
		err = StageExpressionDependencies(op.Context(), retirement, retirement.DomainTargets, parser.DropRestrict, scope, nil, nil, ids.ObjectAddress{})
	}
	if err == nil {
		_, err = e.ValidateCatalogStage(op.Context(), retirement)
	}
	if err == nil {
		err = e.StageCatalogOverlay(scope, retirement, scope.Owner.Catalog.Clone())
	}
	err = scope.Finish(err)
	op.Done()
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(retirement)
	e.Mu.Lock()
	private := e.PrivateCatalogLocked(holder.Transactions.Catalog)
	e.Mu.Unlock()
	if private.Get(procedure.Address()) != nil || e.CatalogRead().Get(procedure.Address()) != procedure {
		t.Fatal("private provider retirement leaked or retained provider")
	}
	creator := sessionStart(t, e, nil)
	creatorRef := transactionBegin(t, creator)
	creatorEnded := false
	defer func() {
		if !creatorEnded {
			transactionEnd(t, creator, creatorRef, false)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		operation, err := creator.Acquire(ctx)
		if err == nil {
			_, err = e.Execute(operation, statement(t, "CREATE CAST (retirement_src AS integer) WITH FUNCTION retirement_score(retirement_src)"), 0, nil)
			operation.Done()
		}
		done <- err
	}()
	joined := false
	defer func() {
		cancel()
		if !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("cast dependency waiter did not terminate")
			}
		}
	}()
	lockPending(t, e, 1)
	state := e.Locks.Inspect(holder.Transactions.Ticket)
	ref := e.Catalog.Domain.Ref(base.Epoch(), procedure.Address())
	resource := lock.Resource{ObjectRef: ref}
	entry := state.Resources[resource]
	if entry == nil || len(entry.Pending) != 1 {
		t.Fatal("cast did not wait on its actual routine identity")
	}
	transactionEnd(t, holder, holderRef, false)
	holderEnded = true
	select {
	case err = <-done:
		joined = true
	case <-time.After(5 * time.Second):
		t.Fatal("cast dependency waiter did not resume after provider rollback")
	}
	if err != nil {
		t.Fatal(err)
	}
	if e.CatalogRead().Get(procedure.Address()) != procedure {
		t.Fatal("provider rollback changed exact identity")
	}
	e.Mu.Lock()
	private = e.PrivateCatalogLocked(creator.Transactions.Catalog)
	e.Mu.Unlock()
	cast, ok := private.Cast(source.SQLType.ID(), sqltype.Int4)
	if !ok || cast.Function != procedure.Address() {
		t.Fatal("resumed declaration lost provider identity", cast)
	}
	if _, ok := e.CatalogRead().Cast(source.SQLType.ID(), sqltype.Int4); ok {
		t.Fatal("resumed cast published before transaction end")
	}
	transactionRows(t, creator, "SELECT 'red'::retirement_src::integer", 41)
	// Roll back exactly the declaration's private journal, retaining the provider
	// and a healthy physical owner for a fresh attempt.
	transactionEnd(t, creator, creatorRef, false)
	creatorEnded = true
	if _, ok := e.CatalogRead().Cast(source.SQLType.ID(), sqltype.Int4); ok || e.CatalogRead().Get(procedure.Address()) != procedure {
		t.Fatal("rollback retained cast or retired provider")
	}
	if creator.TransactionState() != session.TxIdle {
		t.Fatal("rollback left physical owner in a failed transaction")
	}
	transactionRows(t, creator, "SELECT 41", 41)
}
