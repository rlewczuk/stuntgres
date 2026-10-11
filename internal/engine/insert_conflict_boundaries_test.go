package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

type insertConflictInputCursor struct {
	schema                      []ColumnDescriptor
	rows                        [][]sqltype.Value
	position, nextCalls, closes int
	nextError, closeError       error
}

func (c *insertConflictInputCursor) Schema() []ColumnDescriptor { return c.schema }
func (c *insertConflictInputCursor) Next(ctx context.Context, out []sqltype.Value) (bool, error) {
	c.nextCalls++
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if c.nextError != nil && c.position == 1 {
		return false, c.nextError
	}
	if c.position == len(c.rows) {
		return false, nil
	}
	copy(out, c.rows[c.position])
	c.position++
	return true, nil
}
func (c *insertConflictInputCursor) Close() error { c.closes++; return c.closeError }
func TestInsertConflictProducerConsumedCountAndClose(t *testing.T) {
	for _, count := range []int{3, -1} {
		for _, failure := range []string{"none", "next", "close"} {
			f := insertConflictUniqueFixture(t)
			scope, ref := insertConflictUnitScope(t, f)
			b := bindingForConflict(ref, f.parent, scope.insertConflict)
			cursor := &insertConflictInputCursor{schema: f.parent.columns, rows: [][]sqltype.Value{insertConflictUnitRow(t, f, 10), insertConflictUnitRow(t, f, 20), insertConflictUnitRow(t, f, 30)}}
			injected := errors.New("owned cursor failure")
			if failure == "next" {
				cursor.nextError = injected
			}
			if failure == "close" {
				cursor.closeError = injected
			}
			rows, err := assembleInsertRows(scope.op.Context(), b, cursor, count, "public", scope.input, scope.output, scope, true, nil, nil)
			if failure == "none" {
				if err != nil || len(rows) != 2 || cursor.position != 3 {
					t.Fatal("consumed vs admitted", count, len(rows), cursor.position, err)
				}
			} else if !errors.Is(err, injected) || rows != nil {
				t.Fatal("failure did not veto batch", rows, err)
			}
			if cursor.closes != 1 {
				t.Fatal("cursor close count", cursor.closes)
			}
			if len(scope.stage.rows) != 0 {
				t.Fatal("assembly published a partial batch")
			}
		}
	}
}
func TestInsertConflictOmissionPreservesNonKeyGrants(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	resource := lockResource{ObjectRef: ref, row: 5}
	if err := f.e.locks.acquire(scope.op.Context(), scope.ticket, resource, rowUpdate); err != nil {
		t.Fatal(err)
	}
	// The mark is deliberately unique-only. Real row/relation/completion ownership
	// must survive even though a new unique-key reservation is discarded.
	admitted, err := scope.admitInsertRow(ref, insertConflictUnitRow(t, f, 10), nil, nil)
	if err != nil || admitted {
		t.Fatal(admitted, err)
	}
	if len(f.e.locks.checkpointUnique(scope.ticket, nil)) != 0 {
		t.Fatal("skipped key retained")
	}
	if f.e.locks.checkpoint(scope.ticket)[resource]&rowUpdate == 0 {
		t.Fatal("row witness lock was lost")
	}
}
func TestInsertConflictACLCurrentManifest(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	admin, done := roleOperation(t, f.e, "postgres")
	defer done()
	if _, err := f.e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "conflict_reader"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, doneReader := roleOperation(t, f.e, "conflict_reader")
	defer doneReader()
	role, _ := f.e.Roles().Lookup("conflict_reader")
	owner, _ := f.e.Roles().Lookup("postgres")
	f.parent.owner = owner.Ref
	f.index.owner = owner.Ref
	ref := ObjectRef{owner: f.e.owner, Epoch: f.e.state.epoch, Address: f.parent.address}
	acl := func(privileges Privileges) ACLValue {
		return ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: owner.Ref, Grantee: ACLGrantee{Role: role.Ref}, Privileges: privileges}}}
	}
	if err := f.e.SetObjectACL(context.Background(), ref, acl(InsertPrivilege)); err != nil {
		t.Fatal(err)
	}
	b := bindingForConflict(ref, f.parent, &boundInsertConflict{targets: []int{0, 1}, reads: []int{0, 1}})
	check := func(want string) {
		t.Helper()
		f.e.mu.Lock()
		auth, err := f.e.authorizationLocked(reader)
		f.e.mu.Unlock()
		if err == nil {
			err = auth.checkBinding(b, &parser.Insert{})
		}
		if want == "" {
			if err != nil {
				t.Fatal(err)
			}
			return
		}
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != want {
			t.Fatal(want, err)
		}
	}
	check("42501")
	column := ref
	column.Address.SubID = 10
	if err := f.e.SetObjectACL(context.Background(), column, acl(SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("42501")
	column.Address.SubID = 20
	if err := f.e.SetObjectACL(context.Background(), column, acl(SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("")
	if err := f.e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	check("42501")
	// Targetless DO NOTHING has no hidden selected-index SELECT manifest.
	b.conflict.reads = nil
	check("")
}

func TestInsertConflictClaimsPreserveAdvisoryCountsAndFinish(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	session := scope.op.session
	key := lockResource{kind: advisoryBigintResource, low: 42}
	for range 2 {
		advisoryUnitGrant(t, session, scope.txRef, key, advisoryShared)
		advisoryUnitGrant(t, session, scope.txRef, key, advisoryExclusive)
	}
	want := relationHold{mode: advisoryShared | advisoryExclusive, shared: 2, exclusive: 2}
	manager := f.e.locks
	// A session ticket has no candidate unique-key contribution. The mark must
	// not capture or later restore the persistent counted ownership.
	mark := manager.checkpointUnique(session.advisory.ticket, nil)
	if len(mark) != 0 {
		t.Fatal("persistent ticket entered candidate checkpoint", mark)
	}
	manager.restoreUnique(session.advisory.ticket, mark)
	row := insertConflictUnitRow(t, f, 20)
	if admitted, err := scope.admitInsertRow(ref, row, nil, nil); err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	if admitted, err := scope.admitInsertRow(ref, insertConflictUnitRow(t, f, 10), [][]sqltype.Value{row}, nil); err != nil || admitted {
		t.Fatal(admitted, err)
	}
	if got := advisoryUnitHold(session, key); got != want {
		t.Fatal("candidate omission altered counted grants", got, want)
	}
	if len(manager.checkpointUnique(scope.ticket, nil)) != 1 {
		t.Fatal("accepted unique claim was lost or skipped claim leaked")
	}
	failure := errors.New("candidate statement rollback")
	if err := scope.Finish(failure); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got := advisoryUnitHold(session, key); got != want {
		t.Fatal("statement Finish altered session ownership", got, want)
	}
	if len(manager.checkpointUnique(scope.ticket, nil)) != 0 {
		t.Fatal("finished statement retained unique claims")
	}
	for range 2 {
		advisoryUnitUnlock(t, session, key, advisoryExclusive, true)
		advisoryUnitUnlock(t, session, key, advisoryShared, true)
	}
	if got := advisoryUnitHold(session, key); got != (relationHold{}) {
		t.Fatal("explicit advisory cleanup retained counts", got)
	}
}
