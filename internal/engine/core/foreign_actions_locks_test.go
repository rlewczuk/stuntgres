package core_test

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func foreignActionLockFixture(t *testing.T, definition string) (*core.Engine, *session.Session, *session.Session, *session.Session, ids.ObjectRef, ids.RowID) {
	t.Helper()
	e := engine.New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE c(pid INT " + definition + ",note TEXT)", "INSERT INTO p VALUES(1)", "INSERT INTO c VALUES(1,'old')"} {
		sessionSQL(t, c, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
	return e, a, b, c, ref, e.Storage.Relations[ref.Address].Versions[0].Row
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
			done := enginetest.LockAsync(e, b, ctx, tc.q, false)
			joined, holderEnded, waiterEnded := false, false, false
			defer func() {
				cancel()
				if !holderEnded {
					transactionEnd(t, a, holder, false)
				}
				if !joined {
					enginetest.LockAwait(t, done, context.Canceled)
				}
				if !waiterEnded {
					transactionEnd(t, b, waiter, false)
				}
			}()
			foreignKeyQueuedMode(t, e, ref, row, a.Transactions.Ticket, tc.queued)
			b.Transactions.Mu.Lock()
			ticket := b.Transactions.Ticket
			b.Transactions.Mu.Unlock()
			foreignKeyHeldMode(t, e, ref, 0, ticket, lock.RowExclusive)
			// A real canonical queued request must not retain either metadata mutex.
			deadline := time.Now().Add(time.Second)
			for !e.Mu.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("action wait retained engine metadata")
				}
				runtime.Gosched()
			}
			e.Mu.Unlock()
			for !b.Transactions.Mu.TryLock() {
				if time.Now().After(deadline) {
					t.Fatal("action wait retained owner metadata")
				}
				runtime.Gosched()
			}
			b.Transactions.Mu.Unlock()
			transactionEnd(t, a, holder, true)
			holderEnded = true
			latest := e.Storage.Relations[ref.Address].Versions[0]
			enginetest.LockAwait(t, done, nil)
			joined = true
			foreignKeyHeldMode(t, e, ref, row, ticket, tc.held)
			b.Transactions.Mu.Lock()
			if tc.name == "delete" {
				root := b.Transactions.Deletes[ref.Address]
				if root == nil || root.Rows[row].Version != latest.Version || root.Rows[row].Storage != e.Storage.Relations[ref.Address] {
					b.Transactions.Mu.Unlock()
					t.Fatal("delete failed latest physical predecessor")
				}
			} else {
				writes := b.Transactions.Writes[ref.Address]
				if len(writes) != 1 || writes[0].Row != row || writes[0].Base != latest.Version {
					b.Transactions.Mu.Unlock()
					t.Fatal("action failed latest update lineage", writes, latest)
				}
				text, err := writes[0].Values[1].Text()
				if err != nil || text != "latest" {
					b.Transactions.Mu.Unlock()
					t.Fatal("action overwrote non-key payload", text, err)
				}
			}
			b.Transactions.Mu.Unlock()
			transactionEnd(t, b, waiter, false)
			waiterEnded = true
			enginetest.LockEmpty(t, e)
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
			done := enginetest.LockAsync(e, b, ctx, "DELETE FROM p", false)
			joined, ended := false, false
			defer func() {
				cancel()
				if !ended {
					transactionEnd(t, a, holder, false)
				}
				if !joined {
					enginetest.LockAwait(t, done, context.Canceled)
				}
			}()
			foreignKeyQueuedMode(t, e, ref, row, a.Transactions.Ticket, lock.ForUpdate)
			if replace {
				if err := engine.Reset(ctx, e); err != nil {
					t.Fatal(err)
				}
				enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
			} else {
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
			}
			joined = true
			transactionEnd(t, a, holder, false)
			ended = true
			if replace {
				sessionSQL(t, c, "CREATE TABLE p(id INT PRIMARY KEY)")
				sessionSQL(t, c, "CREATE TABLE c(pid INT REFERENCES p ON DELETE CASCADE,note TEXT)")
				next := enginetest.CatalogRef(t, e, e.CatalogRead(), "c")
				if next == ref || next.Address == ref.Address {
					t.Fatal("replacement revived child action authority")
				}
			} else {
				parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "p")
				if len(e.Storage.Relations[parent.Address].Versions) != 1 || len(e.Storage.Relations[ref.Address].Versions) != 1 || e.Storage.Relations[ref.Address].Ends != nil {
					t.Fatal("cancel published a partial action")
				}
			}
			enginetest.LockEmpty(t, e)
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
	if _, err = scope.Execute(enginetest.Statement(t, "DELETE FROM p"), 0, nil); err != nil {
		t.Fatal(err)
	}
	named := scope.LockedRef
	err = dml.SettleForeignActions(scope.WriteHost())
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "pid" of relation "c" violates not-null constraint`, Detail: "Failing row contains (null, old).", Schema: "public", Table: "c", Column: "pid", Context: `SQL statement "UPDATE ONLY "public"."c" SET "pid" = NULL WHERE $1 OPERATOR(pg_catalog.=) "pid""`}
	if !errors.As(err, &d) || *d != want {
		t.Fatal(d, err, want)
	}
	admission := core.ScopeForeignActionsOrZero(scope).Admissions[ref.Address]
	if admission == nil || admission.Ref != ref || admission.Tx != scope.TxRef || scope.LockedRef != named || len(scope.Stage.Rows[ref.Address]) != 0 || e.Storage.Relations[ref.Address].Versions[0].Row != row {
		t.Fatal("local report bypassed captured child authority")
	}
}

func TestForeignActionsPrepared(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "CREATE TABLE d(id INT,pid INT DEFAULT 1 REFERENCES p ON DELETE SET DEFAULT)", "INSERT INTO p VALUES(1),(2),(6),(7),(10)", "INSERT INTO d VALUES(11,10),(12,10)"} {
		sessionSQL(t, s, q)
	}
	probe := &defaultOrdinal{}
	defaultProbe(t, e, probe)
	ast, description := sequencePreparedDescription(t, e, s, "DELETE FROM p WHERE id=10")
	if probe.ordinal != 0 || core.DescriptionAnalysis(&description) == nil {
		t.Fatal("Prepare demanded an action/default", probe.ordinal, core.DescriptionAnalysis(&description))
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "d")
	originals := append([]storage.RowVersion(nil), e.Storage.Relations[ref.Address].Versions...)
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
	rows := e.Storage.Relations[ref.Address].Versions
	if len(rows) != 2 || rows[0].Row != originals[0].Row || rows[1].Row != originals[1].Row || rows[0].Version == originals[0].Version || rows[1].Version == originals[1].Version {
		t.Fatal("prepared action lost child lineage", rows, originals)
	}
}

func TestForeignActionsLocksTwoPhase(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e, a, b, c, ref, row := foreignActionLockFixture(t, "REFERENCES p ON DELETE CASCADE")
			origin := transactionBegin(t, a)
			ticket := a.Transactions.Ticket
			sessionSQL(t, a, "DELETE FROM p")
			foreignKeyHeldMode(t, e, ref, 0, ticket, lock.RowExclusive)
			foreignKeyHeldMode(t, e, ref, row, ticket, lock.ForUpdate)
			sessionSQL(t, a, "PREPARE TRANSACTION 'action_unit'")
			prepared := core.EnginePreparedRef(e).Lookup("action_unit")
			if prepared == nil || prepared.Ref != origin || prepared.Ticket != ticket || a.Transactions.Ticket != nil || prepared.Deletes[ref.Address] == nil {
				t.Fatal("action deletion/grants did not transfer to prepared owner")
			}
			foreignKeyHeldMode(t, e, ref, row, ticket, lock.ForUpdate)
			waiter := transactionBegin(t, b)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := enginetest.LockAsync(e, b, ctx, "UPDATE c SET note='peer'", false)
			joined, completed, waiterEnded := false, false, false
			defer func() {
				cancel()
				if !completed {
					sessionSQL(t, c, "ROLLBACK PREPARED 'action_unit'")
				}
				if !joined {
					enginetest.LockAwait(t, done, context.Canceled)
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
			enginetest.LockAwait(t, done, nil)
			joined = true
			transactionEnd(t, b, waiter, true)
			waiterEnded = true
			if core.EnginePreparedRef(e).Len() != 0 {
				t.Fatal("completed action prepared owner retained")
			}
			storage := e.Storage.Relations[ref.Address]
			if commit {
				if storage.Ends == nil {
					t.Fatal("prepared commit lost child deletion")
				}
			} else {
				if storage.Ends != nil || len(storage.Versions) != 1 {
					t.Fatal("prepared rollback left child deletion")
				}
				value, err := storage.Versions[0].Values[1].Text()
				if err != nil || value != "peer" {
					t.Fatal("prepared rollback failed peer latest mutation", value, err)
				}
			}
			enginetest.LockEmpty(t, e)
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
			next.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("fa_owner")
			next.ObjectRevision++
			stage := e.BeginCatalogStage(view)
			if err := stage.Replace(&next); err != nil {
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: view.Epoch()}); err != nil {
				t.Fatal(err)
			}
			if force {
				sessionSQL(t, creator, "ALTER TABLE c FORCE ROW LEVEL SECURITY")
			}
			sessionSQL(t, a, "SET ROLE fa_actor")
			scope := foreignActionUnitScope(t, e, a, "DELETE FROM p")
			original := scope.Identity.Current
			err = dml.SettleForeignActions(scope.WriteHost())
			var d *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23502", Message: `null value in column "pid" of relation "c" violates not-null constraint`, Detail: "Failing row contains (null, old).", Schema: "public", Table: "c", Column: "pid", Context: `SQL statement "UPDATE ONLY "public"."c" SET "pid" = NULL WHERE $1 OPERATOR(pg_catalog.=) "pid""`}
			if !errors.As(err, &d) || *d != want {
				t.Fatal(d, err, want)
			}
			if scope.ForeignActionAuth != nil || !sqltype.SameDatum(scope.Identity.Current, original) {
				t.Fatal("child-owner reporting changed invoking identity")
			}
			current, err := scope.Identity.Current.Text()
			if err != nil || current != "fa_actor" {
				t.Fatal("lost initiating actor identity", current, err)
			}
			if force {
				// The public Nested witness owns SQL diagnostics. These checks
				// distinguish the private admitted RI context from an arbitrary
				// acting-authority marker without changing initiating identity.
				e.Mu.Lock()
				auth, authErr := core.ScopeWriteAuthorizationLocked(scope, e)
				e.Mu.Unlock()
				if authErr != nil {
					t.Fatal(authErr)
				}
				admission := core.ScopeForeignActionsOrZero(scope).Admissions[childRef.Address]
				if admission == nil || !admission.Writable {
					t.Fatal("missing actual writable child admission")
				}
				binding := analyze.Binding{Ref: childRef, Relation: admission.Relation}
				row := admission.Storage.Versions[0].Values
				for _, tc := range []struct {
					name                                         string
					settling, writable, owner, viewCheck, detail bool
				}{
					{name: "marker_only", writable: true, owner: true},
					{name: "read_only", settling: true, owner: true},
					{name: "nonowner", settling: true, writable: true},
					{name: "view_check", settling: true, writable: true, owner: true, viewCheck: true},
					{name: "admitted_owner", settling: true, writable: true, owner: true, detail: true},
				} {
					t.Run(tc.name, func(t *testing.T) {
						acting := &auth
						if tc.owner {
							acting = core.ForeignAuthorizationOf(auth.ActingAs(admission.Relation.Owner))
						}
						previous := scope.WriteHost().SwapForeignActionAuthority(acting)
						wasWritable := admission.Writable
						admission.Writable = tc.writable
						core.ScopeForeignActions(scope).Settling = tc.settling
						defer func() {
							scope.WriteHost().SwapForeignActionAuthority(previous)
							core.ScopeForeignActions(scope).Settling = false
							admission.Writable = wasWritable
						}()
						detail, err := core.EngineRowDiagnosticDetail(e, scope, &binding, row, tc.viewCheck)
						if err != nil || (detail != "") != tc.detail {
							t.Fatal("RI reporting authority", detail, err)
						}
						if !sqltype.SameDatum(scope.Identity.Current, original) {
							t.Fatal("reporting changed initiating identity")
						}
					})
				}
			}
		})
	}
}

func TestForeignActionsLocksSnapshot(t *testing.T) {
	for _, isolation := range []session.Isolation{session.RepeatableRead, session.Serializable} {
		t.Run(map[session.Isolation]string{session.RepeatableRead: "repeatable", session.Serializable: "serializable"}[isolation], func(t *testing.T) {
			e, holder, waiter, creator, child, _ := foreignActionLockFixture(t, "REFERENCES p ON DELETE CASCADE")
			sessionSQL(t, creator, "INSERT INTO p VALUES(2)")
			op := transactionOperation(t, waiter)
			ref, err := e.Begin(op, session.TxOptions{Isolation: isolation})
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
			done := enginetest.LockAsync(e, waiter, ctx, "DELETE FROM p WHERE id=1", false)
			joined := false
			defer func() {
				cancel()
				if !joined {
					enginetest.LockAwait(t, done, context.DeadlineExceeded)
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
			parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "p")
			if e.Storage.Relations[parent.Address].Ends != nil || e.Storage.Relations[child.Address].Ends != nil {
				t.Fatal("failed stable action published a deletion")
			}
			transactionRows(t, creator, "SELECT pid FROM c", 2)
			enginetest.LockEmpty(t, e)
		})
	}
}

// A closed, bounded test-only producer witnesses actual stored row values;
// there is no production callback, function registry or new SQL capability.
type defaultOrdinal struct {
	expr.Info
	ordinal int64
	failAt  int64
	cancel  context.CancelFunc
}

func (e *defaultOrdinal) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := exec.EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	e.ordinal++
	if e.ordinal == e.failAt {
		if e.cancel != nil {
			e.cancel()
			return sqltype.Value{}, c.Context.Err()
		}
		return sqltype.Value{}, core.SQLError("22003", "integer out of range")
	}
	return sqltype.Integer(e.Usage.Type, e.ordinal)
}

func defaultProbe(t *testing.T, e *core.Engine, probe *defaultOrdinal) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for address, descriptor := range e.Catalog.State.Descriptors() {
		if old, ok := descriptor.(*catalog.Relation); ok && old.Name() == "d" {
			next := *old
			probe.Info = expr.Info{Usage: old.Columns[1].Usage}
			next.ColumnMeta.Defaults = map[int32]catalog.ColumnDefault{old.Attributes[1]: {Expression: probe}}
			// Private descriptor fixture only; published SQL defaults still require the
			// provider's closed-node ownership/serialization validation.
			state := catalog.CloneState(e.Catalog.State)
			state.SetDescriptor(address, &next)
			e.Catalog.PublishLocked(state)
			return
		}
	}
	t.Fatal("missing probe relation")
}
