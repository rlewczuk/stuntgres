package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Native SQL GRANT is deliberately not expanded for this part. Exercise the
// actual private ACL/source admission and physical-reference collector instead.
func TestMaterializedViewAuthorization(t *testing.T) {
	e := New()
	op, done, stage, d := materializedUnitStage(t, e)
	defer done()
	reader := e.authorizationDomain.Principal(99)
	e.mu.Lock()
	putFixtureRole(e.roles, authorization.RoleDescriptor{Ref: reader, Name: "mv_unit_reader"})

	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	baseOID, err := e.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	base := *d
	base.address = ObjectAddress{ClassID: RelationClassID, ObjectID: baseOID}
	base.name, base.relationKind, base.view = "unit_base", tableRelation, nil
	base.acl = authorization.ACLValue{Explicit: true}
	if err := stage.Create(&base); err != nil {
		t.Fatal(err)
	}
	d.acl = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}
	auth.catalog, auth.state.CurrentUser = e.stageRead(stage), reader
	auth.recaptureCatalog(auth.catalog)
	baseRef := e.domain.Ref(auth.catalog.Epoch(), base.address)
	mvRef := e.domain.Ref(auth.catalog.Epoch(), d.address)
	d.view.sources = []viewLeaf{{ref: baseRef, reads: []int32{1}, attributes: []int32{1}}}
	mvSources := []queryLeaf{{ref: mvRef, relation: d, uses: []int{0}}}
	if err := auth.checkQuerySources(mvSources); err != nil {
		t.Fatal("MV SELECT demanded base ACL:", err)
	}
	var denied *sqlstate.Error
	if err := auth.checkQuerySources([]queryLeaf{{ref: baseRef, relation: &base, uses: []int{0}}}); !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for table unit_base" {
		t.Fatalf("base unexpectedly readable: %v", err)
	}
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(auth, auth.state.CurrentUser, mvSources, nil, SessionToken{}, queryRowMark{})
	e.mu.Unlock()
	if err != nil || !reflect.DeepEqual(refs, []ObjectRef{mvRef}) {
		t.Fatalf("physical read expanded frozen source references: %v %v", refs, err)
	}
}
