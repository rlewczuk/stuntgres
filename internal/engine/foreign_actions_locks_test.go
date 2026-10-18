package engine

import (
	"context"
	"errors"
	"runtime"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func foreignActionLockFixture(t *testing.T, definition string) (*Engine, *Session, *Session, *Session, ids.ObjectRef, ids.RowID) {
	t.Helper()
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT " + definition + ",note TEXT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1,'old')"} {
		sessionSQL(t, c, q)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "c")
	return e, a, b, c, ref, e.storage[ref.Address].versions[0].row
}

func TestForeignActionsLocks(t *testing.T) {
	for _, tc := range []struct {
		name, definition, q string
		queued, held        lock.Mode
	}{
		{"delete", "REFERENCES p ON DELETE CASCADE", "DELETE FROM p", lock.ForUpdate, lock.ForUpdate},
		{"key-update", "PRIMARY KEY REFERENCES p ON UPDATE CASCADE", "UPDATE p SET id=2", lock.ForNoKeyUpdate, lock.ForNoKeyUpdate | lock.ForUpdate},
		{"non-key-set", "REFERENCES p ON DELETE SET NULL", "DELETE FROM p", lock.ForNoKeyUpdate, lock.ForNoKeyUpdate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, a, b, _, ref, row := foreignActionLockFixture(t, tc.definition)
			holder, waiter := transactionBegin(t, a), transactionBegin(t, b)
			sessionSQL(t, a, "UPDATE c SET note='latest'")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := lockAsync(e, b, ctx, tc.q, false)
			joined, holderEnded, waiterEnded := false, false, false
			defer func() {
				cancel()
				if !holderEnded {
					transactionEnd(t, a, holder, false)
				}
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
				if !waiterEnded {
					transactionEnd(t, b, waiter, false)
				}
			}()
			foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, tc.queued)
			b.transactions.mu.Lock()
			ticket := b.transactions.ticket
			b.transactions.mu.Unlock()
			foreignKeyHeldMode(t, e, ref, 0, ticket, lock.RowExclusive)
			// A real canonical queued request must not retain either metadata mutex.
			deadline := time.Now().Add(time.Second)
			for !e.mu.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("action wait retained engine metadata")
				}
				runtime.Gosched()
			}
			e.mu.Unlock()
			for !b.transactions.mu.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("action wait retained owner metadata")
				}
				runtime.Gosched()
			}
			b.transactions.mu.Unlock()
			transactionEnd(t, a, holder, true)
			holderEnded = true
			latest := e.storage[ref.Address].versions[0]
			lockAwait(t, done, nil)
			joined = true
			foreignKeyHeldMode(t, e, ref, row, ticket, tc.held)
			b.transactions.mu.Lock()
			if tc.name == "delete" {
				root := b.transactions.deletes[ref.Address]
				if root == nil || root.rows[row].version != latest.version || root.rows[row].storage != e.storage[ref.Address] {
					b.transactions.mu.Unlock()
					t.Fatal("delete failed latest physical predecessor")
				}
			} else {
				writes := b.transactions.writes[ref.Address]
				if len(writes) != 1 || writes[0].row != row || writes[0].base != latest.version {
					b.transactions.mu.Unlock()
					t.Fatal("action failed latest update lineage", writes, latest)
				}
				text, err := writes[0].values[1].Text()
				if err != nil || text != "latest" {
					b.transactions.mu.Unlock()
					t.Fatal("action overwrote non-key payload", text, err)
				}
			}
			b.transactions.mu.Unlock()
			transactionEnd(t, b, waiter, false)
			waiterEnded = true
			lockEmpty(t, e)
		})
	}
}

func TestForeignActionsCancellation(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replacement"}[replace], func(t *testing.T) {
			e, a, b, c, ref, row := foreignActionLockFixture(t, "REFERENCES p ON DELETE CASCADE")
			holder := transactionBegin(t, a)
			sessionSQL(t, a, "UPDATE c SET note='held'")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := lockAsync(e, b, ctx, "DELETE FROM p", false)
			joined, ended := false, false
			defer func() {
				cancel()
				if !ended {
					transactionEnd(t, a, holder, false)
				}
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, lock.ForUpdate)
			if replace {
				if err := e.Reset(ctx); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ErrCatalogReplaced)
			} else {
				cancel()
				lockAwait(t, done, context.Canceled)
			}
			joined = true
			transactionEnd(t, a, holder, false)
			ended = true
			if replace {
				sessionSQL(t, c, "CREATE TABLE p(id INT PRIMARY KEY)")
				sessionSQL(t, c, "CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE,note TEXT)")
				next := catalogRef(t, e, e.CatalogRead(), "c")
				if next == ref || next.Address == ref.Address {
					t.Fatal("replacement revived child action authority")
				}
			} else {
				parent := catalogRef(t, e, e.CatalogRead(), "p")
				if len(e.storage[parent.Address].versions) != 1 || len(e.storage[ref.Address].versions) != 1 || e.storage[ref.Address].ends != nil {
					t.Fatal("cancel published a partial action")
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestForeignActionsDiagnostics(t *testing.T) {
	e, a, _, _, ref, row := foreignActionLockFixture(t, "NOT NULL REFERENCES p ON DELETE SET NULL")
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	if _, err = scope.Execute(statement(t, "DELETE FROM p"), 0, nil); err != nil {
		t.Fatal(err)
	}
	named := scope.lockedRef
	err = scope.settleForeignActions()
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "pid" of relation "c" violates not-null constraint`, Detail: "Failing row contains (null, old).", Schema: "public", Table: "c", Column: "pid", Context: `SQL statement "UPDATE ONLY "public"."c" SET "pid" = NULL WHERE $1 OPERATOR(pg_catalog.=) "pid""`}
	if !errors.As(err, &d) || *d != want {
		t.Fatal(d, err, want)
	}
	admission := scope.foreignActionAdmissions[ref.Address]
	if admission == nil || admission.ref != ref || admission.tx != scope.txRef || scope.lockedRef != named || len(scope.stage.rows[ref.Address]) != 0 || e.storage[ref.Address].versions[0].row != row {
		t.Fatal("local report bypassed captured child authority")
	}
}

func TestForeignActionsPrepared(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE d(id INT,pid INT DEFAULT 1 REFERENCES p ON DELETE SET DEFAULT)", "INSERT INTO p VALUES(1),(2),(6),(7),(10)", "INSERT INTO d VALUES(11,10),(12,10)"} {
		sessionSQL(t, s, q)
	}
	probe := &defaultOrdinal{}
	defaultProbe(t, e, probe)
	ast, description := sequencePreparedDescription(t, e, s, "DELETE FROM p WHERE id=10")
	if probe.ordinal != 0 || description.analysis == nil {
		t.Fatal("Prepare demanded an action/default", probe.ordinal, description.analysis)
	}
	ref := catalogRef(t, e, e.CatalogRead(), "d")
	originals := append([]rowVersion(nil), e.storage[ref.Address].versions...)
	// Replace the current evaluator at the established private default seam. The
	// retained initiating analysis must never own an executable child plan/value.
	current := &defaultOrdinal{ordinal: 5}
	defaultProbe(t, e, current)
	result, err := sequencePreparedExecute(e, s, ast, &description)
	if err != nil || result.Count != 1 {
		t.Fatal(result, err)
	}
	if probe.ordinal != 0 || current.ordinal != 7 {
		t.Fatal("Execute used a captured default", probe.ordinal, current.ordinal)
	}
	rows := e.storage[ref.Address].versions
	if len(rows) != 2 || rows[0].row != originals[0].row || rows[1].row != originals[1].row || rows[0].version == originals[0].version || rows[1].version == originals[1].version {
		t.Fatal("prepared action lost child lineage", rows, originals)
	}
}

func TestForeignActionsLocksTwoPhase(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e, a, b, c, ref, row := foreignActionLockFixture(t, "REFERENCES p ON DELETE CASCADE")
			origin := transactionBegin(t, a)
			ticket := a.transactions.ticket
			sessionSQL(t, a, "DELETE FROM p")
			foreignKeyHeldMode(t, e, ref, 0, ticket, lock.RowExclusive)
			foreignKeyHeldMode(t, e, ref, row, ticket, lock.ForUpdate)
			sessionSQL(t, a, "PREPARE TRANSACTION 'action_unit'")
			prepared := e.prepared["action_unit"]
			if prepared == nil || prepared.ref != origin || prepared.ticket != ticket || a.transactions.ticket != nil || prepared.deletes[ref.Address] == nil {
				t.Fatal("action deletion/grants did not transfer to prepared owner")
			}
			foreignKeyHeldMode(t, e, ref, row, ticket, lock.ForUpdate)
			waiter := transactionBegin(t, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := lockAsync(e, b, ctx, "UPDATE c SET note='peer'", false)
			joined, completed, waiterEnded := false, false, false
			defer func() {
				cancel()
				if !completed {
					sessionSQL(t, c, "ROLLBACK PREPARED 'action_unit'")
				}
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
				if !waiterEnded {
					transactionEnd(t, b, waiter, false)
				}
			}()
			foreignKeyQueuedMode(t, e, ref, row, ticket, lock.ForNoKeyUpdate)
			command := "ROLLBACK PREPARED 'action_unit'"
			if commit {
				command = "COMMIT PREPARED 'action_unit'"
			}
			sessionSQL(t, c, command)
			completed = true
			lockAwait(t, done, nil)
			joined = true
			transactionEnd(t, b, waiter, true)
			waiterEnded = true
			if len(e.prepared) != 0 {
				t.Fatal("completed action prepared owner retained")
			}
			storage := e.storage[ref.Address]
			if commit {
				if storage.ends == nil {
					t.Fatal("prepared commit lost child deletion")
				}
			} else {
				if storage.ends != nil || len(storage.versions) != 1 {
					t.Fatal("prepared rollback left child deletion")
				}
				value, err := storage.versions[0].values[1].Text()
				if err != nil || value != "peer" {
					t.Fatal("prepared rollback failed peer latest mutation", value, err)
				}
			}
			lockEmpty(t, e)
		})
	}
}

func TestForeignActionsDiagnosticsOwner(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(map[bool]string{false: "unforced", true: "forced"}[force], func(t *testing.T) {
			e, a, _, creator, childRef, _ := foreignActionLockFixture(t, "NOT NULL REFERENCES p ON DELETE SET NULL")
			for _, q := range []string{"CREATE ROLE fa_owner", "CREATE ROLE fa_actor", "ALTER TABLE c ENABLE ROW LEVEL SECURITY", "GRANT DELETE ON p TO fa_actor"} {
				sessionSQL(t, creator, q)
			}
			// Use the incumbent validated immutable owner stage, not excluded
			// public ALTER TABLE OWNER syntax or an authorization bypass callback.
			view := e.CatalogRead()
			next := *view.MustRelation(childRef.Address)
			next.owner = e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("fa_owner")
			next.revision++
			stage := e.BeginCatalogStage(view)
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: view.Epoch()}); err != nil {
				t.Fatal(err)
			}
			if force {
				sessionSQL(t, creator, "ALTER TABLE c FORCE ROW LEVEL SECURITY")
			}
			sessionSQL(t, a, "SET ROLE fa_actor")
			scope := foreignActionUnitScope(t, e, a, "DELETE FROM p")
			original := scope.identity.current
			err = scope.settleForeignActions()
			var d *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "pid" of relation "c" violates not-null constraint`, Detail: "Failing row contains (null, old).", Schema: "public", Table: "c", Column: "pid", Context: `SQL statement "UPDATE ONLY "public"."c" SET "pid" = NULL WHERE $1 OPERATOR(pg_catalog.=) "pid""`}
			if !errors.As(err, &d) || *d != want {
				t.Fatal(d, err, want)
			}
			if scope.foreignActionAuth != nil || !sqltype.SameDatum(scope.identity.current, original) {
				t.Fatal("child-owner reporting changed invoking identity")
			}
			current, err := scope.identity.current.Text()
			if err != nil || current != "fa_actor" {
				t.Fatal("lost initiating actor identity", current, err)
			}
		})
	}
}

func TestForeignActionsLocksSnapshot(t *testing.T) {
	for _, isolation := range []Isolation{RepeatableRead, Serializable} {
		t.Run(map[Isolation]string{RepeatableRead: "repeatable", Serializable: "serializable"}[isolation], func(t *testing.T) {
			e, holder, waiter, creator, child, _ := foreignActionLockFixture(t, "REFERENCES p ON DELETE CASCADE")
			sessionSQL(t, creator, "INSERT INTO p VALUES(2)")
			op := transactionOperation(t, waiter)
			ref, err := e.Begin(op, TxOptions{Isolation: isolation})
			op.Done()
			if err != nil {
				t.Fatal(err)
			}
			ended := false
			defer func() {
				if !ended {
					transactionEnd(t, waiter, ref, false)
				}
			}()
			transactionRows(t, waiter, "SELECT pid FROM c", 1)
			sessionSQL(t, holder, "UPDATE c SET pid=2")
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			done := lockAsync(e, waiter, ctx, "DELETE FROM p WHERE id=1", false)
			joined := false
			defer func() {
				cancel()
				if !joined {
					lockAwait(t, done, context.DeadlineExceeded)
				}
			}()
			select {
			case err = <-done:
				joined = true
			case <-ctx.Done():
				t.Fatal("snapshot action did not terminate", ctx.Err())
			}
			var d *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "40001", Message: "could not serialize access due to concurrent update", Context: `SQL statement "DELETE FROM ONLY "public"."c" WHERE $1 OPERATOR(pg_catalog.=) "pid""`}
			if !errors.As(err, &d) || *d != want {
				t.Fatal(d, err, want)
			}
			transactionEnd(t, waiter, ref, false)
			ended = true
			parent := catalogRef(t, e, e.CatalogRead(), "p")
			if e.storage[parent.Address].ends != nil || e.storage[child.Address].ends != nil {
				t.Fatal("failed stable action published a deletion")
			}
			transactionRows(t, creator, "SELECT pid FROM c", 2)
			lockEmpty(t, e)
		})
	}
}
