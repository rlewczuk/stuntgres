package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func objectRevokeUnitFixture(t *testing.T) (*core.Engine, *session.Operation, authorization.PrincipalRef, authorization.PrincipalRef, authorization.PrincipalRef, ids.ObjectAddress) {
	t.Helper()
	e, op, reader, address := objectGrantUnitFixture(t)
	target := roleAttributeCreate(t, e, op, "revoke_target", true, parser.RoleOptions{}).Ref
	leaf := roleAttributeCreate(t, e, op, "revoke_leaf", true, parser.RoleOptions{}).Ref
	old := e.Catalog.State.MustRelation(address)
	next := *old
	next.Security.ACL = authorization.MaterializeRelationACL(authorization.ACLValue{}, old.Owner)
	next.Security.ACL.Entries = append(next.Security.ACL.Entries, objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, authorization.SelectPrivilege))
	next.Security.ColumnACL = map[int32]authorization.ACLValue{
		1: {Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(old.Owner, target, authorization.UpdatePrivilege, 0)}},
		3: {Explicit: true, Entries: []authorization.ACLEntry{
			objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, authorization.SelectPrivilege),
			objectACLUnitEntry(reader, target, authorization.SelectPrivilege, authorization.SelectPrivilege),
			objectACLUnitEntry(target, leaf, authorization.SelectPrivilege, 0),
		}},
	}
	e.Catalog.PublishLocked(catalog.CloneState(e.Catalog.State))
	e.Catalog.State.SetDescriptor(address, &next)
	return e, op, reader, target, leaf, address
}

func objectRevokeUnitRequest() *parser.ObjectGrant {
	request := objectGrantUnitRequest()
	request.Privileges[0].Columns = nil
	request.Revoke, request.OptionOnly, request.Cascade = true, true, true
	return request
}

func TestObjectRevokeAtomicity(t *testing.T) {
	for _, failure := range []string{"restrict-column", "late-object", "late-column", "late-recipient"} {
		t.Run(failure, func(t *testing.T) {
			e, op, _, _, _, address := objectRevokeUnitFixture(t)
			before, roles := e.Catalog.State, e.RoleStore.Published
			old := before.MustRelation(address)
			tableBefore := slices.Clone(old.Security.ACL.Entries)
			columnBefore := slices.Clone(old.Security.ColumnACL[3].Entries)
			request := objectRevokeUnitRequest()
			code := ""
			switch failure {
			case "restrict-column":
				request.Cascade = false
				code = "2BP01"
			case "late-object":
				request.Relations = append(request.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
				code = "42P01"
			case "late-column":
				request.Privileges = append(request.Privileges, parser.GrantPrivilege{Name: "select", Columns: []parser.Identifier{{Text: "missing"}}})
				code = "42703"
			case "late-recipient":
				request.Grantees = append(request.Grantees, parser.GrantGrantee{Role: parser.RoleSpec{Name: "missing"}})
				code = "42704"
			}
			err := applyObjectGrantUnit(e, op, request)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != code || e.Catalog.State != before || e.RoleStore.Published != roles || !slices.Equal(old.Security.ACL.Entries, tableBefore) || !slices.Equal(old.Security.ColumnACL[3].Entries, columnBefore) {
				t.Fatal("failed revoke changed catalog or snapshot", failure, err)
			}
			if code == "2BP01" && (diagnostic.Message != "dependent privileges exist" || diagnostic.Hint != "Use CASCADE to revoke them too.") {
				t.Fatal("RESTRICT diagnostic", diagnostic)
			}
		})
	}
}

func TestObjectRevokeColumnSelection(t *testing.T) {
	e, op, reader, target, _, address := objectRevokeUnitFixture(t)
	before, roles := e.Catalog.State, e.RoleStore.Published
	old := before.MustRelation(address)
	request := objectRevokeUnitRequest()
	request.Relations = append(request.Relations, request.Relations[0])
	request.Grantees = append(request.Grantees, request.Grantees[0])
	request.Privileges = append(request.Privileges, parser.GrantPrivilege{Name: "select", Columns: []parser.Identifier{{Text: "b"}, {Text: "b"}}})
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.Catalog.State.MustRelation(address)
	wantTable := []authorization.ACLEntry{old.Security.ACL.Entries[0], objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, 0)}
	wantColumn := []authorization.ACLEntry{objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, 0)}
	if e.Catalog.State == before || next == old || e.RoleStore.Published.Revision != roles.Revision+1 || !slices.Equal(next.Security.ACL.Entries, wantTable) || !slices.Equal(next.Security.ColumnACL[3].Entries, wantColumn) || !reflect.DeepEqual(next.Security.ColumnACL[1], old.Security.ColumnACL[1]) {
		t.Fatal("duplicate/stable/per-column publication", next, e.RoleStore.Published.Revision, roles.Revision)
	}
	if old.Security.ColumnACL[3].Entries[1].Grantee.Role != target || old.Security.ACL.Entries[1].GrantOptions != authorization.SelectPrivilege {
		t.Fatal("retained original mutated", old)
	}
	stable, stableRoles := e.Catalog.State, e.RoleStore.Published
	if err := applyObjectGrantUnit(e, op, request); err != nil || e.Catalog.State != stable || e.RoleStore.Published != stableRoles {
		t.Fatal("explicit no-op advanced publication", err)
	}
	// Column-only plain removal cannot subtract the surviving table privilege.
	column := objectRevokeUnitRequest()
	column.OptionOnly = false
	column.Privileges[0].Columns = []parser.Identifier{{Text: "b"}}
	if err := applyObjectGrantUnit(e, op, column); err != nil {
		t.Fatal(err)
	}
	next = e.Catalog.State.MustRelation(address)
	if len(next.Security.ColumnACL[3].Entries) != 0 || !slices.Equal(next.Security.ACL.Entries, wantTable) {
		t.Fatal("column revoke changed table or retained grant", next)
	}
}

func TestObjectRevokeNoopPublication(t *testing.T) {
	for _, public := range []bool{false, true} {
		e, op, _, address := objectGrantUnitFixture(t)
		before, roles := e.Catalog.State, e.RoleStore.Published
		request := objectRevokeUnitRequest()
		request.Grantees = []parser.GrantGrantee{{Public: public, Role: parser.RoleSpec{Identity: "current"}}}
		if err := applyObjectGrantUnit(e, op, request); err != nil {
			t.Fatal(err)
		}
		next := e.Catalog.State.MustRelation(address)
		if e.Catalog.State == before || e.RoleStore.Published.Revision != roles.Revision+1 || !reflect.DeepEqual(next.Security.ACL, authorization.MaterializeRelationACL(authorization.ACLValue{}, next.Owner)) || len(next.Security.ColumnACL) != 0 {
			t.Fatal("absent table defaults not explicitly materialized", next)
		}
		stable, stableRoles := e.Catalog.State, e.RoleStore.Published
		if err := applyObjectGrantUnit(e, op, request); err != nil || e.Catalog.State != stable || e.RoleStore.Published != stableRoles || len(op.DetachNotices()) != 0 {
			t.Fatal("repeat PUBLIC/owner no-op was not quiet", err)
		}
	}
}

func TestObjectRevokePublicationFence(t *testing.T) {
	for _, failure := range []string{"revision", "operation"} {
		t.Run(failure, func(t *testing.T) {
			e, op, _, _, _, _ := objectRevokeUnitFixture(t)
			if failure == "revision" {
				e.RoleStore.PublishLocked(e.RoleStore.Published.Advance())
				saturated := *e.RoleStore.Published
				saturated.Revision = ^uint64(0)
				e.RoleStore.PublishLocked(&saturated)
			} else {
				op.Session.Mu.Lock()
				delete(op.Session.Operations, op)
				op.Session.Mu.Unlock()
				defer func() {
					op.Session.Mu.Lock()
					op.Session.Operations[op] = struct{}{}
					op.Session.Mu.Unlock()
				}()
			}
			before, roles := e.Catalog.State, e.RoleStore.Published
			err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest())
			if err == nil || e.Catalog.State != before || e.RoleStore.Published != roles || failure == "revision" && !strings.Contains(err.Error(), "revision exhausted") {
				t.Fatal("revoke escaped publication fence", failure, err)
			}
		})
	}
}

func TestObjectRevokeCancellationPublication(t *testing.T) {
	e, op, _, _, _, _ := objectRevokeUnitFixture(t)
	originalContext := op.Ctx
	ctx, cancel := context.WithCancel(originalContext)
	counter := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 1 << 30}
	op.Ctx = counter
	if err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest()); err != nil {
		t.Fatal("calibrating revoke checkpoints", err)
	}
	op.Ctx = originalContext
	cancel()
	for at := int32(1); at <= counter.checks.Load(); at++ {
		e, op, _, _, _, _ := objectRevokeUnitFixture(t)
		before, roles := e.Catalog.State, e.RoleStore.Published
		originalContext := op.Ctx
		ctx, cancel := context.WithCancel(originalContext)
		op.Ctx = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest())
		op.Ctx = originalContext
		cancel()
		if !errors.Is(err, context.Canceled) || e.Catalog.State != before || e.RoleStore.Published != roles {
			t.Fatal("canceled staged revoke published", at, err)
		}
	}
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestObjectRevokeCatalogRegistration(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		name := "registered_repeated_target"
		if malformed {
			name = "malformed_view_identity"
		}
		t.Run(name, func(t *testing.T) {
			e, op, reader, _ := objectGrantUnitFixture(t)
			request := objectGrantUnitRequest()
			request.Privileges[0].Columns = nil
			request.Relations = []parser.ObjectName{{Parts: []parser.Identifier{{Text: "pg_catalog"}, {Text: "pg_authid"}}}}
			if err := applyObjectGrantUnit(e, op, request); err != nil {
				t.Fatal("initial registered grant", err)
			}
			address := e.CatalogRead().NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: "pg_authid"})
			if malformed {
				next := *e.Catalog.State.MustRelation(address)
				next.CatalogView = !next.CatalogView
				state := catalog.CloneState(e.Catalog.State)
				state.SetDescriptor(address, &next)
				e.Catalog.PublishLocked(state)
			}
			before, roles := e.CatalogRead(), e.RoleStore.Published
			old := before.MustRelation(address)
			request.Revoke = true
			request.Relations = append(request.Relations, request.Relations[0])
			err := applyObjectGrantUnit(e, op, request)
			if malformed {
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || e.Catalog.State != before.State() || e.RoleStore.Published != roles {
					t.Fatal("malformed registered target admitted or published", err)
				}
				return
			}
			if err != nil {
				t.Fatal("registered repeated revoke", err)
			}
			after := e.CatalogRead()
			next := after.MustRelation(address)
			if next == old || !pgcatalog.RegisteredSource(before, old) || !pgcatalog.RegisteredSource(after, next) || pgcatalog.RegisteredSource(after, old) || pgcatalog.RegisteredSource(before, next) {
				t.Fatal("ACL publication lost captured source identity")
			}
			principals := []authorization.PrincipalRef{reader}
			if authorization.ACLRights(old.Security.ACL, old.Owner, principals, authorization.SelectPrivilege, false) == 0 || authorization.ACLRights(next.Security.ACL, next.Owner, principals, authorization.SelectPrivilege, false) != 0 {
				t.Fatal("revoke mutated retained ACL or failed to remove grant")
			}
		})
	}
}
