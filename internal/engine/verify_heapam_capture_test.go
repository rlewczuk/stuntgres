package engine

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func heapUnitInstalled(t *testing.T, e *Engine) *catalog.Extension {
	t.Helper()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	extensionUnitPublish(t, e, op, intarrayUnitStage(t, e, op, "amcheck", ""))
	return e.CatalogRead().MustExtension(e.catalog.State.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "amcheck"}))
}
func heapUnitExpression(t *testing.T, e *Engine) plan.InstalledVerifyHeapamExpr {
	t.Helper()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "verify_heapam"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "typed"}}}}
	node, err := b.BindFunctionProvider(raw)
	if err != nil { t.Fatal(err) }
	return node.(plan.InstalledVerifyHeapamExpr)
}
func heapUnitArgs(t *testing.T, e *Engine, d *catalog.Relation, stop bool) [6]sqltype.Value {
	t.Helper()
	view := e.CatalogRead()
	regclass, _ := view.LookupType(sqltype.Regclass)
	boolean, _ := view.LookupType(sqltype.Bool)
	text, _ := view.LookupType(sqltype.Text)
	bigint, _ := view.LookupType(sqltype.Int8)
	return [6]sqltype.Value{sqltype.String(regclass, strconv.FormatUint(uint64(d.Address().ObjectID), 10)), sqltype.Boolean(boolean, stop), sqltype.Boolean(boolean, false), sqltype.String(text, "none"), sqltype.Null(bigint), sqltype.Null(bigint)}
}
func heapUnitRows(t *testing.T, rows []sqltype.Value) [][]any {
	t.Helper()
	var out [][]any
	for _, row := range rows {
		if row.CompositeLen() != 4 { t.Fatal("wrong record width", row.CompositeLen()) }
		fields := make([]any, 4)
		for i := range fields {
			v := row.CompositeField(i)
			if v.IsNull() { continue }
			if i == 3 { value, err := v.Text(); if err != nil { t.Fatal(err) }; fields[i] = value } else { value, err := v.Integer(); if err != nil { t.Fatal(err) }; fields[i] = value }
		}
		out = append(out, fields)
	}
	return out
}

func TestVerifyHeapamCapture(t *testing.T) {
	t.Run("unfiltered-stable-identity-immutable-roots", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		heapUnitInstalled(t, e)
		n := heapUnitExpression(t, e)
		integer := testUsage(e, sqltype.Int4).Type
		v, err := sqltype.Integer(integer, 7)
		if err != nil { t.Fatal(err) }
		bad := []storage.RowVersion{{Row: 11, Version: 101, Born: 5, Values: []sqltype.Value{v, v}}, {Row: 12, Version: 102, Born: 6, Values: []sqltype.Value{v}}}
		old := append([]storage.RowVersion(nil), bad...)
		e.mu.Lock()
		store := e.storage.Relations[d.Address()]
		store.Versions = bad
		store.Ends = &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{11: {Version: 101, End: 6}}}
		e.mu.Unlock()
		reader, ticket := blockingUnitOwner(t, e)
		scope := pgrowlocksUnitScope(t, e, reader)
		scope.routineDependencies = []catalog.ExpressionDependency{{Ref: n.Ref, Revision: n.Procedure.Revision()}}
		if err := scope.snapshotDataPhase(); err != nil { t.Fatal(err) }
		ref := e.catalog.Domain.Ref(e.catalog.State.Epoch(), d.Address())
		resource := lock.Resource{ObjectRef: ref}
		want := [][]any{{nil, nil, int64(7), "logical row 11 version 101: attribute 7 has an invalid typed value"}, {nil, nil, nil, "logical row 12 version 102: row has 1 values but relation has 2 columns"}}
		for _, stop := range []bool{false, true} {
			rows, err := (*statementHost)(scope).VerifyHeapam(heapUnitArgs(t, e, d, stop), n.Usage.Type)
			expected := want
			if stop { expected = want[:1] }
			if err != nil || !reflect.DeepEqual(heapUnitRows(t, rows), expected) { t.Fatal("unfiltered exact findings", rows, err, expected) }
			if e.locks.HasAccessShare(ticket, resource) { t.Fatal("verification retained added AccessShare") }
		}
		if !reflect.DeepEqual(bad, old) || !reflect.DeepEqual(bad[0].Values, []sqltype.Value{v, v}) || !reflect.DeepEqual(bad[1].Values, []sqltype.Value{v}) { t.Fatal("verification mutated retained rows/values") }
		// Retain an explicit prefix, replace only the live COW vector, and
		// require the same exact findings through the old captured roots.
		data := &visibility.Data{Catalog: e.CatalogRead(), Committed: map[ids.ObjectAddress][]storage.RowVersion{ref.Address: bad}, Ends: map[ids.ObjectAddress]*storage.DeletionRoot{ref.Address: store.Ends}, Frontier: 99}
		view := visibility.NewView(data)
		defer view.Release()
		e.mu.Lock()
		store.Versions = nil
		e.mu.Unlock()
		findings, err := exec.VerifyHeapamPrefix(context.Background(), view, ref, d, false)
		if err != nil || len(findings) != 2 || findings[0].Row != 11 || findings[0].Version != 101 { t.Fatal("old roots lost", findings, err) }
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if rows, err := exec.VerifyHeapamPrefix(ctx, view, ref, d, false); !errors.Is(err, context.Canceled) || rows != nil { t.Fatal("canceled captured scan", rows, err) }
		request, err := e.locks.StartAcquire(context.Background(), ticket, resource, lock.AccessShare, lock.Policy{})
		if err != nil || request != nil { t.Fatal(request, err) }
		args := heapUnitArgs(t, e, d, false)
		args[2] = sqltype.Boolean(testUsage(e, sqltype.Bool).Type, true)
		_, err = (*statementHost)(scope).VerifyHeapam(args, n.Usage.Type)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || !e.locks.HasAccessShare(ticket, resource) { t.Fatal("prior contribution/error release", err) }
	})
	t.Run("readmission-fences-descriptor-and-storage", func(t *testing.T) {
		e, _, d := typedUnitCatalog(t, false)
		reader, _ := blockingUnitOwner(t, e)
		scope := pgrowlocksUnitScope(t, e, reader)
		ref := e.catalog.Domain.Ref(e.catalog.State.Epoch(), d.Address())
		store := e.storage.Relations[d.Address()]
		e.mu.Lock()
		auth, err := scope.writeAuthorizationLocked(e)
		if err == nil { err = scope.verifyHeapamReadmitLocked(e, auth, ref, d, store, nil) }
		e.mu.Unlock()
		if err != nil { t.Fatal(err) }
		e.mu.Lock()
		e.storage.Relations[d.Address()] = &storage.Relation{}
		err = scope.verifyHeapamReadmitLocked(e, auth, ref, d, store, nil)
		e.storage.Relations[d.Address()] = store
		bad := *d
		bad.ObjectRevision++
		errDescriptor := scope.verifyHeapamReadmitLocked(e, auth, ref, &bad, store, nil)
		foreign := ref
		foreign.Epoch++
		errEpoch := scope.verifyHeapamReadmitLocked(e, auth, foreign, d, store, nil)
		e.mu.Unlock()
		for _, err := range []error{err, errDescriptor, errEpoch} { if !errors.Is(err, ids.ErrCatalogReplaced) { t.Fatal("readmission accepted replaced authority", err) } }
	})
}

func TestVerifyHeapamCaptureWait(t *testing.T) {
	for _, cancel := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel", false: "storage-replaced"}[cancel], func(t *testing.T) {
			e, _, d := typedUnitCatalog(t, false)
			heapUnitInstalled(t, e)
			n := heapUnitExpression(t, e)
			holderSession, holder := blockingUnitOwner(t, e)
			reader, ticket := blockingUnitOwner(t, e)
			scope := pgrowlocksUnitScope(t, e, reader)
			resource := lock.Resource{ObjectRef: e.catalog.Domain.Ref(e.catalog.State.Epoch(), d.Address())}
			request, err := e.locks.StartAcquire(context.Background(), holder, resource, lock.AccessExclusive, lock.Policy{})
			if err != nil || request != nil { t.Fatal(request, err) }
			held := true
			defer func() { if held { transactionEnd(t, holderSession, holder.Ref(), false) } }()
			args := heapUnitArgs(t, e, d, false)
			done := make(chan error, 1)
			go func() { _, err := (*statementHost)(scope).VerifyHeapam(args, n.Usage.Type); done <- err }()
			joined := false
			defer func() {
				if !joined { scope.op.Cancel(context.Canceled); <-done }
			}()
			lockPending(t, e, 1)
			snapshot := e.locks.Inspect()
			entry := snapshot.Resources[resource]
			if entry == nil || len(entry.Pending) != 1 || entry.Pending[0].Ticket != ticket || entry.Pending[0].Mode != lock.AccessShare {
				t.Fatal("heap verification did not queue its exact temporary contribution")
			}
			want := context.Canceled
			if cancel { scope.op.Cancel(context.Canceled) } else {
				e.mu.Lock()
				e.storage.Relations[d.Address()] = &storage.Relation{}
				e.mu.Unlock()
				transactionEnd(t, holderSession, holder.Ref(), false)
				held = false
				want = ids.ErrCatalogReplaced
			}
			err = <-done
			joined = true
			if !errors.Is(err, want) || e.locks.HasAccessShare(ticket, resource) { t.Fatal("wait lost cancellation/incarnation or retained grant", err, want) }
		})
	}
}
