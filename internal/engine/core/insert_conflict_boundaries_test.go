package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type insertConflictInputCursor struct {
	schema                      []catalog.Column
	rows                        [][]sqltype.Value
	position, nextCalls, closes int
	nextError, closeError       error
}

func (c *insertConflictInputCursor) Schema() []catalog.Column { return c.schema }
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
			b := dml.BindingForConflict(ref, f.parent, scope.InsertConflict)
			cursor := &insertConflictInputCursor{schema: f.parent.Columns, rows: [][]sqltype.Value{insertConflictUnitRow(t, f, 10), insertConflictUnitRow(t, f, 20), insertConflictUnitRow(t, f, 30)}}
			injected := errors.New("owned cursor failure")
			if failure == "next" {
				cursor.nextError = injected
			}
			if failure == "close" {
				cursor.closeError = injected
			}
			rows, err := dml.AssembleInsertRows(scope.Op.Context(), b, cursor, count, "public", scope.Input, scope.Output, scope.WriteHost(), true, nil, nil)
			if failure == "none" {
				if err != nil || len(rows.Values) != 2 || cursor.position != 3 {
					t.Fatal("consumed vs admitted", count, len(rows.Values), cursor.position, err)
				}
			} else if !errors.Is(err, injected) || rows.Values != nil {
				t.Fatal("failure did not veto batch", rows, err)
			}
			if cursor.closes != 1 {
				t.Fatal("cursor close count", cursor.closes)
			}
			if len(scope.Stage.Rows) != 0 {
				t.Fatal("assembly published a partial batch")
			}
		}
	}
}
func TestInsertConflictOmissionPreservesNonKeyGrants(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	resource := lock.Resource{ObjectRef: ref, Row: 5}
	if err := f.e.Locks.Acquire(scope.Op.Context(), scope.LockTicket, resource, lock.ForUpdate); err != nil {
		t.Fatal(err)
	}
	// The mark is deliberately unique-only. Real row/relation/completion ownership
	// must survive even though a new unique-key reservation is discarded.
	admissionContext := scope.ActivateRuntime(scope.Op.Context())
	admitted, err := dml.AdmitInsertRow(admissionContext, scope.WriteHost(), ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 10)}, index.WriteBatch{}, nil)
	if err != nil || admitted {
		t.Fatal(admitted, err)
	}
	if len(f.e.Locks.CheckpointUnique(scope.LockTicket, nil)) != 0 {
		t.Fatal("skipped key retained")
	}
	if f.e.Locks.Checkpoint(scope.LockTicket)[resource].Mode&lock.ForUpdate == 0 {
		t.Fatal("row witness lock was lost")
	}
}
func TestInsertConflictACLCurrentManifest(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	admin, done := enginetest.RoleOperation(t, f.e, "postgres")
	defer done()
	if _, err := f.e.Execute(admin, &parser.CreateRole{Name: parser.Identifier{Text: "conflict_reader"}, Options: parser.RoleOptions{Present: parser.RoleLogin, Login: true}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	reader, doneReader := enginetest.RoleOperation(t, f.e, "conflict_reader")
	defer doneReader()
	role, _ := f.e.Roles().Lookup("conflict_reader")
	owner, _ := f.e.Roles().Lookup("postgres")
	f.parent.Owner = owner.Ref
	f.index.Owner = owner.Ref
	ref := f.e.Catalog.Domain.Ref(f.e.Catalog.State.Epoch(), f.parent.Address())
	acl := func(privileges authorization.Privileges) authorization.ACLValue {
		return authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner.Ref, Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: privileges}}}
	}
	if err := enginetest.SetObjectACL(f.e, context.Background(), ref, acl(authorization.InsertPrivilege)); err != nil {
		t.Fatal(err)
	}
	b := dml.BindingForConflict(ref, f.parent, dml.NewInsertConflictState(&analyze.InsertConflict{Targets: []int{0, 1}, Reads: []int{0, 1}}))
	check := func(want string) {
		t.Helper()
		f.e.Mu.Lock()
		auth, err := f.e.AuthorizationLocked(reader)
		f.e.Mu.Unlock()
		if err == nil {
			err = analyze.CheckBinding(auth, b, &parser.Insert{})
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
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("42501")
	column.Address.SubID = 20
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, acl(authorization.SelectPrivilege)); err != nil {
		t.Fatal(err)
	}
	check("")
	if err := enginetest.SetObjectACL(f.e, context.Background(), column, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	check("42501")
	// Targetless DO NOTHING has no hidden selected-index SELECT manifest.
	b.Insert.Conflict.Reads = nil
	check("")
}

func TestInsertConflictClaimsPreserveAdvisoryCountsAndFinish(t *testing.T) {
	f := insertConflictUniqueFixture(t)
	scope, ref := insertConflictUnitScope(t, f)
	session := scope.Op.Session
	key := lock.Resource{Database: scope.Engine().Catalog.Domain, Kind: lock.AdvisoryBigintResource, Low: 42}
	for range 2 {
		advisoryUnitGrant(t, session, scope.TxRef, key, lock.AdvisoryShare)
		advisoryUnitGrant(t, session, scope.TxRef, key, lock.AdvisoryExclusive)
	}
	want := lock.Hold{Mode: lock.AdvisoryShare | lock.AdvisoryExclusive, Shared: 2, Exclusive: 2}
	manager := f.e.Locks
	// A session ticket has no candidate unique-key contribution. The mark must
	// not capture or later restore the persistent counted ownership.
	mark := manager.CheckpointUnique(session.Advisory.Ticket, nil)
	if len(mark) != 0 {
		t.Fatal("persistent ticket entered candidate checkpoint", mark)
	}
	manager.RestoreUnique(session.Advisory.Ticket, mark)
	row := insertConflictUnitRow(t, f, 20)
	admissionContext := scope.ActivateRuntime(scope.Op.Context())
	if admitted, err := dml.AdmitInsertRow(admissionContext, scope.WriteHost(), ref, &index.WriteCandidate{Values: row}, index.WriteBatch{}, nil); err != nil || !admitted {
		t.Fatal(admitted, err)
	}
	if admitted, err := dml.AdmitInsertRow(admissionContext, scope.WriteHost(), ref, &index.WriteCandidate{Values: insertConflictUnitRow(t, f, 10)}, index.WriteBatch{Values: [][]sqltype.Value{row}}, nil); err != nil || admitted {
		t.Fatal(admitted, err)
	}
	if got := advisoryUnitHold(session, key); got != want {
		t.Fatal("candidate omission altered counted grants", got, want)
	}
	if len(manager.CheckpointUnique(scope.LockTicket, nil)) != 1 {
		t.Fatal("accepted unique claim was lost or skipped claim leaked")
	}
	failure := errors.New("candidate statement rollback")
	if err := scope.Finish(failure); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if got := advisoryUnitHold(session, key); got != want {
		t.Fatal("statement Finish altered session ownership", got, want)
	}
	if len(manager.CheckpointUnique(scope.LockTicket, nil)) != 0 {
		t.Fatal("finished statement retained unique claims")
	}
	for range 2 {
		advisoryUnitUnlock(t, session, key, lock.AdvisoryExclusive, true)
		advisoryUnitUnlock(t, session, key, lock.AdvisoryShare, true)
	}
	if got := advisoryUnitHold(session, key); got != (lock.Hold{}) {
		t.Fatal("explicit advisory cleanup retained counts", got)
	}
}
