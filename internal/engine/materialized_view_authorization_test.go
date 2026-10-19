package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
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
	baseOID, err := e.catalog.allocateOIDLocked()
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	base := *d
	base.ObjectAddress = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: baseOID}
	base.ObjectName, base.RelationKind, base.View = "unit_base", catalog.TableRelation, nil
	base.Security.ACL = authorization.ACLValue{Explicit: true}
	if err := stage.Create(&base); err != nil {
		t.Fatal(err)
	}
	d.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.SelectPrivilege}}}
	auth.catalog, auth.state.CurrentUser = e.stageRead(stage), reader
	auth.recaptureCatalog(auth.catalog)
	baseRef := e.catalog.Domain.Ref(auth.catalog.Epoch(), base.Address())
	mvRef := e.catalog.Domain.Ref(auth.catalog.Epoch(), d.Address())
	viewOf(d).sources = []viewLeaf{{ref: baseRef, reads: []int32{1}, attributes: []int32{1}}}
	mvSources := []queryLeaf{{ref: mvRef, relation: d, uses: []int{0}}}
	if err := auth.checkQuerySources(mvSources); err != nil {
		t.Fatal("MV SELECT demanded base ACL:", err)
	}
	var denied *sqlstate.Error
	if err := auth.checkQuerySources([]queryLeaf{{ref: baseRef, relation: &base, uses: []int{0}}}); !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for table unit_base" {
		t.Fatalf("base unexpectedly readable: %v", err)
	}
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(auth, auth.state.CurrentUser, mvSources, nil, ids.SessionToken{}, queryRowMark{})
	e.mu.Unlock()
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectRef{mvRef}) {
		t.Fatalf("physical read expanded frozen source references: %v %v", refs, err)
	}
}
