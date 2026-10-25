package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestQueryJoinsPrivateVisibilityAndRepeatableRead(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)"} {
		enginetest.SessionSQL(t, a, q)
	}
	tx := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
	enginetest.TransactionRows(t, a, "SELECT k FROM a JOIN b USING(k)", 1)
	peer := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, b, "INSERT INTO a VALUES(2)")
	enginetest.SessionSQL(t, b, "INSERT INTO b VALUES(2)")
	enginetest.TransactionEnd(t, b, peer, true)
	enginetest.TransactionRows(t, a, "SELECT k FROM a JOIN b USING(k)", 1)
	enginetest.SessionSQL(t, a, "INSERT INTO a VALUES(3)")
	enginetest.SessionSQL(t, a, "INSERT INTO b VALUES(3)")
	enginetest.TransactionRows(t, a, "SELECT k FROM a JOIN b USING(k) ORDER BY k", 1, 3)
	enginetest.SessionSQL(t, a, "DELETE FROM a WHERE k=1")
	enginetest.TransactionRows(t, a, "SELECT k FROM a JOIN b USING(k)", 3)
	enginetest.TransactionEnd(t, a, tx, false)
	enginetest.TransactionRows(t, b, "SELECT k FROM a JOIN b USING(k) ORDER BY k", 1, 2)
	enginetest.LockEmpty(t, e)
}

func TestQueryJoinsStatementFrontierAndNoWidening(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "CREATE TABLE unrelated(k INT)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)"} {
		enginetest.SessionSQL(t, a, q)
	}
	op := enginetest.TransactionOperation(t, a)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	ast := enginetest.Statement(t, "SELECT k FROM a JOIN b USING(k)")
	if _, err = scope.Describe(ast); err != nil {
		t.Fatal(err)
	}
	unrelated := enginetest.CatalogRef(t, e, e.CatalogRead(), "unrelated")
	var widenErr error
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var binding analyze.Binding
	if err == nil {
		binding, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	var source *exec.Source
	if err == nil {
		source, err = e.CaptureQuerySourcesLocked(scope, binding.QuerySources, false)
	}
	frontier := e.Visibility.CommitSeq()
	if err == nil {
		_, widenErr = scope.AcquireDataViewLocked(e, unrelated)
	}
	e.Mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	if widenErr == nil {
		scope.Finish(errors.New("view widened"))
		t.Fatal("retained statement view widened")
	}
	defer source.Close()
	data := source.View.Data()
	if source.Sources[1].View.Data() != data || data.Frontier != frontier || len(data.Committed) != 2 || data.GenerationAt(binding.QuerySources[0].Ref.Address) != binding.QuerySources[0].Storage || data.GenerationAt(binding.QuerySources[1].Ref.Address) != binding.QuerySources[1].Storage {
		t.Fatal(data)
	}
	peer := enginetest.TransactionBegin(t, b)
	enginetest.SessionSQL(t, b, "INSERT INTO a VALUES(2)")
	enginetest.SessionSQL(t, b, "INSERT INTO b VALUES(2)")
	enginetest.TransactionEnd(t, b, peer, true)
	if err = exec.SelectQuerySources(op.Ctx, source); err != nil {
		t.Fatal(err)
	}
	rows, n, err := exec.Finalize(op.Ctx, binding.Query.Root, source, nil)
	if err != nil || n != 1 {
		t.Fatal(rows, n, err)
	}
	values := make([]sqltype.Value, 1)
	present, err := rows.Next(context.Background(), values)
	value, _ := values[0].Integer()
	if !present || err != nil || value != 1 {
		t.Fatal(present, err, value)
	}
	rows.Close()
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.TransactionRows(t, b, "SELECT k FROM a JOIN b USING(k) ORDER BY k", 1, 2)
	enginetest.LockEmpty(t, e)
}

func TestQueryJoinsSecondLeafLockAndCancellation(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, cancelled := range []bool{false, true} {
			e := engine.New()
			holder := enginetest.StartSession(t, e, nil)
			reader := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)"} {
				enginetest.SessionSQL(t, holder, q)
			}
			tx := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "LOCK TABLE b IN ACCESS EXCLUSIVE MODE")
			ctx, cancel := context.WithCancel(context.Background())
			done := enginetest.LockAsync(e, reader, ctx, "SELECT a.k FROM a JOIN b USING(k)", prepare)
			enginetest.LockPending(t, e, 1)
			aref := enginetest.CatalogRef(t, e, e.CatalogRead(), "a")
			bref := enginetest.CatalogRef(t, e, e.CatalogRead(), "b")
			lockState := e.Locks.Inspect()
			first := lockState.Resources[lock.Resource{ObjectRef: aref}]
			second := lockState.Resources[lock.Resource{ObjectRef: bref}]
			admitted := first != nil && len(first.Holders) == 1 && second != nil && len(second.Pending) == 1
			if !admitted {
				t.Fatal("second-leaf wait did not retain first grant")
			}
			if cancelled {
				cancel()
				enginetest.LockAwait(t, done, context.Canceled)
			}
			enginetest.TransactionEnd(t, holder, tx, false)
			if !cancelled {
				enginetest.LockAwait(t, done, nil)
				cancel()
			}
			enginetest.LockEmpty(t, e)
		}
	}
}

func TestQueryJoinsSelfLockAndInsertDestination(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TABLE a(k INT)")
	enginetest.SessionSQL(t, s, "INSERT INTO a VALUES(1),(1)")
	tx := enginetest.TransactionBegin(t, s)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM a AS x CROSS JOIN a AS y", 4)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "a")
	lockState := e.Locks.Inspect()
	entry := lockState.Resources[lock.Resource{ObjectRef: ref}]
	ok := len(lockState.Resources) == 1 && entry != nil && len(entry.Holders) == 1 && len(entry.Pending) == 0
	if !ok {
		t.Fatal("self occurrences duplicated physical lock authority")
	}
	result := enginetest.SessionSQL(t, s, "INSERT INTO a SELECT x.k FROM a AS x JOIN a AS y USING(k)")
	if result.Rows != nil || result.Count != 4 {
		t.Fatal(result)
	}
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM a", 6)
	enginetest.TransactionEnd(t, s, tx, false)
	enginetest.TransactionRows(t, s, "SELECT count(*) FROM a", 2)
	enginetest.LockEmpty(t, e)
}

func TestQueryJoinsGrantIdentityReplacement(t *testing.T) {
	e, query, source, release := queryJoinsAdmittedFixture(t, "SELECT a.k FROM a JOIN b USING(k)", true)
	defer release()
	old := source.Sources[1].Ref
	// Snapshot genuine admission evidence while its statement is still live.
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
	}()
	if _, err = scope.Describe(enginetest.Statement(t, "SELECT a.k FROM a JOIN b USING(k)")); err != nil {
		t.Fatal(err)
	}
	if len(scope.QuerySources) != 2 || scope.QuerySources[1].Ref != old {
		t.Fatal(scope.QuerySources, old)
	}
	grant := scope.QuerySources[1]
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	if len(scope.QuerySources) != 0 {
		t.Fatal("finished scope retained admission metadata")
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	// The explicit statement still authorizes logical consumption of captured
	// immutable rows, but neither it nor the separate finished statement's grant
	// can authorize the old physical identity in the replacement catalog.
	e.Mu.Lock()
	_, err = core.CurrentQuerySourceLocked(e, e.Catalog.Current(), nil, grant)
	e.Mu.Unlock()
	if !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal(old, err)
	}
	rows, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || n != 4 {
		t.Fatal(rows, n, err)
	}
	rows.Close()
}

func TestQueryJoinsSecondLeafGrantRebind(t *testing.T) {
	for _, prepare := range []bool{false, true} {
		for _, kind := range []string{"revision", "shadow", "reset", "restore", "recreate"} {
			t.Run(kind+map[bool]string{false: "/execute", true: "/describe"}[prepare], func(t *testing.T) {
				e := engine.New()
				holder, reader := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE SCHEMA shadow", "CREATE TABLE l(a INT)", "CREATE TABLE t(a INT)", "INSERT INTO l VALUES(7)", "INSERT INTO t VALUES(7)"} {
					enginetest.SessionSQL(t, holder, q)
				}
				if kind == "shadow" {
					enginetest.SessionSQL(t, reader, "SET search_path TO shadow,public")
				}
				tx := enginetest.TransactionBegin(t, holder)
				enginetest.SessionSQL(t, holder, "LOCK TABLE t IN ACCESS EXCLUSIVE MODE")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				done := enginetest.LockAsync(e, reader, ctx, "SELECT t.a FROM l JOIN t USING(a)", prepare)
				enginetest.LockPending(t, e, 1)
				switch kind {
				case "revision":
					stage, ref, _ := notNullMetadataStage(t, e)
					publication, err := e.ValidateCatalogStage(context.Background(), stage)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = enginetest.PublishCatalog(e, publication, core.PublicationToken{Epoch: ref.Epoch}); err != nil {
						t.Fatal(err)
					}
				case "shadow":
					enginetest.Run(t, e, "CREATE TABLE shadow.t(a INT)")
				case "reset":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
				case "restore":
					if err := engine.Restore(context.Background(), e, strings.NewReader("CREATE TABLE l(a INT);CREATE TABLE t(a INT);INSERT INTO l VALUES(91);INSERT INTO t VALUES(91)")); err != nil {
						t.Fatal(err)
					}
				case "recreate":
					enginetest.SessionSQL(t, holder, "DROP TABLE t")
					enginetest.SessionSQL(t, holder, "CREATE TABLE t(a INT)")
				}
				if kind == "reset" || kind == "restore" {
					enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				}
				enginetest.TransactionEnd(t, holder, tx, kind == "recreate")
				if kind == "revision" {
					enginetest.LockAwait(t, done, nil)
				} else if kind != "reset" && kind != "restore" {
					enginetest.LockAwait(t, done, ids.ErrCatalogReplaced)
				}
				enginetest.LockEmpty(t, e)
			})
		}
	}
}

func TestQueryJoinsCancelPreservesTransactionAuthority(t *testing.T) {
	for _, recoverable := range []bool{false, true} {
		t.Run(map[bool]string{false: "full-abort", true: "savepoint-abort"}[recoverable], func(t *testing.T) {
			e := engine.New()
			holder, reader := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "CREATE TABLE prior(k INT)"} {
				enginetest.SessionSQL(t, holder, q)
			}
			h := enginetest.TransactionBegin(t, holder)
			enginetest.SessionSQL(t, holder, "LOCK TABLE b IN ACCESS EXCLUSIVE MODE")
			r := enginetest.TransactionBegin(t, reader)
			enginetest.TransactionRows(t, reader, "SELECT count(*) FROM prior", 0)
			prior := enginetest.RoleTableRef(t, e, "prior")
			left, right := enginetest.RoleTableRef(t, e, "a"), enginetest.RoleTableRef(t, e, "b")
			readerTicket, holderTicket := reader.Transactions.Ticket, holder.Transactions.Ticket
			if recoverable {
				controlsRequire(t, reader, "SAVEPOINT before_join")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := enginetest.LockAsync(e, reader, ctx, "SELECT * FROM a CROSS JOIN b", false)
			enginetest.LockPending(t, e, 1)
			lockState := e.Locks.Inspect()
			entry := lockState.Resources[lock.Resource{ObjectRef: prior}]
			priorHeld := entry != nil && entry.Holders[readerTicket].Mode&lock.AccessShare != 0
			first := lockState.Resources[lock.Resource{ObjectRef: left}]
			joinHeld := first != nil && first.Holders[readerTicket].Mode&lock.AccessShare != 0
			if !priorHeld || !joinHeld {
				t.Fatal("join admission lost earlier authority or failed to acquire its first leaf", priorHeld, joinHeld)
			}
			cancel()
			enginetest.LockAwait(t, done, context.Canceled)
			lockState = e.Locks.Inspect()
			entry = lockState.Resources[lock.Resource{ObjectRef: prior}]
			priorHeld = entry != nil && entry.Holders[readerTicket].Mode&lock.AccessShare != 0
			other := lockState.Resources[lock.Resource{ObjectRef: right}]
			holderHeld := other != nil && other.Holders[holderTicket].Mode&lock.AccessExclusive != 0 && other.Holders[readerTicket].Mode == 0 && len(other.Pending) == 0
			first = lockState.Resources[lock.Resource{ObjectRef: left}]
			joinHeld = first != nil && first.Holders[readerTicket].Mode&lock.AccessShare != 0
			// Full abort ends the reader ticket and all its grants. Savepoint
			// abort retains the ticket and premark grant, but releases the
			// admitted first leaf acquired after the mark. Neither releases
			// the competing owner's grant or retains the canceled waiter.
			if priorHeld != recoverable || !holderHeld || joinHeld || reader.TransactionState() != session.TxAborted {
				t.Fatal("cancellation violated transaction authority", priorHeld, holderHeld, joinHeld, reader.TransactionState())
			}
			if recoverable && reader.Transactions.Ticket != readerTicket || !recoverable && reader.Transactions.Ticket != nil {
				t.Fatal("cancellation changed transaction ticket authority")
			}
			if recoverable {
				controlsRequire(t, reader, "ROLLBACK TO before_join")
				if reader.Transactions.Ticket != readerTicket || reader.TransactionState() != session.TxActive {
					t.Fatal("savepoint recovery replaced outer authority")
				}
				enginetest.TransactionRows(t, reader, "SELECT count(*) FROM prior", 0)
			}
			enginetest.TransactionEnd(t, reader, r, false)
			enginetest.TransactionEnd(t, holder, h, false)
			enginetest.LockEmpty(t, e)
		})
	}
}

func TestQueryJoinsEarlierLeafMetadataDuringWait(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, revoked := range []bool{false, true} {
			for _, inserting := range []bool{false, true} {
				e := engine.New()
				holder, reader := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE USER waiting_join_reader", "CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "CREATE TABLE dst(k INT)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)"} {
					enginetest.SessionSQL(t, holder, q)
				}
				role, _ := e.Roles().Lookup("waiting_join_reader")
				grantor := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
				left := enginetest.RoleTableRef(t, e, "a")
				grant := func(name string, rights authorization.Privileges) {
					t.Helper()
					if err := enginetest.SetObjectACL(e, context.Background(), enginetest.RoleTableRef(t, e, name), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: grantor, Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: rights}}}); err != nil {
						t.Fatal(err)
					}
				}
				grant("a", authorization.SelectPrivilege)
				grant("b", authorization.SelectPrivilege)
				grant("dst", authorization.InsertPrivilege)
				enginetest.SessionSQL(t, reader, "SET SESSION AUTHORIZATION waiting_join_reader")
				q := "SELECT a.k FROM a JOIN b USING(k)"
				if inserting {
					q = "INSERT INTO dst SELECT a.k FROM a JOIN b USING(k)"
				}
				ast := enginetest.Statement(t, q)
				var expected *core.StatementDescription
				if prepared {
					op := enginetest.TransactionOperation(t, reader)
					description, err := e.Describe(op, ast)
					op.Done()
					if err != nil {
						t.Fatal(err)
					}
					expected = &description
				}
				tx := enginetest.TransactionBegin(t, holder)
				enginetest.SessionSQL(t, holder, "LOCK TABLE b IN ACCESS EXCLUSIVE MODE")
				old := e.Catalog.State.Get(left.Address)
				done := make(chan error, 1)
				var result core.Result
				var capturedScope *core.StatementScope
				var capturedSources []exec.Leaf
				go func() {
					op, err := reader.Acquire(context.Background())
					if err != nil {
						done <- err
						return
					}
					scope, err := e.StartStatement(op)
					if err != nil {
						op.Done()
						done <- err
						return
					}
					capturedScope = scope
					scope.Expected = expected
					result, err = scope.Execute(ast, 0, nil)
					// Observe admission metadata before Finish clears grants.
					capturedSources = append([]exec.Leaf(nil), scope.QuerySources...)
					err = scope.Finish(err)
					if result.Rows != nil {
						result.Rows.Close()
					}
					op.Done()
					done <- err
				}()
				enginetest.LockPending(t, e, 1)
				enginetest.Run(t, e, "COMMENT ON TABLE a IS 'join wait metadata'")
				if revoked {
					grant("a", 0)
				}
				enginetest.TransactionEnd(t, holder, tx, false)
				var err error
				select {
				case err = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("metadata-refresh waiter stuck")
				}
				if revoked {
					var diagnostic *sqlstate.Error
					want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table a"}
					if !errors.As(err, &diagnostic) || *diagnostic != want || result.Rows != nil || result.Count != 0 {
						t.Fatal(result, diagnostic, err)
					}
				} else if err != nil || result.Count != 1 || (result.Rows != nil) == inserting {
					t.Fatal(result, err)
				}
				if capturedScope == nil || len(capturedSources) != 2 || capturedSources[0].Relation == old || capturedSources[0].Relation.Comment != "join wait metadata" || len(capturedScope.QuerySources) != 0 {
					t.Fatal("earlier grant metadata was not refreshed or cleanup retained grants", capturedSources, capturedScope)
				}
				wantCount := int64(0)
				if inserting && !revoked {
					wantCount = 1
				}
				enginetest.TransactionRows(t, holder, "SELECT count(*) FROM dst", wantCount)
				enginetest.LockEmpty(t, e)
			}
		}
	}
}

func TestQueryJoinsParameterAnalysis(t *testing.T) {
	e := engine.New()
	for _, q := range []string{"CREATE TABLE a(l TEXT,k INT)", "CREATE TABLE b(r TEXT,k INT)"} {
		enginetest.Run(t, e, q)
	}
	s := enginetest.StartSession(t, e, nil)
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	for _, tc := range []struct {
		q      string
		result sqltype.ID
	}{
		{"SELECT a.l FROM a JOIN b ON a.k=$1 AND a.k=b.k", sqltype.Text},
		{"SELECT $1 FROM a JOIN b ON a.k=$1 AND a.k=b.k", sqltype.Int4},
	} {
		description, err := e.Describe(op, enginetest.Statement(t, tc.q))
		if err != nil || description.Parameters.Count != 1 || len(description.Parameters.Types) != 1 || description.Parameters.Types[0].Type.ID() != sqltype.Int4 || len(description.Results) != 1 || description.Results[0].Usage.Type.ID() != tc.result {
			t.Fatal(tc, description, err)
		}
	}
}

func TestQueryJoinsSelectedPrivateRoots(t *testing.T) {
	e := engine.New()
	s, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "CREATE TABLE unrelated(k INT)", "INSERT INTO a VALUES(1)", "INSERT INTO b VALUES(1)", "INSERT INTO unrelated VALUES(9)"} {
		enginetest.SessionSQL(t, s, q)
	}
	tx := enginetest.TransactionBegin(t, s)
	enginetest.SessionSQL(t, s, "DELETE FROM unrelated WHERE k=9")
	enginetest.SessionSQL(t, s, "INSERT INTO unrelated VALUES(10)")
	unrelated := enginetest.RoleTableRef(t, e, "unrelated")
	var held exec.RowResult
	for _, private := range []bool{false, true} {
		if private {
			enginetest.SessionSQL(t, s, "DELETE FROM a WHERE k=1")
			enginetest.SessionSQL(t, s, "INSERT INTO a VALUES(3)")
			enginetest.SessionSQL(t, s, "INSERT INTO b VALUES(3)")
		}
		var query *plan.Query
		source, finish := func() (*exec.Source, func(error) error) {
			op := enginetest.TransactionOperation(t, s)
			t.Cleanup(op.Done)
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			released := false
			finish := func(err error) error {
				if released {
					return err
				}
				released = true
				err = scope.Finish(err)
				op.Done()
				return err
			}
			t.Cleanup(func() { _ = finish(context.Canceled) })
			ast := enginetest.Statement(t, "SELECT k FROM a JOIN b USING(k)")
			if _, err = scope.Describe(ast); err != nil {
				finish(err)
				t.Fatal(err)
			}
			if scope.OwnDeletes[unrelated.Address] == nil || len(scope.OwnWrites[unrelated.Address]) == 0 {
				finish(nil)
				t.Fatal("private-root exclusion witness was vacuous")
			}
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			var bound analyze.Binding
			if err == nil {
				bound, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
			}
			var source *exec.Source
			if err == nil {
				source, err = e.CaptureQuerySourcesLocked(scope, bound.QuerySources, false)
			}
			e.Mu.Unlock()
			if err == nil {
				err = exec.SelectQuerySources(op.Ctx, source)
			}
			if err != nil {
				finish(err)
				t.Fatal(err)
			}
			query = bound.Query
			return source, finish
		}()
		defer source.Close()
		data := source.View.Data()
		ownCount, deleteCount := 0, 0
		if private {
			ownCount, deleteCount = 2, 1
		}
		if len(data.Committed) != 2 || len(data.Own) != ownCount || len(data.Deleted) != deleteCount || len(data.Own[unrelated.Address]) != 0 || data.Deleted[unrelated.Address] != nil {
			t.Fatal("unrelated private roots retained", data)
		}
		rows, n, err := exec.Finalize(context.Background(), query.Root, source, nil)
		err = finish(err)
		if err != nil || n != 1 {
			t.Fatal(rows, n, err)
		}
		if private {
			held = rows
		} else {
			rows.Close()
		}
	}
	enginetest.TransactionEnd(t, s, tx, false)
	values := make([]sqltype.Value, 1)
	present, err := held.Next(context.Background(), values)
	value, _ := values[0].Integer()
	if !present || err != nil || value != 3 {
		t.Fatal("rollback invalidated eager private output", present, value, err)
	}
	held.Close()
	enginetest.TransactionRows(t, peer, "SELECT k FROM a JOIN b USING(k)", 1)
	enginetest.TransactionRows(t, peer, "SELECT k FROM unrelated", 9)
	enginetest.LockEmpty(t, e)
}

func notNullMetadataStage(t *testing.T, e *core.Engine) (*core.CatalogStage, ids.ObjectRef, *catalog.Relation) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	next.Columns = append([]catalog.Column(nil), old.Columns...)
	next.Columns[0].Nullable = false
	next.Constraints.NotNull = []catalog.NotNullConstraint{{Name: "t_a_not_null", Attribute: 1}}
	e.Mu.Lock()
	err := table.AssignDefinitionOIDs(core.DDLHost{E: e}, &next)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := e.Storage.Relations[ref.Address]
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.AlterRelation(context.Background(), ref, &next, original, original, len(original.Versions), original.Generation, original.Ends); err != nil {
		t.Fatal(err)
	}
	return stage, ref, old
}
