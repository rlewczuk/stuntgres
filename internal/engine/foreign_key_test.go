package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"stuntgres/internal/sqlstate"
	"testing"
	"time"
)

func foreignKeyLockFixture(t *testing.T) (*Engine, *Session, *Session, *Session, ObjectRef, RowID) {
	t.Helper()
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	sessionSQL(t, c, "CREATE TABLE p(id INT PRIMARY KEY,note INT)")
	sessionSQL(t, c, "CREATE TABLE c(pid INT REFERENCES p)")
	sessionSQL(t, c, "INSERT INTO p VALUES(1,10)")
	ref := catalogRef(t, e, e.CatalogRead(), "p")
	e.mu.Lock()
	row := e.storage[ref.Address].versions[0].row
	e.mu.Unlock()
	return e, a, b, c, ref, row
}
func foreignKeyHeldMode(t *testing.T, e *Engine, ref ObjectRef, row RowID, ticket *relationTicket, want relationLockMode) {
	t.Helper()
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ref, row: row}]
	var actual relationLockMode
	if entry != nil {
		actual = entry.holders[ticket].mode
	}
	e.locks.mu.Unlock()
	if actual != want {
		t.Fatal("exact canonical parent row grant", ref, row, actual, want)
	}
}
func foreignKeyQueuedMode(t *testing.T, e *Engine, ref ObjectRef, row RowID, holder *relationTicket, want relationLockMode) {
	t.Helper()
	rowLockQueued(t, e, holder, want)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ref, row: row}]
	found := false
	if entry != nil {
		for _, request := range entry.pending {
			found = found || !request.terminal && request.mode == want && request.ticket != holder
		}
	}
	e.locks.mu.Unlock()
	if !found {
		t.Fatal("request queued on another/fabricated relation or row", ref, row, want)
	}
}
func foreignKeyAwaitCode(t *testing.T, done <-chan error, code string) {
	t.Helper()
	select {
	case err := <-done:
		if code == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != code {
			t.Fatal(err, code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FK operation did not terminate")
	}
}

func TestForeignKeyCanonicalRowAdmission(t *testing.T) {
	for _, parentFirst := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "child-first", true: "parent-first"}[parentFirst]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				e, a, b, c, ref, row := foreignKeyLockFixture(t)
				tx := transactionBegin(t, a)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var done <-chan error
				joined := false
				ended := false
				defer func() {
					cancel()
					if !ended {
						transactionEnd(t, a, tx, false)
					}
					if done != nil && !joined {
						lockAwait(t, done, context.Canceled)
					}
				}()
				if parentFirst {
					sessionSQL(t, a, "UPDATE p SET id=2 WHERE id=1")
					foreignKeyHeldMode(t, e, ref, row, a.transactions.ticket, noKeyUpdate|rowUpdate)
					statement(t, "INSERT INTO c VALUES(1)")
					done = lockAsync(e, b, ctx, "INSERT INTO c VALUES(1)", false)
					foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, keyShare)
				} else {
					sessionSQL(t, a, "INSERT INTO c VALUES(1)")
					foreignKeyHeldMode(t, e, ref, row, a.transactions.ticket, keyShare)
					other := transactionBegin(t, b)
					sessionSQL(t, b, "UPDATE p SET note=11 WHERE id=1")
					foreignKeyHeldMode(t, e, ref, row, b.transactions.ticket, noKeyUpdate)
					foreignKeyHeldMode(t, e, ref, row, a.transactions.ticket, keyShare)
					transactionEnd(t, b, other, true)
					statement(t, "UPDATE p SET id=2 WHERE id=1")
					done = lockAsync(e, b, ctx, "UPDATE p SET id=2 WHERE id=1", false)
					foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, rowUpdate)
				}
				transactionEnd(t, a, tx, commit)
				ended = true
				code := ""
				if commit {
					code = "23503"
				}
				foreignKeyAwaitCode(t, done, code)
				joined = true
				if parentFirst {
					if commit {
						transactionRows(t, c, "SELECT id FROM p", 2)
						transactionRows(t, c, "SELECT pid FROM c")
					} else {
						transactionRows(t, c, "SELECT id FROM p", 1)
						transactionRows(t, c, "SELECT pid FROM c", 1)
					}
				} else {
					if commit {
						transactionRows(t, c, "SELECT id FROM p", 1)
						transactionRows(t, c, "SELECT pid FROM c", 1)
					} else {
						transactionRows(t, c, "SELECT id FROM p", 2)
						transactionRows(t, c, "SELECT pid FROM c")
					}
					transactionRows(t, c, "SELECT note FROM p", 11)
				}
				lockEmpty(t, e)
			})
		}
	}
}

func TestForeignKeyQueuedCancellationAndReplacement(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "replacement"}[replace], func(t *testing.T) {
			e, a, b, c, ref, row := foreignKeyLockFixture(t)
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "DELETE FROM p WHERE id=1")
			foreignKeyHeldMode(t, e, ref, row, a.transactions.ticket, rowUpdate)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			statement(t, "INSERT INTO c VALUES(1)")
			done := lockAsync(e, b, ctx, "INSERT INTO c VALUES(1)", false)
			joined, ended := false, false
			defer func() {
				cancel()
				if !ended {
					transactionEnd(t, a, tx, false)
				}
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, keyShare)
			if replace {
				if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
				lockAwait(t, done, ErrCatalogReplaced)
			} else {
				cancel()
				lockAwait(t, done, context.Canceled)
			}
			joined = true
			transactionEnd(t, a, tx, false)
			ended = true
			if replace {
				sessionSQL(t, c, "CREATE TABLE p(id INT PRIMARY KEY,note INT)")
				sessionSQL(t, c, "CREATE TABLE c(pid INT REFERENCES p)")
				sessionSQL(t, c, "INSERT INTO p VALUES(1,10)")
				next := catalogRef(t, e, e.CatalogRead(), "p")
				if next == ref || next.Address == ref.Address {
					t.Fatal("recreated name revived old authority", next, ref)
				}
			}
			sessionSQL(t, b, "INSERT INTO c VALUES(1)")
			transactionRows(t, c, "SELECT pid FROM c", 1)
			transactionRows(t, c, "SELECT id FROM p", 1)
			lockEmpty(t, e)
		})
	}
}

func TestForeignKeySavepointGrantRestoration(t *testing.T) {
	e, a, _, c, ref, row := foreignKeyLockFixture(t)
	sessionSQL(t, c, "INSERT INTO p VALUES(2,20)")
	e.mu.Lock()
	second := e.storage[ref.Address].versions[1].row
	e.mu.Unlock()
	tx := transactionBegin(t, a)
	defer transactionEnd(t, a, tx, false)
	sessionSQL(t, a, "INSERT INTO c VALUES(1)")
	ticket := a.transactions.ticket
	sessionSQL(t, a, "SAVEPOINT mark")
	sessionSQL(t, a, "INSERT INTO c VALUES(2)")
	op, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "INSERT INTO c VALUES(999)"), 0, nil)
	op.Done()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "23503" {
		t.Fatal(err)
	}
	foreignKeyHeldMode(t, e, ref, row, ticket, keyShare)
	foreignKeyHeldMode(t, e, ref, second, ticket, 0)
	sessionSQL(t, a, "ROLLBACK TO mark")
	transactionRows(t, a, "SELECT pid FROM c", 1)
	sessionSQL(t, a, "INSERT INTO c VALUES(2)")
	foreignKeyHeldMode(t, e, ref, second, ticket, keyShare)
}

func TestForeignKeyDefinitionAuthority(t *testing.T) {
	e, _, _, c, ref, _ := foreignKeyLockFixture(t)
	sessionSQL(t, c, "CREATE UNIQUE INDEX wrong_key ON p(note)")
	view := e.CatalogRead()
	child := view.state.descriptors[catalogRef(t, e, view, "c").Address].(*relationDescriptor)
	original := child.foreignKeys[0]
	for _, change := range []func(*foreignKeyConstraint){func(f *foreignKeyConstraint) { f.OID = 0 }, func(f *foreignKeyConstraint) { f.Target.ObjectID = 0 }, func(f *foreignKeyConstraint) { f.Referenced = []int32{99} }, func(f *foreignKeyConstraint) { f.Index = catalogRef(t, e, view, "wrong_key").Address }, func(f *foreignKeyConstraint) { f.Attributes = []int32{99} }} {
		copy := *child
		copy.foreignKeys = slices.Clone(child.foreignKeys)
		change(&copy.foreignKeys[0])
		if err := validateForeignKeys(view, &copy, map[string]bool{}); err == nil {
			t.Fatal("invalid attachment admitted", copy.foreignKeys[0])
		}
	}
	index := view.state.descriptors[original.Index].(*relationDescriptor)
	for _, phase := range []string{"nonunique", "nonready", "invalid", "notlive"} {
		state := cloneCatalog(view.state)
		copy := *index
		definition := *index.sqlIndex
		switch phase {
		case "nonunique":
			definition.unique = false
		case "nonready":
			definition.ready = false
		case "invalid":
			definition.valid = false
		case "notlive":
			definition.live = false
		}
		copy.sqlIndex = &definition
		state.descriptors[index.address] = &copy
		if err := validateForeignKeys(CatalogRead{e, state}, child, map[string]bool{}); err == nil {
			t.Fatal("invalid selected index authority", phase)
		}
	}
	if !reflect.DeepEqual(child.foreignKeys[0], original) || child.foreignKeys[0].Target != ref.Address {
		t.Fatal("validation mutated source attachment")
	}
}

func TestForeignKeyTypeIdentityIsolation(t *testing.T) {
	e, _, _, c, ref, _ := foreignKeyLockFixture(t)
	view := e.CatalogRead()
	childAddress := catalogRef(t, e, view, "c").Address
	old := view.state.descriptors[childAddress].(*relationDescriptor)
	definition := old.foreignKeys[0]
	parent := view.state.descriptors[ref.Address].(*relationDescriptor)
	index := view.state.descriptors[definition.Index].(*relationDescriptor)
	sessionSQL(t, c, "ALTER TABLE p ALTER id TYPE BIGINT")
	next := e.CatalogRead().state.descriptors[childAddress].(*relationDescriptor)
	if next.address != old.address || next.foreignKeys[0].OID == definition.OID || next.foreignKeys[0].Index == definition.Index || next.foreignKeys[0].Target != definition.Target || !slices.Equal(next.foreignKeys[0].Attributes, definition.Attributes) || !slices.Equal(next.foreignKeys[0].Referenced, definition.Referenced) {
		t.Fatal("TYPE did not rebuild exact attachment", definition, next.foreignKeys[0])
	}
	if !reflect.DeepEqual(old.foreignKeys[0], definition) || view.state.descriptors[ref.Address] != parent || view.state.descriptors[index.address] != index {
		t.Fatal("TYPE changed retained catalog")
	}
	sessionSQL(t, c, "INSERT INTO c VALUES(1)")
	transactionRows(t, c, "SELECT pid FROM c", 1)
}

func TestForeignKeyPrivateFinalImage(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE self(id INT PRIMARY KEY,pid INT REFERENCES self)")
	sessionSQL(t, s, "INSERT INTO self VALUES(1,NULL)")
	tx := transactionBegin(t, s)
	ended := false
	defer func() {
		if !ended {
			transactionEnd(t, s, tx, false)
		}
	}()
	if result := sessionSQL(t, s, "INSERT INTO self VALUES(2,1)"); result.Count != 1 {
		t.Fatal(result.Count)
	}
	if result := sessionSQL(t, s, "INSERT INTO self VALUES(3,4),(4,2)"); result.Count != 2 {
		t.Fatal(result.Count)
	}
	sessionSQL(t, s, "SAVEPOINT mark")
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Execute(op, statement(t, "UPDATE self SET id=5 WHERE id=2"), 0, nil)
	op.Done()
	var d *sqlstate.Error
	if !errors.As(err, &d) || d.Code != "23503" {
		t.Fatal(err)
	}
	sessionSQL(t, s, "ROLLBACK TO mark")
	transactionRows(t, s, "SELECT id FROM self ORDER BY id", 1, 2, 3, 4)
	if result := sessionSQL(t, s, "DELETE FROM self WHERE id>2"); result.Count != 2 {
		t.Fatal(result.Count)
	}
	if result := sessionSQL(t, s, "UPDATE self SET id=5 WHERE id=2"); result.Count != 1 {
		t.Fatal(result.Count)
	}
	transactionRows(t, s, "SELECT id FROM self ORDER BY id", 1, 5)
	if result := sessionSQL(t, s, "DELETE FROM self"); result.Count != 2 {
		t.Fatal(result.Count)
	}
	transactionRows(t, s, "SELECT id FROM self")
	transactionEnd(t, s, tx, false)
	ended = true
	transactionRows(t, s, "SELECT id FROM self", 1)
	lockEmpty(t, e)
}

func TestForeignKeyCreationIdentityAfterWait(t *testing.T) {
	e, a, b, c, ref, _ := foreignKeyLockFixture(t)
	sessionSQL(t, c, "CREATE SCHEMA shadow")
	sessionSQL(t, b, "SET search_path TO shadow,public")
	tx := transactionBegin(t, a)
	sessionSQL(t, a, "LOCK public.p IN ACCESS EXCLUSIVE MODE")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	statement(t, "CREATE TABLE c(pid INT REFERENCES p)")
	done := lockAsync(e, b, ctx, "CREATE TABLE c(pid INT REFERENCES p)", false)
	joined, ended := false, false
	defer func() {
		cancel()
		if !ended {
			transactionEnd(t, a, tx, false)
		}
		if !joined {
			lockAwait(t, done, context.Canceled)
		}
	}()
	lockPending(t, e, 1)
	e.locks.mu.Lock()
	entry := e.locks.entries[lockResource{ObjectRef: ref}]
	found := false
	if entry != nil {
		for _, request := range entry.pending {
			found = found || !request.terminal && request.mode == shareRowExclusive
		}
	}
	e.locks.mu.Unlock()
	if !found {
		t.Fatal("CREATE did not retain original parent DDL request")
	}
	sessionSQL(t, c, "CREATE TABLE shadow.p(id INT PRIMARY KEY)")
	transactionEnd(t, a, tx, true)
	ended = true
	lockAwait(t, done, ErrCatalogReplaced)
	joined = true
	transactionRows(t, c, "SELECT count(*)::int FROM pg_class WHERE relname='c'", 1)
	sessionSQL(t, b, "CREATE TABLE c(pid INT REFERENCES p)")
	sessionSQL(t, c, "INSERT INTO shadow.p VALUES(2)")
	sessionSQL(t, b, "INSERT INTO c VALUES(2)")
	transactionRows(t, b, "SELECT pid FROM c", 2)
	sessionSQL(t, c, "DELETE FROM public.p")
	lockEmpty(t, e)
}

func TestForeignKeyQueuedNonKeyCompatibility(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
			e, a, b, c, ref, row := foreignKeyLockFixture(t)
			tx := transactionBegin(t, a)
			sessionSQL(t, a, "INSERT INTO c VALUES(1)")
			ctx, cancel := context.WithCancel(context.Background())
			done := lockAsync(e, b, ctx, "DELETE FROM p WHERE id=1", false)
			ended, joined := false, false
			defer func() {
				cancel()
				if !ended {
					transactionEnd(t, a, tx, false)
				}
				if !joined {
					lockAwait(t, done, context.Canceled)
				}
			}()
			foreignKeyHeldMode(t, e, ref, row, a.transactions.ticket, keyShare)
			foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, rowUpdate)
			other := transactionBegin(t, c)
			otherEnded := false
			defer func() {
				if !otherEnded {
					transactionEnd(t, c, other, false)
				}
			}()
			updateCtx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			op, err := c.Acquire(updateCtx)
			if err != nil {
				stop()
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, "UPDATE p SET note=11 WHERE id=1"), 0, nil)
			op.Done()
			stop()
			if err != nil || result.Count != 1 {
				t.Fatal("compatible non-key write waited behind pending DELETE", result.Count, err)
			}
			foreignKeyHeldMode(t, e, ref, row, c.transactions.ticket, noKeyUpdate)
			foreignKeyHeldMode(t, e, ref, row, a.transactions.ticket, keyShare)
			foreignKeyQueuedMode(t, e, ref, row, a.transactions.ticket, rowUpdate)
			transactionEnd(t, c, other, true)
			otherEnded = true
			transactionEnd(t, a, tx, commit)
			ended = true
			code := ""
			if commit {
				code = "23503"
			}
			foreignKeyAwaitCode(t, done, code)
			joined = true
			lockEmpty(t, e)
		})
	}
}
