package engine

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func uniqueAsyncUnit(t *testing.T, s *Session, query string) (<-chan deleteOutcome, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	ast := statement(t, query)
	done := make(chan deleteOutcome, 1)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		op, _, err := s.AcquireCommand(ctx)
		var result Result
		if err == nil {
			result, err = s.Engine().Execute(op, ast, 0, nil)
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

func uniqueClaimsEmpty(t *testing.T, e *Engine) {
	t.Helper()
	lockEmpty(t, e)
	e.locks.mu.Lock()
	defer e.locks.mu.Unlock()
	if len(e.locks.keyBuckets) != 0 {
		t.Fatal("retained empty unique key buckets", len(e.locks.keyBuckets))
	}
}

func TestUniqueClaimSavepointCancellation(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, a, "SAVEPOINT m")
	sessionSQL(t, a, "INSERT INTO t VALUES(2)")
	sessionSQL(t, a, "ROLLBACK TO m")
	e.locks.mu.Lock()
	claims := 0
	for resource, entry := range e.locks.entries {
		if resource.key != nil && len(entry.holders) != 0 {
			claims++
		}
	}
	e.locks.mu.Unlock()
	if claims != 1 {
		t.Fatal("savepoint did not restore claim contributions", claims)
	}
	sessionSQL(t, b, "INSERT INTO t VALUES(2)")
	done, cancel := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	lockPending(t, e, 1)
	cancel()
	deleteAwait(t, done, 0, context.Canceled)
	lockPending(t, e, 0)
	transactionEnd(t, a, ar, true)
	btreeWantKeys(t, e, "u", "1", "2")
	uniqueClaimsEmpty(t, e)
}

func TestUniqueClaimDeadlock(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, b, "INSERT INTO t VALUES(2)")
	first, _ := uniqueAsyncUnit(t, a, "INSERT INTO t VALUES(2)")
	lockPending(t, e, 1)
	second, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	var victim deleteOutcome
	var survivor <-chan deleteOutcome
	select {
	case victim = <-first:
		survivor = second
	case victim = <-second:
		survivor = first
	case <-time.After(5 * time.Second):
		t.Fatal("unique cycle did not resolve")
	}
	var diagnostic *sqlstate.Error
	if !errors.As(victim.err, &diagnostic) || diagnostic.Code != "40P01" || diagnostic.Message != "deadlock detected" || diagnostic.Hint != "See server log for query details." || diagnostic.Position != "" || diagnostic.Context != "" || diagnostic.Schema != "" || diagnostic.Table != "" || !strings.Contains(diagnostic.Detail, "waits for UniqueKey on relation ") || !strings.Contains(diagnostic.Detail, " unique key") {
		t.Fatal(victim.err, diagnostic)
	}
	if victim.result.Count != 0 || victim.result.Rows != nil {
		t.Fatal("victim exposed effects", victim.result)
	}
	deleteAwait(t, survivor, 1, nil)
	transactionEnd(t, a, ar, false)
	transactionEnd(t, b, br, false)
	uniqueClaimsEmpty(t, e)
}

func TestUniqueRealRetainedTemporaryRemapping(t *testing.T) {
	for _, self := range []bool{false, true} {
		t.Run(map[bool]string{false: "foreign", true: "same_owner"}[self], func(t *testing.T) {
			ctx := context.Background()
			source := New()
			a := sessionStart(t, source, nil)
			sessionSQL(t, a, "CREATE SCHEMA portable CREATE TABLE t(k INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT)")
			sessionSQL(t, a, "INSERT INTO portable.t VALUES(7)")
			before := source.CatalogRead()
			original := before.state.descriptors[roleTableRef(t, source, "portable.t").Address].(*relationDescriptor)
			originalConstraint := original.uniqueConstraints[0]
			originalConstraint.Attributes = append([]int32(nil), originalConstraint.Attributes...)
			sourceRoot := source.indexRoots[originalConstraint.Index]
			var dump bytes.Buffer
			if err := source.Dump(ctx, &dump); err != nil {
				t.Fatal(err)
			}
			target := New()
			b := sessionStart(t, target, nil)
			if self {
				target, b = source, a
			}
			sessionSQL(t, b, "CREATE TEMP SEQUENCE kept CACHE 10")
			sequenceScalar(t, b, "SELECT nextval('kept')", 1)
			_, description := sequencePreparedDescription(t, target, b, "SELECT nextval('kept')")
			tempRef := description.sequences.constants[0].ref
			temp := target.state.descriptors[tempRef.Address]
			generator := target.sequences[tempRef.Address]
			memory := b.sequences[tempRef.Address]
			if !self && tempRef.Address.ObjectID != original.address.ObjectID {
				t.Fatal("fixture must produce a real retained TEMP address collision", tempRef.Address, original.address)
			}
			if _, err := target.ReplaceCatalog(ctx, before); err != nil {
				t.Fatal(err)
			}
			if target.state.descriptors[tempRef.Address] != temp || target.sequences[tempRef.Address] != generator || b.sequences[tempRef.Address] != memory {
				t.Fatal("retained TEMP identity changed")
			}
			parent := target.state.descriptors[roleTableRef(t, target, "portable.t").Address].(*relationDescriptor)
			index, root := btreeRoot(t, target, "portable.u")
			if parent.uniqueConstraints[0].Index != index.address || index.sqlIndex.parent != parent.address || index.sqlIndex.constraint.OID != parent.uniqueConstraints[0].OID || len(root.entries) != 1 || root.entries[0].row == 0 || root.entries[0].version == 0 {
				t.Fatal("broken imported closure")
			}
			if root == sourceRoot || self && root.entries[0].row == sourceRoot.entries[0].row {
				t.Fatal("replacement imported source root/row identities")
			}
			if !reflect.DeepEqual(original.uniqueConstraints[0], originalConstraint) {
				t.Fatal("source definition mutated")
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 2)
			var again bytes.Buffer
			if err := target.Dump(ctx, &again); err != nil || !bytes.Equal(dump.Bytes(), again.Bytes()) {
				t.Fatal("noncanonical remapped replay", err, again.String())
			}
			if err := target.Restore(ctx, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			sequenceScalar(t, b, "SELECT nextval('kept')", 3)
			result, err := testExecute(target, ctx, statement(t, "INSERT INTO portable.t VALUES(7)"), nil)
			if result.Count != 0 || result.Rows != nil {
				t.Fatal(result)
			}
			nullCountUnitDiagnostic(t, err, sqlstate.Error{Code: "23505", Message: `duplicate key value violates unique constraint "u"`, Detail: "Key (k)=(7) already exists.", Schema: "portable", Table: "t", Constraint: "u"})
			uniqueClaimsEmpty(t, target)
		})
	}
}

func TestUniquePrivateBaselineRootsAndBindingLifetime(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(extra INT,k INT CONSTRAINT u UNIQUE)")
	sessionSQL(t, a, "INSERT INTO t VALUES(0,1)")
	parentRef := roleTableRef(t, e, "t")
	original := e.state.descriptors[parentRef.Address].(*relationDescriptor)
	originalRoot := e.indexRoots[original.uniqueConstraints[0].Index]
	ar := transactionBegin(t, a)
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err := scope.gate(statement(t, "INSERT INTO t VALUES(0,2)")); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	first, err := scope.bindUniqueWrite(parentRef)
	if err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	again, err := scope.bindUniqueWrite(parentRef)
	if err != nil || first != again || first.keys[0].ordering.slots[0] != 1 {
		scope.Finish(err)
		op.Done()
		t.Fatal("immutable binding not reused", err)
	}
	if err := scope.Finish(nil); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	if scope.uniqueBinding != nil {
		t.Fatal("Finish retained statement binding")
	}
	sessionSQL(t, a, "ALTER TABLE t DROP extra")
	sessionSQL(t, a, "ALTER TABLE t ALTER k TYPE BIGINT")
	changes := a.transactions.catalog
	var rebuilt ObjectAddress
	var root *logicalIndexRoot
	for address, candidate := range changes.uniqueRoots {
		rebuilt, root = address, candidate
	}
	if root == nil || len(root.entries) != 1 || root == originalRoot {
		t.Fatal("missing private TYPE baseline root")
	}
	sessionSQL(t, a, "SAVEPOINT after_type")
	sessionSQL(t, a, "ALTER TABLE t ALTER k TYPE TEXT USING k::text")
	if a.transactions.catalog.uniqueRoots[rebuilt] != nil {
		t.Fatal("removed backing retained private baseline")
	}
	if len(root.entries) != 1 || btreeKeys(t, root)[0] != "1" {
		t.Fatal("retained private root mutated")
	}
	sessionSQL(t, a, "ROLLBACK TO after_type")
	if a.transactions.catalog.uniqueRoots[rebuilt] != root {
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
	if err := scope.gate(statement(t, "INSERT INTO t VALUES(3)")); err != nil {
		scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	next, err := scope.bindUniqueWrite(parentRef)
	if err != nil || next == first || next.keys[0].ordering.slots[0] != 0 {
		scope.Finish(err)
		op.Done()
		t.Fatal("descriptor-changing operation reused stale slots", err)
	}
	if err := scope.Finish(nil); err != nil {
		op.Done()
		t.Fatal(err)
	}
	op.Done()
	sessionSQL(t, a, "INSERT INTO t VALUES(3)")
	if a.transactions.catalog.uniqueRoots[rebuilt] != root || len(root.entries) != 1 {
		t.Fatal("DML rebuilt/modified baseline instead of overlaying effects")
	}
	transactionEnd(t, a, ar, false)
	if e.state.descriptors[parentRef.Address] != original || e.indexRoots[original.uniqueConstraints[0].Index] != originalRoot {
		t.Fatal("rollback changed committed closure")
	}
	uniqueClaimsEmpty(t, e)
}

func TestUniqueClaimStatementTimeout(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	sessionSQL(t, b, "SET statement_timeout='80ms'")
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
	transactionEnd(t, a, ar, false)
	uniqueClaimsEmpty(t, e)
}

func TestUniqueClaimEpochFenceReadmission(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, a, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	ar := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(1)")
	done, _ := uniqueAsyncUnit(t, b, "INSERT INTO t VALUES(1)")
	lockPending(t, e, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	deleteAwait(t, done, 0, ErrCatalogReplaced)
	transactionEnd(t, a, ar, false)
	uniqueClaimsEmpty(t, e)
	sessionSQL(t, b, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1)")
	btreeWantKeys(t, e, "u", "1")
	uniqueClaimsEmpty(t, e)
}

func TestUniqueKeyChangeTwoUpgradersEvaluateOnce(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	observer := sessionStart(t, e, nil)
	sessionSQL(t, observer, "CREATE SEQUENCE draws")
	sessionSQL(t, observer, "CREATE TABLE t(k INT CONSTRAINT u UNIQUE,v BIGINT)")
	sessionSQL(t, observer, "INSERT INTO t VALUES(1,0)")
	ar, br := transactionBegin(t, a), transactionBegin(t, b)
	transactionRows(t, a, "SELECT k FROM t FOR KEY SHARE", 1)
	transactionRows(t, b, "SELECT k FROM t FOR KEY SHARE", 1)
	first, _ := uniqueAsyncUnit(t, a, "UPDATE t SET k=2,v=nextval('draws') WHERE k=1")
	lockPending(t, e, 1)
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
	transactionEnd(t, a, ar, false)
	transactionEnd(t, b, br, false)
	sequenceScalar(t, observer, "SELECT last_value FROM draws", draws)
	btreeWantKeys(t, e, "u", "1")
	uniqueClaimsEmpty(t, e)
}
