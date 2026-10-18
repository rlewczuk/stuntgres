package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func objectRevokeUnitFixture(t *testing.T) (*Engine, *Operation, authorization.PrincipalRef, authorization.PrincipalRef, authorization.PrincipalRef, ids.ObjectAddress) {
	t.Helper()
	e, op, reader, address := objectGrantUnitFixture(t)
	target := roleAttributeCreate(t, e, op, "revoke_target", true, parser.RoleOptions{}).Ref
	leaf := roleAttributeCreate(t, e, op, "revoke_leaf", true, parser.RoleOptions{}).Ref
	old := e.catalog.state.MustRelation(address)
	next := *old
	next.ACL = authorization.MaterializeRelationACL(authorization.ACLValue{}, old.Owner)
	next.ACL.Entries = append(next.ACL.Entries, objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, authorization.SelectPrivilege))
	next.ColumnACL = map[int32]authorization.ACLValue{
		1: {Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(old.Owner, target, authorization.UpdatePrivilege, 0)}},
		3: {Explicit: true, Entries: []authorization.ACLEntry{
			objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, authorization.SelectPrivilege),
			objectACLUnitEntry(reader, target, authorization.SelectPrivilege, authorization.SelectPrivilege),
			objectACLUnitEntry(target, leaf, authorization.SelectPrivilege, 0),
		}},
	}
	e.catalog.state = cloneCatalog(e.catalog.state)
	e.catalog.state.setDescriptor(address, &next)
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
			before, roles := e.catalog.state, e.roles
			old := before.MustRelation(address)
			tableBefore := slices.Clone(old.ACL.Entries)
			columnBefore := slices.Clone(old.ColumnACL[3].Entries)
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
			if !errors.As(err, &diagnostic) || diagnostic.Code != code || e.catalog.state != before || e.roles != roles || !slices.Equal(old.ACL.Entries, tableBefore) || !slices.Equal(old.ColumnACL[3].Entries, columnBefore) {
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
	before, roles := e.catalog.state, e.roles
	old := before.MustRelation(address)
	request := objectRevokeUnitRequest()
	request.Relations = append(request.Relations, request.Relations[0])
	request.Grantees = append(request.Grantees, request.Grantees[0])
	request.Privileges = append(request.Privileges, parser.GrantPrivilege{Name: "select", Columns: []parser.Identifier{{Text: "b"}, {Text: "b"}}})
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.catalog.state.MustRelation(address)
	wantTable := []authorization.ACLEntry{old.ACL.Entries[0], objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, 0)}
	wantColumn := []authorization.ACLEntry{objectACLUnitEntry(old.Owner, reader, authorization.SelectPrivilege, 0)}
	if e.catalog.state == before || next == old || e.roles.revision != roles.revision+1 || !slices.Equal(next.ACL.Entries, wantTable) || !slices.Equal(next.ColumnACL[3].Entries, wantColumn) || !reflect.DeepEqual(next.ColumnACL[1], old.ColumnACL[1]) {
		t.Fatal("duplicate/stable/per-column publication", next, e.roles.revision, roles.revision)
	}
	if old.ColumnACL[3].Entries[1].Grantee.Role != target || old.ACL.Entries[1].GrantOptions != authorization.SelectPrivilege {
		t.Fatal("retained original mutated", old)
	}
	stable, stableRoles := e.catalog.state, e.roles
	if err := applyObjectGrantUnit(e, op, request); err != nil || e.catalog.state != stable || e.roles != stableRoles {
		t.Fatal("explicit no-op advanced publication", err)
	}
	// Column-only plain removal cannot subtract the surviving table privilege.
	column := objectRevokeUnitRequest()
	column.OptionOnly = false
	column.Privileges[0].Columns = []parser.Identifier{{Text: "b"}}
	if err := applyObjectGrantUnit(e, op, column); err != nil {
		t.Fatal(err)
	}
	next = e.catalog.state.MustRelation(address)
	if len(next.ColumnACL[3].Entries) != 0 || !slices.Equal(next.ACL.Entries, wantTable) {
		t.Fatal("column revoke changed table or retained grant", next)
	}
}

func TestObjectRevokeNoopPublication(t *testing.T) {
	for _, public := range []bool{false, true} {
		e, op, _, address := objectGrantUnitFixture(t)
		before, roles := e.catalog.state, e.roles
		request := objectRevokeUnitRequest()
		request.Grantees = []parser.GrantGrantee{{Public: public, Role: parser.RoleSpec{Identity: "current"}}}
		if err := applyObjectGrantUnit(e, op, request); err != nil {
			t.Fatal(err)
		}
		next := e.catalog.state.MustRelation(address)
		if e.catalog.state == before || e.roles.revision != roles.revision+1 || !reflect.DeepEqual(next.ACL, authorization.MaterializeRelationACL(authorization.ACLValue{}, next.Owner)) || len(next.ColumnACL) != 0 {
			t.Fatal("absent table defaults not explicitly materialized", next)
		}
		stable, stableRoles := e.catalog.state, e.roles
		if err := applyObjectGrantUnit(e, op, request); err != nil || e.catalog.state != stable || e.roles != stableRoles || len(op.DetachNotices()) != 0 {
			t.Fatal("repeat PUBLIC/owner no-op was not quiet", err)
		}
	}
}

func TestObjectRevokePublicationFence(t *testing.T) {
	for _, failure := range []string{"revision", "operation"} {
		t.Run(failure, func(t *testing.T) {
			e, op, _, _, _, _ := objectRevokeUnitFixture(t)
			if failure == "revision" {
				e.roles = advanceRoleRevision(e.roles)
				saturated := *e.roles
				saturated.revision = ^uint64(0)
				e.roles = &saturated
			} else {
				op.session.mu.Lock()
				delete(op.session.operations, op)
				op.session.mu.Unlock()
				defer func() {
					op.session.mu.Lock()
					op.session.operations[op] = struct{}{}
					op.session.mu.Unlock()
				}()
			}
			before, roles := e.catalog.state, e.roles
			err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest())
			if err == nil || e.catalog.state != before || e.roles != roles || failure == "revision" && !strings.Contains(err.Error(), "revision exhausted") {
				t.Fatal("revoke escaped publication fence", failure, err)
			}
		})
	}
}

func TestObjectRevokeCancellationPublication(t *testing.T) {
	e, op, _, _, _, _ := objectRevokeUnitFixture(t)
	originalContext := op.ctx
	ctx, cancel := context.WithCancel(originalContext)
	counter := &cancelAtErrCheck{Context: ctx, cancel: cancel, at: 1 << 30}
	op.ctx = counter
	if err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest()); err != nil {
		t.Fatal("calibrating revoke checkpoints", err)
	}
	op.ctx = originalContext
	cancel()
	for at := int32(1); at <= counter.checks.Load(); at++ {
		e, op, _, _, _, _ := objectRevokeUnitFixture(t)
		before, roles := e.catalog.state, e.roles
		originalContext := op.ctx
		ctx, cancel := context.WithCancel(originalContext)
		op.ctx = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest())
		op.ctx = originalContext
		cancel()
		if !errors.Is(err, context.Canceled) || e.catalog.state != before || e.roles != roles {
			t.Fatal("canceled staged revoke published", at, err)
		}
	}
}
