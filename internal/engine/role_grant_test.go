package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

// These fixtures exercise the private catalog planner directly, not public SQL.
func objectGrantUnitFixture(t *testing.T) (*Engine, *Operation, authorization.PrincipalRef, ids.ObjectAddress) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	reader := roleAttributeCreate(t, e, op, "grant_reader", true, parser.RoleOptions{}).Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900001}
	namespace := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	relation := &catalog.Relation{ObjectAddress: address, ObjectNamespace: namespace, ObjectName: "grant_unit", Owner: op.RoleState().CurrentUser, Columns: []catalog.Column{{Name: "a"}, {Name: "b"}}, Index: map[string]int{"a": 0, "b": 1}, Attributes: []int32{1, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}}
	e.catalog.state = catalog.CloneState(e.catalog.state)
	e.catalog.state.SetDescriptor(address, relation)
	e.catalog.state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: relation.Name()}, address)
	return e, op, reader, address
}
func applyObjectGrantUnit(e *Engine, op *Operation, s *parser.ObjectGrant) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, err := e.authorizationLocked(op)
	if err != nil {
		return err
	}
	_, err = e.objectGrantLocked(op, v, s)
	return err
}
func objectGrantUnitRequest() *parser.ObjectGrant {
	return &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select", Columns: []parser.Identifier{{Text: "b"}}}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "grant_unit"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}}
}

func TestObjectGrantSequencePublication(t *testing.T) {
	e, op, reader, address := objectGrantUnitFixture(t)
	original := e.catalog.state.MustRelation(address)
	original.RelationKind = catalog.SequenceRelation
	original.SequencePlanStamp = 1
	generator := &sequenceGenerator{last: 11}
	e.storage.sequences[address] = generator
	request := objectGrantUnitRequest()
	request.Kind = "sequence"
	request.Privileges = []parser.GrantPrivilege{{All: true}}
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.catalog.state.MustRelation(address)
	mask := authorization.DefaultMask(authorization.DefaultSequences)
	if next == original || !next.Security.ACL.Explicit || len(next.Security.ACL.Entries) != 2 || next.Security.ACL.Entries[0].Privileges != mask || next.Security.ACL.Entries[0].GrantOptions != 0 {
		t.Fatal("wrong sequence baseline", next.Security.ACL)
	}
	if authorization.ACLRights(next.Security.ACL, next.Owner, []authorization.PrincipalRef{reader}, mask, false) != mask {
		t.Fatal("sequence ALL omitted rights", next.Security.ACL)
	}
	if next.SequencePlanStamp == original.SequencePlanStamp || e.storage.sequences[address] != generator || generator.last != 11 || original.Security.ACL.Explicit {
		t.Fatal("ACL publication changed generator or retained descriptor")
	}
	before, roles := e.catalog.state, e.roles
	request.Relations = append(request.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
	if err := applyObjectGrantUnit(e, op, request); err == nil || e.catalog.state != before || e.roles != roles {
		t.Fatal("late target error published sequence ACL", err)
	}
}

func TestObjectGrantAtomicPublicationAndStableSubID(t *testing.T) {
	e, op, reader, address := objectGrantUnitFixture(t)
	before := e.catalog.state
	roles := e.roles
	failed := objectGrantUnitRequest()
	failed.Relations = append(failed.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
	if err := applyObjectGrantUnit(e, op, failed); err == nil {
		t.Fatal("missing target accepted")
	}
	if e.catalog.state != before || e.roles != roles {
		t.Fatal("late failure published prefix")
	}
	request := objectGrantUnitRequest()
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.catalog.state.MustRelation(address)
	if next == before.Get(address) || next.Security.ColumnACL[3].Entries[0].Grantee.Role != reader || len(next.Security.ColumnACL[1].Entries) != 0 || e.roles.revision != roles.revision+1 {
		t.Fatal("wrong immutable/stable publication", next)
	}
	if len(before.MustRelation(address).Security.ColumnACL) != 0 {
		t.Fatal("retained descriptor mutated")
	}
	stableState, stableRoles := e.catalog.state, e.roles
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state != stableState || e.roles != stableRoles {
		t.Fatal("no-op advanced revisions")
	}
	request.Revoke = true
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	if len(e.catalog.state.MustRelation(address).Security.ColumnACL[3].Entries) != 0 || len(next.Security.ColumnACL[3].Entries) != 1 {
		t.Fatal("revoke mutated old snapshot")
	}
}

func TestObjectGrantColumnTableIndependence(t *testing.T) {
	e, op, reader, address := objectGrantUnitFixture(t)
	column := objectGrantUnitRequest()
	if err := applyObjectGrantUnit(e, op, column); err != nil {
		t.Fatal(err)
	}
	table := objectGrantUnitRequest()
	table.Privileges[0].Columns = nil
	if err := applyObjectGrantUnit(e, op, table); err != nil {
		t.Fatal(err)
	}
	column.Revoke = true
	if err := applyObjectGrantUnit(e, op, column); err != nil {
		t.Fatal(err)
	}
	relation := e.catalog.state.MustRelation(address)
	if authorization.ACLRights(relation.Security.ACL, relation.Owner, []authorization.PrincipalRef{reader}, authorization.TableGrantPrivileges, false)&authorization.SelectPrivilege == 0 {
		t.Fatal("column revoke negated table grant")
	}
	if !relation.Security.ACL.Explicit || len(relation.Security.ACL.Entries) != 2 {
		t.Fatal("table owner defaults not materialized", relation.Security.ACL)
	}
	table.Revoke = true
	if err := applyObjectGrantUnit(e, op, table); err != nil {
		t.Fatal(err)
	}
	relation = e.catalog.state.MustRelation(address)
	if authorization.ACLRights(relation.Security.ACL, relation.Owner, []authorization.PrincipalRef{reader}, authorization.TableGrantPrivileges, false) != 0 {
		t.Fatal("table revoke retained source rights")
	}
}

func TestObjectGrantRevisionExhaustionAndSessionFence(t *testing.T) {
	e, op, _, _ := objectGrantUnitFixture(t)
	originalRoles := e.roles
	saturated := *originalRoles
	saturated.revision = ^uint64(0)
	e.roles = &saturated
	before := e.catalog.state
	err := applyObjectGrantUnit(e, op, objectGrantUnitRequest())
	if err == nil || !strings.Contains(err.Error(), "revision exhausted") || e.catalog.state != before || e.roles != &saturated {
		t.Fatal(err, "revision exhaustion published")
	}
	e.roles = originalRoles
	originalContext := op.ctx
	ctx, cancel := context.WithCancel(originalContext)
	cancel()
	op.ctx = ctx
	err = applyObjectGrantUnit(e, op, objectGrantUnitRequest())
	op.ctx = originalContext
	if !errors.Is(err, context.Canceled) || e.catalog.state != before {
		t.Fatal(err, "cancellation published")
	}
	op.session.mu.Lock()
	delete(op.session.operations, op)
	op.session.mu.Unlock()
	err = applyObjectGrantUnit(e, op, objectGrantUnitRequest())
	op.session.mu.Lock()
	op.session.operations[op] = struct{}{}
	op.session.mu.Unlock()
	if err == nil || e.catalog.state != before {
		t.Fatal(err, "invalid operation published")
	}
}

func TestObjectGrantGrantorPrecedence(t *testing.T) {
	e, op, _, _ := objectGrantUnitFixture(t)
	request := objectGrantUnitRequest()
	request.Relations[0].Parts[0].Text = "missing"
	grantor := parser.RoleSpec{Name: "grant_reader"}
	request.Grantor = &grantor
	err := applyObjectGrantUnit(e, op, request)
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "grantor must be current user"}
	if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal(err, want)
	}
	grantor.Name = "missing_grantor"
	err = applyObjectGrantUnit(e, op, request)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42704" {
		t.Fatal("object resolved before grantor", err)
	}
}

func TestObjectGrantIndexOwnershipUsesParentDependency(t *testing.T) {
	e, op, _, address := objectGrantUnitFixture(t)
	parent := e.catalog.state.MustRelation(address)
	index := &catalog.Relation{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900002},
		ObjectNamespace: parent.Namespace(),
		ObjectName:      "grant_unit_b_idx",
		Owner:           parent.Owner,
		RelationKind:    catalog.IndexRelation,
		SQLIndex:        &catalog.IndexDefinition{Parent: address},
	}
	var dependencies []roleDependency
	descriptorRoleDependencies(parent, parent.Owner, &dependencies)
	descriptorRoleDependencies(index, parent.Owner, &dependencies)
	if len(dependencies) != 1 || dependencies[0].address != address || dependencies[0].kind != 'o' {
		t.Fatal("redundant index shared-owner dependency", dependencies)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil || dependencyDescription(auth, dependencies[0]) != "owner of table grant_unit" {
		t.Fatal("parent ownership diagnostic", dependencies, err)
	}
}

func TestObjectGrantMergedColumnNoticeOrder(t *testing.T) {
	e, owner, _, _ := objectGrantUnitFixture(t)
	roleAttributeCreate(t, e, owner, "grant_target", true, parser.RoleOptions{})
	table := objectGrantUnitRequest()
	table.Privileges[0].Columns = nil
	if err := applyObjectGrantUnit(e, owner, table); err != nil {
		t.Fatal(err)
	}
	actor, done := roleOperation(t, e, "grant_reader")
	defer done()
	request := objectGrantUnitRequest()
	request.Grantees[0].Role.Name = "grant_target"
	request.Privileges = []parser.GrantPrivilege{
		{Name: "select", Columns: []parser.Identifier{{Text: "b"}, {Text: "a"}, {Text: "b"}}},
		{Name: "insert", Columns: []parser.Identifier{{Text: "b"}, {Text: "a"}}},
	}
	for _, revoke := range []bool{false, true} {
		request.Revoke = revoke
		before, roles := e.catalog.state, e.roles
		if err := applyObjectGrantUnit(e, actor, request); err != nil {
			t.Fatal(err)
		}
		code, message := "01007", "no privileges were granted"
		if revoke {
			code, message = "01006", "no privileges could be revoked"
		}
		want := []sqlstate.Error{
			{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message + ` for column "a" of relation "grant_unit"`},
			{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message + ` for column "b" of relation "grant_unit"`},
		}
		if got := actor.DetachNotices(); !reflect.DeepEqual(got, want) {
			t.Fatalf("revoke=%v notices %#v want %#v", revoke, got, want)
		}
		if e.catalog.state != before || e.roles != roles {
			t.Fatal("zero-option request published ACLs")
		}
	}
	// Signed ordering also retains the table warning first, then negative
	// system attributes and positive live attributes across a dropped gap.
	request.Privileges = []parser.GrantPrivilege{{Name: "select"}}
	before, roles := e.catalog.state, e.roles
	if err := applyObjectGrantUnit(e, actor, request); err != nil {
		t.Fatal(err)
	}
	want := []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `no privileges could be revoked for "grant_unit"`}}
	for _, column := range []string{"tableoid", "cmax", "xmax", "cmin", "xmin", "ctid", "a", "b"} {
		want = append(want, sqlstate.Error{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01006", Message: `no privileges could be revoked for column "` + column + `" of relation "grant_unit"`})
	}
	if got := actor.DetachNotices(); !reflect.DeepEqual(got, want) {
		t.Fatalf("signed attribute notices %#v want %#v", got, want)
	}
	if e.catalog.state != before || e.roles != roles {
		t.Fatal("zero-option table revoke published ACLs")
	}
}

func TestObjectGrantMergedColumnMaskWarnings(t *testing.T) {
	column := []parser.Identifier{{Text: "b"}}
	cases := []struct {
		name       string
		privileges []parser.GrantPrivilege
		partial    bool
	}{
		{name: "enumerated", privileges: []parser.GrantPrivilege{
			{Name: "select", Columns: column},
			{Name: "insert", Columns: []parser.Identifier{{Text: "b"}, {Text: "b"}}},
			{Name: "update", Columns: column},
			{Name: "references", Columns: column},
			{Name: "select", Columns: column},
		}},
		{name: "all", privileges: []parser.GrantPrivilege{{All: true, Columns: []parser.Identifier{{Text: "b"}, {Text: "b"}}}}},
		{name: "incomplete", privileges: []parser.GrantPrivilege{{Name: "select", Columns: column}, {Name: "insert", Columns: column}}, partial: true},
	}
	for _, options := range []bool{false, true} {
		for _, tc := range cases {
			name := tc.name + "/no-options"
			if options {
				name = tc.name + "/select-options"
			}
			t.Run(name, func(t *testing.T) {
				e, owner, source, address := objectGrantUnitFixture(t)
				target := roleAttributeCreate(t, e, owner, "grant_target", true, parser.RoleOptions{}).Ref
				table := objectGrantUnitRequest()
				table.Privileges[0].Columns = nil
				table.GrantOption = options
				if err := applyObjectGrantUnit(e, owner, table); err != nil {
					t.Fatal(err)
				}
				actor, done := roleOperation(t, e, "grant_reader")
				defer done()
				request := objectGrantUnitRequest()
				request.Grantees[0].Role.Name = "grant_target"
				request.Privileges = tc.privileges
				for _, revoke := range []bool{false, true} {
					request.Revoke = revoke
					before, roles := e.catalog.state, e.roles
					if err := applyObjectGrantUnit(e, actor, request); err != nil {
						t.Fatal(err)
					}
					var want []sqlstate.Error
					if !options || tc.partial {
						code, message := "01007", "no privileges were granted"
						if revoke {
							code, message = "01006", "no privileges could be revoked"
						}
						if options {
							message = "not all privileges were granted"
							if revoke {
								message = "not all privileges could be revoked"
							}
						}
						want = []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: code, Message: message + ` for column "b" of relation "grant_unit"`}}
					}
					if got := actor.DetachNotices(); !reflect.DeepEqual(got, want) {
						t.Fatalf("revoke=%v notices %#v want %#v", revoke, got, want)
					}
					relation := e.catalog.state.MustRelation(address)
					if !reflect.DeepEqual(relation.Security.ACL, before.MustRelation(address).Security.ACL) || len(relation.Security.ColumnACL[1].Entries) != 0 {
						t.Fatal("column request changed table or another stable attribute")
					}
					if !options {
						if e.catalog.state != before || e.roles != roles {
							t.Fatal("zero-option request published")
						}
					} else if revoke {
						if len(relation.Security.ColumnACL[3].Entries) != 0 {
							t.Fatal("revoke retained selected source rights", relation.Security.ColumnACL)
						}
					} else {
						wantACL := []authorization.ACLEntry{objectACLUnitEntry(source, target, authorization.SelectPrivilege, 0)}
						if !reflect.DeepEqual(relation.Security.ColumnACL[3].Entries, wantACL) {
							t.Fatal("wrong source, eligible mask or stable attribute", relation.Security.ColumnACL)
						}
					}
				}
			})
		}
	}
}
