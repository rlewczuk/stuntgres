package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

// These fixtures exercise the private catalog planner directly, not public SQL.
func objectGrantUnitFixture(t *testing.T) (*Engine, *session.Operation, authorization.PrincipalRef, ids.ObjectAddress) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	reader := roleAttributeCreate(t, e, op, "grant_reader", true, parser.RoleOptions{}).Ref
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 900001}
	namespace := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	typ, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing int4 fixture type")
	}
	relation := &catalog.Relation{ObjectAddress: address, ObjectNamespace: namespace, ObjectName: "grant_unit", ObjectRevision: 1, RelationKind: catalog.TableRelation, Owner: op.RoleState().CurrentUser, Columns: []catalog.Column{{Name: "a", Usage: typ.Usage()}, {Name: "b", Usage: typ.Usage()}}, Index: map[string]int{"a": 0, "b": 1}, Attributes: []int32{1, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}}
	e.catalog.state = catalog.CloneState(e.catalog.state)
	e.catalog.state.SetDescriptor(address, relation)
	e.catalog.state.BindName(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: relation.Name()}, address)
	return e, op, reader, address
}
func applyObjectGrantUnit(e *Engine, op *session.Operation, s *parser.ObjectGrant) error {
	_, err := e.Execute(op, s, 0, nil)
	return err
}
func objectGrantUnitRequest() *parser.ObjectGrant {
	return &parser.ObjectGrant{Privileges: []parser.GrantPrivilege{{Name: "select", Columns: []parser.Identifier{{Text: "b"}}}}, Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "grant_unit"}}}}, Grantees: []parser.GrantGrantee{{Role: parser.RoleSpec{Name: "grant_reader"}}}}
}

func TestTransactionalPrivilegesJournal(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "retained"}[retained], func(t *testing.T) {
			e, op, reader, address := objectGrantUnitFixture(t)
			committed, roles := e.catalog.state, e.roles.published
			if _, err := e.Begin(op, session.TxOptions{}); err != nil {
				t.Fatal(err)
			}
			var message *batchUnitMessage
			if retained {
				scope, err := e.StartStatement(op)
				if err != nil {
					t.Fatal(err)
				}
				if err = scope.RetainMessage(); err != nil {
					_ = scope.Finish(err)
					t.Fatal(err)
				}
				message = &batchUnitMessage{scope: scope, op: op}
				defer func() { _ = message.scope.FinishMessage(context.Canceled) }()
			}
			apply := func(statement parser.Statement) error {
				if message != nil {
					_, err := message.step(statement)
					return err
				}
				_, err := e.Execute(op, statement, 0, nil)
				return err
			}
			request := objectGrantUnitRequest()
			if err := apply(request); err != nil {
				t.Fatal(err)
			}
			changes := op.Session.Transactions.Catalog
			if changes == nil || changes.Roles == nil || changes.RolesBase != roles || e.catalog.state != committed || e.roles.published != roles {
				t.Fatal("active ACL utility must retain one private catalog/role pair")
			}
			e.mu.Lock()
			private := e.privateCatalogLocked(changes)
			e.mu.Unlock()
			column := private.State().MustRelation(address).Security.ColumnACL[3]
			if authorization.ACLRights(column, op.RoleState().CurrentUser, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, false) == 0 {
				t.Fatal("private stable column identity lost its grant")
			}
			before := changes.Clone()
			if err := apply(request); err != nil {
				t.Fatal(err)
			}
			if op.Session.Transactions.Catalog.Roles != before.Roles {
				t.Fatal("no-op advanced private role revision")
			}
			mark := parser.Identifier{Text: "acl_mark"}
			if err := apply(&parser.SavepointControl{Kind: parser.CreateSavepoint, Name: mark}); err != nil {
				t.Fatal(err)
			}
			request.Revoke = true
			if err := apply(request); err != nil {
				t.Fatal(err)
			}
			e.mu.Lock()
			suffix := e.privateCatalogLocked(op.Session.Transactions.Catalog)
			e.mu.Unlock()
			if authorization.ACLRights(suffix.State().MustRelation(address).Security.ColumnACL[3], op.RoleState().CurrentUser, []authorization.PrincipalRef{reader}, authorization.SelectPrivilege, false) != 0 {
				t.Fatal("private revoke suffix had no actual effect")
			}
			request.Revoke = false
			request.Relations = append(request.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
			if err := apply(request); err == nil {
				t.Fatal("missing late target accepted")
			}
			for range 2 {
				if err := apply(&parser.SavepointControl{Kind: parser.RollbackSavepoint, Name: mark}); err != nil {
					t.Fatal(err)
				}
				live := op.Session.Transactions.Catalog
				if live == nil || live.Roles != before.Roles {
					t.Fatal("rollback-to lost the live premark authorization")
				}
				e.mu.Lock()
				restored := e.privateCatalogLocked(live)
				e.mu.Unlock()
				if !reflect.DeepEqual(column, restored.State().MustRelation(address).Security.ColumnACL[3]) {
					t.Fatal("rollback-to did not restore the live ACL suffix")
				}
			}
			if err := apply(&parser.SavepointControl{Kind: parser.ReleaseSavepoint, Name: mark}); err != nil {
				t.Fatal(err)
			}
			if err := apply(request); err == nil {
				t.Fatal("unmarked missing late target accepted")
			}
			if e.catalog.state != committed || e.roles.published != roles || op.Session.Transactions.Catalog != nil || changes.Roles != before.Roles || !reflect.DeepEqual(column, private.State().MustRelation(address).Security.ColumnACL[3]) {
				t.Fatal("failed block changed committed or detached private roots")
			}
		})
	}
}

func TestObjectGrantSequencePublication(t *testing.T) {
	e, op, reader, address := objectGrantUnitFixture(t)
	original := e.catalog.state.MustRelation(address)
	options, _, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	original = sequence.NewRelation(sequence.RelationSpec{Address: address, Namespace: original.Namespace(), Name: original.Name(), Revision: 1, Owner: original.Owner, Columns: sequence.Columns(e.CatalogRead()), Options: options})
	e.catalog.state.SetDescriptor(address, original)
	generator := &storage.SequenceGenerator{Last: 11}
	e.mu.Lock()
	e.storage.InstallSequenceLocked(address, generator)
	e.mu.Unlock()
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
	if next.SequencePlanStamp == original.SequencePlanStamp || e.storage.Sequences[address] != generator || generator.Last != 11 || original.Security.ACL.Explicit {
		t.Fatal("ACL publication changed generator or retained descriptor")
	}
	before, roles := e.catalog.state, e.roles.published
	request.Relations = append(request.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
	if err := applyObjectGrantUnit(e, op, request); err == nil || e.catalog.state != before || e.roles.published != roles {
		t.Fatal("late target error published sequence ACL", err)
	}
}

func TestObjectGrantAtomicPublicationAndStableSubID(t *testing.T) {
	e, op, reader, address := objectGrantUnitFixture(t)
	before := e.catalog.state
	roles := e.roles.published
	failed := objectGrantUnitRequest()
	failed.Relations = append(failed.Relations, parser.ObjectName{Parts: []parser.Identifier{{Text: "missing"}}})
	if err := applyObjectGrantUnit(e, op, failed); err == nil {
		t.Fatal("missing target accepted")
	}
	if e.catalog.state != before || e.roles.published != roles {
		t.Fatal("late failure published prefix")
	}
	request := objectGrantUnitRequest()
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	next := e.catalog.state.MustRelation(address)
	if next == before.Get(address) || next.Security.ColumnACL[3].Entries[0].Grantee.Role != reader || len(next.Security.ColumnACL[1].Entries) != 0 || e.roles.published.Revision != roles.Revision+1 {
		t.Fatal("wrong immutable/stable publication", next)
	}
	if len(before.MustRelation(address).Security.ColumnACL) != 0 {
		t.Fatal("retained descriptor mutated")
	}
	stableState, stableRoles := e.catalog.state, e.roles.published
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state != stableState || e.roles.published != stableRoles {
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
	originalRoles := e.roles.published
	saturated := *originalRoles
	saturated.Revision = ^uint64(0)
	e.roles.published = &saturated
	before := e.catalog.state
	err := applyObjectGrantUnit(e, op, objectGrantUnitRequest())
	if err == nil || !strings.Contains(err.Error(), "revision exhausted") || e.catalog.state != before || e.roles.published != &saturated {
		t.Fatal(err, "revision exhaustion published")
	}
	e.roles.published = originalRoles
	originalContext := op.Ctx
	ctx, cancel := context.WithCancel(originalContext)
	cancel()
	op.Ctx = ctx
	err = applyObjectGrantUnit(e, op, objectGrantUnitRequest())
	op.Ctx = originalContext
	if !errors.Is(err, context.Canceled) || e.catalog.state != before {
		t.Fatal(err, "cancellation published")
	}
	op.Session.Mu.Lock()
	delete(op.Session.Operations, op)
	op.Session.Mu.Unlock()
	err = applyObjectGrantUnit(e, op, objectGrantUnitRequest())
	op.Session.Mu.Lock()
	op.Session.Operations[op] = struct{}{}
	op.Session.Mu.Unlock()
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
		SQLIndex:        &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: address},
	}
	var dependencies []access.RoleDependency
	access.DescriptorRoleDependencies(parent, parent.Owner, &dependencies)
	access.DescriptorRoleDependencies(index, parent.Owner, &dependencies)
	if len(dependencies) != 1 || dependencies[0].Address != address || dependencies[0].Kind != 'o' {
		t.Fatal("redundant index shared-owner dependency", dependencies)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	e.mu.Unlock()
	if err != nil || (ddlHost{e: e}).DescribeRoleDependency(auth, dependencies[0]) != "owner of table grant_unit" {
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
		before, roles := e.catalog.state, e.roles.published
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
		if e.catalog.state != before || e.roles.published != roles {
			t.Fatal("zero-option request published ACLs")
		}
	}
	// Signed ordering also retains the table warning first, then negative
	// system attributes and positive live attributes across a dropped gap.
	request.Privileges = []parser.GrantPrivilege{{Name: "select"}}
	before, roles := e.catalog.state, e.roles.published
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
	if e.catalog.state != before || e.roles.published != roles {
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
					before, roles := e.catalog.state, e.roles.published
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
						if e.catalog.state != before || e.roles.published != roles {
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

// The plan returns the warnings raised before its error and emits none
// itself; objectGrantLocked emits them ahead of the error.
func TestObjectGrantPlanWarningsBeforeError(t *testing.T) {
	e, op, _, address := objectGrantUnitFixture(t)
	e.catalog.state.MustRelation(address).RelationKind = catalog.SequenceRelation
	request := objectGrantUnitRequest()
	request.Privileges = []parser.GrantPrivilege{{Name: "insert"}, {Name: "select", Columns: []parser.Identifier{{Text: "missing"}}}}
	e.mu.Lock()
	v, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	req, err := role.ObjectGrantRequest(ddlHost{e: e, op: op}, v, request)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	plan, warnings, err := role.ObjectGrantPlan(op.Context(), v.View, request, req, e.catalog.revisionLocked)
	e.mu.Unlock()
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42703" || plan.Candidate != nil {
		t.Fatal("missing column did not fail the plan", err, plan.Candidate)
	}
	if len(warnings) != 1 || warnings[0].Code != "0LP01" || !strings.Contains(warnings[0].Message, "only supports USAGE, SELECT, and UPDATE") {
		t.Fatal("warning before the error was not returned", warnings)
	}
	if notices := op.DetachNotices(); len(notices) != 0 {
		t.Fatal("plan emitted notices", notices)
	}
	before := e.catalog.state
	if err := applyObjectGrantUnit(e, op, request); !errors.As(err, &diagnostic) || diagnostic.Code != "42703" || e.catalog.state != before {
		t.Fatal("failed grant published or lost its error", err)
	}
	if notices := op.DetachNotices(); len(notices) != 1 || notices[0] != warnings[0] {
		t.Fatal("objectGrantLocked did not emit the warning before the error", notices)
	}
}

func TestTransactionalPrivilegesNamespace(t *testing.T) {
	e, op, _, _ := objectGrantUnitFixture(t)
	request := &parser.ObjectGrant{Kind: "schema", Relations: []parser.ObjectName{{Parts: []parser.Identifier{{Text: "public"}}}}, Privileges: []parser.GrantPrivilege{{Name: "usage"}}, Grantees: objectGrantUnitRequest().Grantees}
	before, roles := e.catalog.state, e.roles.published
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	after := e.catalog.state
	if after.NamespaceGeneration() != before.NamespaceGeneration()+1 || e.roles.published == roles {
		t.Fatal("schema ACL lost its single canonical invalidation")
	}
	if err := applyObjectGrantUnit(e, op, request); err != nil || e.catalog.state != after {
		t.Fatal("schema ACL no-op invalidated", err)
	}
	ref, err := e.Begin(op, session.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request.Revoke = true
	if err := applyObjectGrantUnit(e, op, request); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state != after {
		t.Fatal("private schema ACL invalidated committed root")
	}
	if _, err := e.Rollback(op, ref); err != nil || e.catalog.state != after {
		t.Fatal("schema ACL rollback published", err)
	}
	saturated := catalog.CloneState(after)
	saturated.SetNamespaceGeneration(^uint64(0))
	e.catalog.state = saturated
	roles = e.roles.published
	if err := applyObjectGrantUnit(e, op, request); err == nil || !strings.Contains(err.Error(), "namespace invalidation generation exhausted") || e.catalog.state != saturated || e.roles.published != roles {
		t.Fatal("schema namespace overflow published", err)
	}
}
