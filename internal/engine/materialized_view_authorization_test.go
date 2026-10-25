package engine

import (
	"errors"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"testing"
)

// Native SQL GRANT is deliberately not expanded for this part. Exercise the
// actual private ACL/source admission and physical-reference collector instead.
func TestMaterializedViewAuthorization(t *testing.T) {
	e := New()
	op, done, stage, d := materializedUnitStage(t, e)
	defer done()
	reader := e.RoleStore.Domain.Principal(99)
	e.Mu.Lock()
	putFixtureRole(e.RoleStore.Published, authorization.RoleDescriptor{Ref: reader, Name: "mv_unit_reader"})

	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	baseOID, err := e.Catalog.AllocateOIDLocked()
	e.Mu.Unlock()
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
	auth.Catalog, auth.State.CurrentUser = e.StageRead(stage), reader
	auth.Recapture(auth.Catalog)
	baseRef := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), base.Address())
	mvRef := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), d.Address())
	d.View = stored.ViewOf(d).With(func(p *stored.ViewParts) {
		p.Sources = []stored.ViewLeaf{{Ref: baseRef, Reads: []int32{1}, Attributes: []int32{1}}}
	})
	mvSources := []exec.Leaf{{Ref: mvRef, Relation: d, Uses: []int{0}}}
	if err := analyze.CheckQuerySources(auth, mvSources); err != nil {
		t.Fatal("MV SELECT demanded base ACL:", err)
	}
	var denied *sqlstate.Error
	if err := analyze.CheckQuerySources(auth, []exec.Leaf{{Ref: baseRef, Relation: &base, Uses: []int{0}}}); !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for table unit_base" {
		t.Fatalf("base unexpectedly readable: %v", err)
	}
	e.Mu.Lock()
	refs, err := e.QueryTableRefsLocked(auth, auth.State.CurrentUser, mvSources, nil, ids.SessionToken{}, plan.RowMark{})
	e.Mu.Unlock()
	if err != nil || !reflect.DeepEqual(refs, []ids.ObjectRef{mvRef}) {
		t.Fatalf("physical read expanded frozen source references: %v %v", refs, err)
	}
}
