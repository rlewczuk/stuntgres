package engine

import (
	"context"
	"errors"
	"reflect"
	"runtime"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

func TestInsertConflictAuthorizationAfterWait(t *testing.T) {
	for _, revoke := range []string{"predicate SELECT", "table INSERT"} {
		t.Run(revoke, func(t *testing.T) { insertConflictAuthorizationAfterWait(t, revoke) })
	}
}

func insertConflictAuthorizationAfterWait(t *testing.T, revoke string) {
	t.Helper()
	f := insertConflictUniqueFixture(t)
	f.e.storage.SetFrontierLocked(1, 1)
	f.e.visibility.SetCommitSeqLocked(1)
	definition := *f.index.SQLIndex
	definition.Predicate = nil
	f.index.SQLIndex = &definition
	root, buildErr := buildIndexRoot(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1}}, f.parent, &definition, f.store, nil)
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	f.e.mu.Lock()
	f.e.storage.InstallIndexRootLocked(f.index.Address(), root)
	f.e.mu.Unlock()
	admin, adminDone := roleOperation(t, f.e, "postgres")
	defer adminDone()
	if _, err := f.e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "conflict_waiter"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, ok := f.e.Roles().Lookup("conflict_waiter")
	if !ok {
		t.Fatal("reader principal missing")
	}
	owner, ok := f.e.Roles().Lookup("postgres")
	if !ok {
		t.Fatal("owner principal missing")
	}
	ref := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.parent.Address())
	acl := func(privileges authorization.Privileges) authorization.ACLValue {
		return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: privileges}}}
	}
	schema := f.e.catalog.Domain.Ref(f.e.catalog.state.Epoch(), f.parent.Namespace())
	if err := f.e.SetObjectACL(context.Background(), schema, acl(authorization.UsagePrivilege)); err != nil {
		t.Fatal(err)
	}
	if err := f.e.SetObjectACL(context.Background(), ref, acl(authorization.InsertPrivilege)); err != nil {
		t.Fatal(err)
	}
	column := ref
	column.Address.SubID = 10
	if err := f.e.SetObjectACL(context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	column.Address.SubID = 20
	if err := f.e.SetObjectACL(context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	holder, _ := insertConflictUnitScope(t, f)
	if admitted, err := holder.admitInsertRow(ref, &indexWriteCandidate{values: insertConflictUnitRow(t, f, 20)}, indexWriteBatch{}, nil); err != nil || !admitted {
		t.Fatal("holder claim", admitted, err)
	}
	mark := f.e.locks.CheckpointUnique(holder.ticket, nil)
	if len(mark) != 1 {
		t.Fatal("holder did not acquire exact unique key", mark)
	}
	var resource lock.Resource
	for key := range mark {
		resource = key
	}
	if resource.Key() == nil || resource.ObjectRef.Address != f.index.Address() {
		t.Fatal("reservation is not the actual unique provider", resource)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	session, err := f.e.StartSession(ctx, SessionStart{ID: ids.SessionID(testSessionID.Add(1)), Authenticated: reader.Ref})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var op *Operation
	var scope *StatementScope
	type outcome struct {
		admitted bool
		err      error
	}
	done := make(chan outcome, 1)
	started, joined := false, false
	defer func() {
		cancel()
		if !holder.finished {
			_ = holder.Finish(context.Canceled)
		}
		if started && !joined {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("authorization worker did not join")
			}
		}
		if scope != nil && !scope.finished {
			_ = scope.Finish(context.Canceled)
		}
		if op != nil {
			op.Done()
		}
		session.RequestEnd(CleanClose)
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer closeCancel()
		if err := session.WaitEnd(closeCtx); err != nil {
			t.Error(err)
		}
	}()
	op, err = session.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err = f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.gate(&parser.Insert{}); err != nil {
		t.Fatal(err)
	}
	action := &boundInsertConflict{keys: []conflictKey{{attribute: 10}}, predicate: f.predicate, reads: []int{0, 1}, targets: []int{0, 1}}
	evaluation := EvalContext{evalStatement: evalStatement{Context: ctx, Input: scope.input, Output: scope.output}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
	if err := action.selectArbiters(&evaluation, f.parent, f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	scope.insertConflict = action
	prefix := insertConflictUnitRow(t, f, 30)
	if admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: prefix}, indexWriteBatch{}, nil); err != nil || !admitted {
		t.Fatal("authorized prefix", admitted, err)
	}
	ticket := scope.ticket
	prefixMark := f.e.locks.CheckpointUnique(ticket, nil)
	if len(prefixMark) != 1 {
		t.Fatal("accepted prefix owns no exact reservation", prefixMark)
	}
	var prefixResource lock.Resource
	for key := range prefixMark {
		prefixResource = key
	}
	proposal := insertConflictUnitRow(t, f, 20)
	started = true
	go func() {
		admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: proposal}, indexWriteBatch{values: [][]sqltype.Value{prefix}}, nil)
		done <- outcome{admitted, err}
	}()
	// Registration is witnessed under the actual manager mutex for this exact
	// interned key, holder and waiter ticket—not by worker start or elapsed time.
	for {
		manager := f.e.locks
		lockState := manager.Inspect()
		entry := lockState.Resources[resource]
		queued := false
		prefixEntry := lockState.Resources[prefixResource]
		if entry != nil && entry.Holders[holder.ticket].Mode&lock.UniqueClaim != 0 && prefixEntry != nil && prefixEntry.Holders[ticket].Mode&lock.UniqueClaim != 0 {
			for _, request := range entry.Pending {
				if request.Ticket == ticket && request.Mode&lock.UniqueClaim != 0 && !request.Terminal {
					queued = true
					break
				}
			}
		}
		if queued {
			break
		}
		select {
		case result := <-done:
			joined = true
			t.Fatal("admission returned before exact wait", result)
		default:
		}
		if err := ctx.Err(); err != nil {
			t.Fatal("exact key waiter not observed", err)
		}
		runtime.Gosched()
	}
	revoked := column
	if revoke == "table INSERT" {
		revoked = ref
	}
	if err := f.e.SetObjectACL(context.Background(), revoked, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	release := errors.New("release holder transaction")
	if err := holder.Finish(release); !errors.Is(err, release) {
		t.Fatal(err)
	}
	var result outcome
	select {
	case result = <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("postwait admission did not finish", ctx.Err())
	}
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table t"}
	var diagnostic *sqlstate.Error
	if result.admitted || !errors.As(result.err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
		t.Fatal("current predicate privilege was not checked after wait", result)
	}
	if len(scope.stage.rows) != 0 {
		t.Fatal("failed admission published a prefix", scope.stage.rows)
	}
	if err := scope.Finish(result.err); !errors.Is(err, result.err) {
		t.Fatal(err)
	}
	retained := f.e.storage.Relations[f.parent.Address()].Versions
	if len(retained) != 1 || retained[0].Row != 1 || retained[0].Version != 1 || retained[0].Born != 1 || !reflect.DeepEqual(retained[0].Values, insertConflictUnitRow(t, f, 10)) {
		t.Fatal("failed admission changed exact committed sentinel identity or values", retained)
	}
	manager := f.e.locks
	lockState := manager.Inspect()
	leaked := lockState.Valid(ticket)
	for _, entry := range lockState.Resources {
		if _, ok := entry.Holders[ticket]; ok {
			leaked = true
		}
		for _, request := range entry.Pending {
			if request.Ticket == ticket {
				leaked = true
			}
		}
	}
	buckets := lockState.KeyBuckets
	if leaked || buckets != 0 {
		t.Fatal("failed waiter retained grants, requests, transaction ticket or key bucket", leaked, buckets)
	}
	lockEmpty(t, f.e)
	privileges := authorization.SelectPrivilege
	if revoke == "table INSERT" {
		privileges = authorization.InsertPrivilege
	}
	if err := f.e.SetObjectACL(context.Background(), revoked, acl(privileges)); err != nil {
		t.Fatal(err)
	}
	scope, err = f.e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.gate(&parser.Insert{}); err != nil {
		t.Fatal(err)
	}
	scope.insertConflict = &boundInsertConflict{keys: []conflictKey{{attribute: 10}}, predicate: f.predicate, reads: []int{0, 1}, targets: []int{0, 1}}
	if err := scope.insertConflict.selectArbiters(&evaluation, f.parent, f.e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	accepted := make([][]sqltype.Value, 0, 2)
	for _, row := range [][]sqltype.Value{prefix, proposal} {
		admitted, err := scope.admitInsertRow(ref, &indexWriteCandidate{values: row}, indexWriteBatch{values: accepted}, nil)
		if err != nil || !admitted {
			t.Fatal("postfailure authorized admission", admitted, err)
		}
		accepted = append(accepted, row)
	}
	count, err := scope.stage.applyInsertValidated(ref, indexWriteBatch{values: accepted})
	if err != nil || count != 2 {
		t.Fatal("postfailure batch", count, err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	stored := f.e.storage.Relations[f.parent.Address()].Versions
	if len(stored) != 3 || !reflect.DeepEqual(stored[1].Values, prefix) || !reflect.DeepEqual(stored[2].Values, proposal) {
		t.Fatal("postfailure healthy insert did not publish exact accepted rows", stored)
	}
	lockEmpty(t, f.e)
}
