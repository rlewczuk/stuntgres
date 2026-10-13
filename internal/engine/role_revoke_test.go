package engine

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func objectRevokeUnitFixture(t *testing.T) (*Engine, *Operation, authorization.PrincipalRef, authorization.PrincipalRef, authorization.PrincipalRef, ObjectAddress) {
	t.Helper()
	e, op, reader, address := objectGrantUnitFixture(t)
	target := roleAttributeCreate(t, e, op, "revoke_target", true, parser.RoleOptions{}).Ref
	leaf := roleAttributeCreate(t, e, op, "revoke_leaf", true, parser.RoleOptions{}).Ref
	old := e.state.descriptors[address].(*relationDescriptor)
	next := *old
	next.acl = authorization.MaterializeRelationACL(authorization.ACLValue{}, old.owner)
	next.acl.Entries = append(next.acl.Entries, objectACLUnitEntry(old.owner, reader, authorization.SelectPrivilege, authorization.SelectPrivilege))
	next.columnACL = map[int32]authorization.ACLValue{
		1: {Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(old.owner, target, authorization.UpdatePrivilege, 0)}},
		3: {Explicit: true, Entries: []authorization.ACLEntry{
			objectACLUnitEntry(old.owner, reader, authorization.SelectPrivilege, authorization.SelectPrivilege),
			objectACLUnitEntry(reader, target, authorization.SelectPrivilege, authorization.SelectPrivilege),
			objectACLUnitEntry(target, leaf, authorization.SelectPrivilege, 0),
		}},
	}
	e.state = cloneCatalog(e.state)
	e.state.descriptors[address] = &next
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
			before, roles := e.state, e.roles
			old := before.descriptors[address].(*relationDescriptor)
			tableBefore := slices.Clone(old.acl.Entries)
			columnBefore := slices.Clone(old.columnACL[3].Entries)
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
			if !errors.As(err, &diagnostic) || diagnostic.Code != code || e.state != before || e.roles != roles || !slices.Equal(old.acl.Entries, tableBefore) || !slices.Equal(old.columnACL[3].Entries, columnBefore) {
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
	before, roles := e.state, e.roles
	old := before.descriptors[address].(*relationDescriptor)
	request := objectRevokeUnitRequest()
	request.Relations = append(request.Relations, request.Relations[0])
	request.Grantees = append(request.Grantees, request.Grantees[0])
	request.Privileges = append(request.Privileges, parser.GrantPrivilege{Name: "select", Columns: []parser.Identifier{{Text: "b"}, {Text: "b"}}})
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.state.descriptors[address].(*relationDescriptor)
	wantTable := []authorization.ACLEntry{old.acl.Entries[0], objectACLUnitEntry(old.owner, reader, authorization.SelectPrivilege, 0)}
	wantColumn := []authorization.ACLEntry{objectACLUnitEntry(old.owner, reader, authorization.SelectPrivilege, 0)}
	if e.state == before || next == old || e.roles.revision != roles.revision+1 || !slices.Equal(next.acl.Entries, wantTable) || !slices.Equal(next.columnACL[3].Entries, wantColumn) || !reflect.DeepEqual(next.columnACL[1], old.columnACL[1]) {
		t.Fatal("duplicate/stable/per-column publication", next, e.roles.revision, roles.revision)
	}
	if old.columnACL[3].Entries[1].Grantee.Role != target || old.acl.Entries[1].GrantOptions != authorization.SelectPrivilege {
		t.Fatal("retained original mutated", old)
	}
	stable, stableRoles := e.state, e.roles
	if err := applyObjectGrantUnit(e, op, request); err != nil || e.state != stable || e.roles != stableRoles {
		t.Fatal("explicit no-op advanced publication", err)
	}
	// Column-only plain removal cannot subtract the surviving table privilege.
	column := objectRevokeUnitRequest()
	column.OptionOnly = false
	column.Privileges[0].Columns = []parser.Identifier{{Text: "b"}}
	if err := applyObjectGrantUnit(e, op, column); err != nil {
		t.Fatal(err)
	}
	next = e.state.descriptors[address].(*relationDescriptor)
	if len(next.columnACL[3].Entries) != 0 || !slices.Equal(next.acl.Entries, wantTable) {
		t.Fatal("column revoke changed table or retained grant", next)
	}
}

func TestObjectRevokeNoopPublication(t *testing.T) {
	for _, public := range []bool{false, true} {
		e, op, _, address := objectGrantUnitFixture(t)
		before, roles := e.state, e.roles
		request := objectRevokeUnitRequest()
		request.Grantees = []parser.GrantGrantee{{Public: public, Role: parser.RoleSpec{Identity: "current"}}}
		if err := applyObjectGrantUnit(e, op, request); err != nil {
			t.Fatal(err)
		}
		next := e.state.descriptors[address].(*relationDescriptor)
		if e.state == before || e.roles.revision != roles.revision+1 || !reflect.DeepEqual(next.acl, authorization.MaterializeRelationACL(authorization.ACLValue{}, next.owner)) || len(next.columnACL) != 0 {
			t.Fatal("absent table defaults not explicitly materialized", next)
		}
		stable, stableRoles := e.state, e.roles
		if err := applyObjectGrantUnit(e, op, request); err != nil || e.state != stable || e.roles != stableRoles || len(op.DetachNotices()) != 0 {
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
			before, roles := e.state, e.roles
			err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest())
			if err == nil || e.state != before || e.roles != roles || failure == "revision" && !strings.Contains(err.Error(), "revision exhausted") {
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
		before, roles := e.state, e.roles
		originalContext := op.ctx
		ctx, cancel := context.WithCancel(originalContext)
		op.ctx = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
		err := applyObjectGrantUnit(e, op, objectRevokeUnitRequest())
		op.ctx = originalContext
		cancel()
		if !errors.Is(err, context.Canceled) || e.state != before || e.roles != roles {
			t.Fatal("canceled staged revoke published", at, err)
		}
	}
}
