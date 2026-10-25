package core_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
)

func uniqueAsyncUnit(t *testing.T, s *session.Session, query string) (<-chan deleteOutcome, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := enginetest.Statement(t, query)
	done := make(chan deleteOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, _, err := s.AcquireCommand(ctx)
		var result engine.Result
		if err == nil {
			result, err = engine.EngineOf(s).Execute(op, ast, 0, nil)
			err = op.CommandError(err)
			op.Done()
		}
		done <- deleteOutcome{result, err}
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Error("UNIQUE worker did not join")
		}
	})
	return done, cancel
}

func uniqueClaimsEmpty(t *testing.T, e *engine.Engine) {
	t.Helper()
	enginetest.LockEmpty(t, e)
	uniqueKeyBucketsEmpty(t, e)
}

func uniqueKeyBucketsEmpty(t *testing.T, e *engine.Engine) {
	t.Helper()
	lockState := e.Locks.Inspect()
	if lockState.KeyBuckets != 0 {
		t.Fatal("retained unique key buckets", lockState.KeyBuckets)
	}
}

func TestUniqueClaimSavepointCancellation(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, a, "SAVEPOINT m")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(2)")
	enginetest.SessionSQL(t, a, "ROLLBACK TO m")
	lockState := e.Locks.Inspect()
	claims := 0
	for resource, entry := range lockState.Resources {
		if resource.Key() != nil && len(entry.Holders) != 0 {
			claims++
		}
	}
	if claims != 1 {
		t.Fatal("savepoint did not restore claim contributions", claims)
	}
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(2)")
	done, cancel := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	enginetest.LockPending(t, e, 1)
	cancel()
	deleteAwait(t, done, 0, context.Canceled)
	enginetest.LockPending(t, e, 0)
	enginetest.TransactionEnd(t, a, ar, true)
	btreeWantKeys(t, e, "u", "1", "2")
	uniqueClaimsEmpty(t, e)
}

func TestUniqueClaimDeadlock(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(2)")
	first, _ := uniqueAsyncUnit(t, a, "INSERT INTO t VALUES(2)")
	enginetest.LockPending(t, e, 1)
	second, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	// Victim cleanup can wake the survivor before the victim reports its error.
	// Own both outcomes before identifying the victim by its actual error.
	var firstResult, secondResult deleteOutcome
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for first != nil || second != nil {
		select {
		case firstResult = <-first:
			first = nil
		case secondResult = <-second:
			second = nil
		case <-deadline.C:
			t.Fatal("unique cycle workers did not terminate")
		}
	}
	if (firstResult.err == nil) == (secondResult.err == nil) {
		t.Fatal("want exactly one deadlock victim and one successful inserter", firstResult, secondResult)
	}
	victim, survivor := firstResult, secondResult
	if firstResult.err == nil {
		victim, survivor = secondResult, firstResult
	}
	var diagnostic *sqlstate.Error
	if !errors.As(victim.err, &diagnostic) {
		t.Fatal(victim.err)
	}
	expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details.", Detail: diagnostic.Detail}
	if !reflect.DeepEqual(*diagnostic, expected) || !strings.Contains(diagnostic.Detail, "waits for UniqueKey on relation ") || !strings.Contains(diagnostic.Detail, " unique key") {
		t.Fatal(diagnostic)
	}
	if victim.result.Count != 0 || victim.result.Rows != nil {
		t.Fatal("victim exposed effects", victim.result)
	}
	if survivor.err != nil || survivor.result.Count != 1 || survivor.result.Rows != nil {
		t.Fatal("successful inserter", survivor)
	}
	enginetest.TransactionEnd(t, a, ar, false)
	enginetest.TransactionEnd(t, b, br, false)
	uniqueClaimsEmpty(t, e)
}

func TestUniqueRealRetainedTemporaryRemapping(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "same_owner"}[self], func(t *testing.T) {
			ctx := context.Background()
			source := engine.New()
			a := enginetest.StartSession(t, source, nil)
			enginetest.SessionSQL(t, a, "CREATE SCHEMA collision_padding")
			enginetest.SessionSQL(t, a, "CREATE SCHEMA portable CREATE TABLE t(k INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)")
			enginetest.SessionSQL(t, a, "INSERT INTO portable.t VALUES(7)")
			before := source.CatalogRead()
			original := before.MustRelation(enginetest.RoleTableRef(t, source, "portable.t").Address)
			originalConstraint := original.Constraints.Unique[0]
			originalConstraint.Attributes = append([]int32(nil), originalConstraint.Attributes...)
			sourceRoot := source.Storage.IndexRoots[originalConstraint.Index]
			var dump bytes.Buffer
			if err := engine.Dump(ctx, source, &dump); err != nil {
				t.Fatal(err)
			}
			target := engine.New()
			b := enginetest.StartSession(t, target, nil)
			if self {
				target, b = source, a
			}
			enginetest.SessionSQL(t, b, "CREATE TEMP SEQUENCE kept CACHE 10")
			sequenceScalar(t, b, "SELECT nextval('kept')", 1)
			_, description := sequencePreparedDescription(t, target, b, "SELECT nextval('kept')")
			tempRef := description.Sequences.Constants[0].Ref
			temp := target.Catalog.State.Get(tempRef.Address)
			generator := target.Storage.Sequences[tempRef.Address]
			memory := engine.PayloadOf(b).Sequences.Memory(tempRef.Address)
			if !self && tempRef.Address.ObjectID != original.Address().ObjectID {
				t.Fatal("fixture must produce a real retained TEMP address collision", tempRef.Address, original.Address())
			}
			if _, err := target.ReplaceCatalog(ctx, source, before); err != nil {
				t.Fatal(err)
			}
			if target.Catalog.State.Get(tempRef.Address) != temp || target.Storage.Sequences[tempRef.Address] != generator || engine.PayloadOf(b).Sequences.Memory(tempRef.Address) != memory {
				t.Fatal("retained TEMP identity changed")
			}
			parent := target.Catalog.State.MustRelation(enginetest.RoleTableRef(t, target, "portable.t").Address)
			index, root := btreeRoot(t, target, "portable.u")
			if parent.Constraints.Unique[0].Index != index.Address() || index.SQLIndex.Parent != parent.Address() || index.SQLIndex.Constraint.OID != parent.Constraints.Unique[0].OID || len(root.Entries) != 1 || root.Entries[0].Row == 0 || root.Entries[0].Version == 0 {
				t.Fatal("broken imported closure")
			}
			if root == sourceRoot || self && root.Entries[0].Row == sourceRoot.Entries[0].Row {
				t.Fatal("replacement imported source root/row identities")
			}
			if !reflect.DeepEqual(original.Constraints.Unique[0], originalConstraint) {
				t.Fatal("source definition mutated")
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 2)
			var again bytes.Buffer
			if err := engine.Dump(ctx, target, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("noncanonical remapped replay", err, again.String())
			}
			if err := engine.Restore(ctx, target, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 3)
			result, err := enginetest.Execute(target, ctx, enginetest.Statement(t, "INSERT INTO portable.t VALUES(7)"), nil)
			if result.Count != 0 || result.Rows != nil {
				t.Fatal(result)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (k)=(7) already exists.", Schema: "portable", Table: "t", Constraint: "u"})
			uniqueClaimsEmpty(t, target)
		})
	}
}

func TestUniquePrivateBaselineRootsAndBindingLifetime(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(extra INT,k INT CONSTRAINT u UNIQUE)")
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(0,1)")
	parentRef := enginetest.RoleTableRef(t, e, "t")
	original := e.Catalog.State.MustRelation(parentRef.Address)
	originalRoot := e.Storage.IndexRoots[original.Constraints.Unique[0].Index]
	ar := enginetest.TransactionBegin(t, a)
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err := scope.Gate(enginetest.Statement(t, "INSERT INTO t VALUES(0,2)")); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	first, err := dml.BindUniqueWrite(scope.WriteHost(), parentRef)
	if err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	again, err := dml.BindUniqueWrite(scope.WriteHost(), parentRef)
	if err != nil || first != again || first.Keys[0].Ordering.Slots[0] != 1 {
		scope.Finish(err)
		op.Done()
		t.Fatal("immutable binding not reused", err)
	}
	if err := scope.Finish(nil); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	if scope.UniqueBinding != nil {
		t.Fatal("Finish retained statement binding")
	}
	enginetest.SessionSQL(t, a, "ALTER TABLE t DROP extra")
	enginetest.SessionSQL(t, a, "ALTER TABLE t ALTER k TYPE BIGINT")
	changes := a.Transactions.Catalog
	var rebuilt ids.ObjectAddress
	var root *storage.IndexRoot
	for address, candidate := range index.JournalIndexesOrZero(changes).UniqueRoots {
		rebuilt, root = address, candidate
	}
	if root == nil || len(root.Entries) != 1 || root == originalRoot {
		t.Fatal("missing private TYPE baseline root")
	}
	enginetest.SessionSQL(t, a, "SAVEPOINT after_type")
	enginetest.SessionSQL(t, a, "ALTER TABLE t ALTER k TYPE TEXT USING k::text")
	if index.JournalIndexesOrZero(a.Transactions.Catalog).UniqueRoots[rebuilt] != nil {
		t.Fatal("removed backing retained private baseline")
	}
	if len(root.Entries) != 1 || btreeKeys(t, root)[0] != "1" {
		t.Fatal("retained private root mutated")
	}
	enginetest.SessionSQL(t, a, "ROLLBACK TO after_type")
	if index.JournalIndexesOrZero(a.Transactions.Catalog).UniqueRoots[rebuilt] != root {
		t.Fatal("savepoint did not share/restore immutable baseline")
	}
	op, err = a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err = e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err := scope.Gate(enginetest.Statement(t, "INSERT INTO t VALUES(3)")); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	next, err := dml.BindUniqueWrite(scope.WriteHost(), parentRef)
	if err != nil || next == first || next.Keys[0].Ordering.Slots[0] != 0 {
		scope.Finish(err)
		op.Done()
		t.Fatal("descriptor-changing operation reused stale slots", err)
	}
	if err := scope.Finish(nil); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if index.JournalIndexesOrZero(a.Transactions.Catalog).UniqueRoots[rebuilt] != root || len(root.Entries) != 1 {
		t.Fatal("DML rebuilt/modified baseline instead of overlaying effects")
	}
	enginetest.TransactionEnd(t, a, ar, false)
	if e.Catalog.State.Get(parentRef.Address) != original || e.Storage.IndexRoots[original.Constraints.Unique[0].Index] != originalRoot {
		t.Fatal("rollback changed committed closure")
	}
	uniqueClaimsEmpty(t, e)
}

func TestUniqueClaimStatementTimeout(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	enginetest.SessionSQL(t, b, "SET statement_timeout='80ms'")
	done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	select {
	case outcome := <-done:
		if outcome.result.Count != 0 || outcome.result.Rows != nil {
			t.Fatal(outcome.result)
		}
		nullCountUnitDiagnostic(t, outcome.err, sqlstate.Error{Code: "57014", Message: "canceling statement due to statement timeout"})
	case <-time.After(5 * time.Second):
		t.Fatal("UNIQUE statement deadline did not terminate")
	}
	enginetest.TransactionEnd(t, a, ar, false)
	uniqueClaimsEmpty(t, e)
}

func TestUniqueClaimEpochFenceReadmission(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(1)")
	done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	enginetest.LockPending(t, e, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := engine.Reset(ctx, e); err != nil {
		t.Fatal(err)
	}
	deleteAwait(t, done, 0, ids.ErrCatalogReplaced)
	enginetest.TransactionEnd(t, a, ar, false)
	uniqueClaimsEmpty(t, e)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1)")
	btreeWantKeys(t, e, "u", "1")
	uniqueClaimsEmpty(t, e)
}

func TestUniqueKeyChangeTwoUpgradersEvaluateOnce(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	observer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, observer, "CREATE SEQUENCE draws")
	enginetest.SessionSQL(t, observer, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v BIGINT)")
	enginetest.SessionSQL(t, observer, "INSERT INTO t VALUES(1,0)")
	ar, br := enginetest.TransactionBegin(t, a), enginetest.TransactionBegin(t, b)
	enginetest.TransactionRows(t, a, "SELECT k FROM t FOR KEY SHARE", 1)
	enginetest.TransactionRows(t, b, "SELECT k FROM t FOR KEY SHARE", 1)
	first, _ := uniqueAsyncUnit(t, a, "UPDATE t SET k=2,v=nextval('draws') WHERE k=1")
	enginetest.LockPending(t, e, 1)
	second, _ := uniqueAsyncUnit(t, b, "UPDATE t SET k=2,v=nextval('draws') WHERE k=1")
	// Victim cleanup wakes the survivor before its worker sends the error.
	// Arrival order therefore cannot identify which conversion was rejected.
	var firstResult, secondResult deleteOutcome
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for first != nil || second != nil {
		select {
		case firstResult = <-first:
			first = nil
		case secondResult = <-second:
			second = nil
		case <-deadline.C:
			t.Fatal("key-change conversion workers did not terminate")
		}
	}
	if (firstResult.err == nil) == (secondResult.err == nil) {
		t.Fatal("want exactly one deadlock victim and one successful updater", firstResult, secondResult)
	}
	victim, survivor := firstResult, secondResult
	draws := int64(2)
	if firstResult.err == nil {
		victim, survivor = secondResult, firstResult
		draws = 1
	}
	var diagnostic *sqlstate.Error
	if !errors.As(victim.err, &diagnostic) {
		t.Fatal(victim.err)
	}
	expected := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40P01", Message: "deadlock detected", Hint: "See server log for query details.", Detail: diagnostic.Detail}
	if !reflect.DeepEqual(*diagnostic, expected) || !strings.Contains(diagnostic.Detail, "waits for Update on relation ") || !strings.Contains(diagnostic.Detail, "waits for NoKeyUpdate on relation ") {
		t.Fatal(diagnostic)
	}
	if victim.result.Count != 0 || victim.result.Rows != nil {
		t.Fatal(victim.result)
	}
	if survivor.err != nil || survivor.result.Count != 1 || survivor.result.Rows != nil {
		t.Fatal("successful updater", survivor)
	}
	enginetest.TransactionEnd(t, a, ar, false)
	enginetest.TransactionEnd(t, b, br, false)
	sequenceScalar(t, observer, "SELECT last_value FROM draws", draws)
	btreeWantKeys(t, e, "u", "1")
	uniqueClaimsEmpty(t, e)
}

func btreeWantKeys(t *testing.T, e *engine.Engine, name string, want ...string) *storage.IndexRoot {
	t.Helper()
	_, root := btreeRoot(t, e, name)
	if got := btreeKeys(t, root); !reflect.DeepEqual(got, want) {
		t.Fatal(name, got, want)
	}
	return root
}
