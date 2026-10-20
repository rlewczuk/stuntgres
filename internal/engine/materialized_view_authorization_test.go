package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Native SQL GRANT is deliberately not expanded for this part. Exercise the
// actual private ACL/source admission and physical-reference collector instead.
func TestMaterializedViewAuthorization(t *testing.T) {
	e := New()
	op, done, stage, d := materializedUnitStage(t, e)
	defer done()
	reader := e.roles.domain.Principal(99)
	e.mu.Lock()
	putFixtureRole(e.roles.published, authorization.RoleDescriptor{Ref: reader, Name: "mv_unit_reader"})

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
	auth.Catalog, auth.State.CurrentUser = e.stageRead(stage), reader
	auth.Recapture(auth.Catalog)
	baseRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address())
	mvRef := e.catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	viewOf(d).sources = []viewLeaf{{ref: baseRef, reads: []int32{1}, attributes: []int32{1}}}
	mvSources := []queryLeaf{{Ref: mvRef, Relation: d, Uses: []int{0}}}
	if err := auth.checkQuerySources(mvSources); err != nil {
		t.Fatal("MV SELECT demanded base ACL:", err)
	}
	var denied *sqlstate.Error
	if err := auth.checkQuerySources([]queryLeaf{{Ref: baseRef, Relation: &base, Uses: []int{0}}}); !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for table unit_base" {
		t.Fatalf("base unexpectedly readable: %v", err)
	}
	e.mu.Lock()
	refs, err := e.queryTableRefsLocked(auth, auth.State.CurrentUser, mvSources, nil, ids.SessionToken{}, plan.RowMark{})
	e.mu.Unlock()
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectRef{mvRef}) {
		t.Fatalf("physical read expanded frozen source references: %v %v", refs, err)
	}
}
