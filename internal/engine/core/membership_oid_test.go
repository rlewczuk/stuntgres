package core_test

import (
	"context"
	"math"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func membershipOIDFixture(t *testing.T) *core.Engine {
	t.Helper()
	e := engine.New()
	root := e.RoleStore.Published.Root
	for i, name := range []string{"oid_a","oid_b","oid_c","oid_d"} {
		var err error
		root, err = root.PutDescriptor(authorization.RoleDescriptor{Ref:e.RoleStore.Domain.Principal(uint64(20000+i)),Name:name,Inherit:true,ConnectionLimit:-1})
		if err != nil { t.Fatal(err) }
	}
	e.RoleStore.PublishLocked(&access.RoleRecord{Root:root,Revision:1})
	e.RoleStore.InstallPrincipalFrontierLocked(20004)
	return e
}

func membershipOIDCandidate(t *testing.T,e *core.Engine,base *access.RoleRecord,role,member string) *access.RoleRecord {
	t.Helper()
	bootstrap := e.RoleStore.Domain.Principal(10)
	request := &parser.RoleMembership{Targets:[]parser.RoleSpec{{Name:role}},Members:[]parser.RoleSpec{{Name:member}}}
	root, changed, _, err := authorization.MembershipCandidate(context.Background(),base.Root.Read(bootstrap),authorization.RoleState{CurrentUser:bootstrap,SessionUser:bootstrap},request,nil,true,true)
	if err != nil || !changed { t.Fatal("candidate",changed,err) }
	return &access.RoleRecord{Root:root}
}

func TestMembershipOIDPrivatePublication(t *testing.T) {
	e := membershipOIDFixture(t)
	left, done := enginetest.RoleOperation(t,e,"postgres")
	defer done()
	right, doneRight := enginetest.RoleOperation(t,e,"postgres")
	defer doneRight()
	lref, err := e.Begin(left,session.TxOptions{})
	if err != nil { t.Fatal(err) }
	defer e.Rollback(left,lref)
	rref, err := e.Begin(right,session.TxOptions{})
	if err != nil { t.Fatal(err) }
	defer e.Rollback(right,rref)
	e.Mu.Lock()
	base := e.RoleStore.Published
	l := membershipOIDCandidate(t,e,base,"oid_a","oid_b")
	r := membershipOIDCandidate(t,e,base,"oid_c","oid_d")
	before := e.RoleStore.NextMembershipOID()
	if err := core.InstallOperationRolesLocked(e,left,l); err != nil { t.Fatal(err) }
	if err := core.InstallOperationRolesLocked(e,right,r); err != nil { t.Fatal(err) }
	lm,rm := l.Root.Read(authorization.PrincipalRef{}).Memberships(),r.Root.Read(authorization.PrincipalRef{}).Memberships()
	lid,rid := lm[len(lm)-1].OID,rm[len(rm)-1].OID
	if lid == 0 || rid == 0 || lid == rid || e.RoleStore.Published != base || e.RoleStore.NextMembershipOID() != before+2 {
		t.Fatal("private reservations",lid,rid,e.RoleStore.NextMembershipOID())
	}
	e.Mu.Unlock()
	if _, err := e.Commit(left,lref); err != nil { t.Fatal(err) }
	if _, err := e.Commit(right,rref); err != nil { t.Fatal(err) }
	seen := map[uint32]bool{}
	for edge := range e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Edges() { seen[edge.OID] = true }
	if !seen[lid] || !seen[rid] { t.Fatal("commit replaced private identities",seen) }
}

func TestMembershipOIDRollbackAndExhaustion(t *testing.T) {
	e := membershipOIDFixture(t)
	op, done := enginetest.RoleOperation(t,e,"postgres")
	defer done()
	ref, err := e.Begin(op,session.TxOptions{})
	if err != nil { t.Fatal(err) }
	base := e.RoleStore.Published
	before := base.Root.Read(authorization.PrincipalRef{}).Memberships()
	e.Mu.Lock()
	candidate := membershipOIDCandidate(t,e,base,"oid_a","oid_b")
	frontier := e.RoleStore.NextMembershipOID()
	if err := core.InstallOperationRolesLocked(e,op,candidate); err != nil { t.Fatal(err) }
	e.Mu.Unlock()
	if _, err := e.Rollback(op,ref); err != nil { t.Fatal(err) }
	if e.RoleStore.NextMembershipOID() != frontier+1 || !reflect.DeepEqual(before,e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Memberships()) { t.Fatal("rollback rewound reservation or changed root") }
	e.Mu.Lock()
	defer e.Mu.Unlock()
	e.RoleStore.InstallMembershipFrontierLocked(math.MaxUint32+1)
	candidate = membershipOIDCandidate(t,e,base,"oid_a","oid_b")
	revision,principal := e.RoleStore.NextRevision(),e.RoleStore.NextPrincipal()
	if err := core.InstallOperationRolesLocked(e,op,candidate); err == nil { t.Fatal("exhaustion accepted") }
	if e.RoleStore.Published != base || e.RoleStore.NextRevision() != revision || e.RoleStore.NextPrincipal() != principal || candidate.Revision != 0 || candidate.Root.Read(authorization.PrincipalRef{}).Memberships()[3].OID != 0 { t.Fatal("failed reservation published partial state") }
	e.RoleStore.InstallMembershipFrontierLocked(frontier+1)
	e.RoleStore.InstallRevisionFrontierLocked(math.MaxUint64)
	if err := core.InstallOperationRolesLocked(e,op,candidate); err == nil { t.Fatal("revision exhaustion accepted") }
	if e.RoleStore.NextMembershipOID() != frontier+1 || e.RoleStore.Published != base || candidate.Revision != 0 {
		t.Fatal("revision failure consumed membership reservation")
	}
}

func TestMembershipOIDCreateFailure(t *testing.T) {
	e := membershipOIDFixture(t)
	op, done := enginetest.RoleOperation(t,e,"postgres")
	defer done()
	e.RoleStore.InstallMembershipFrontierLocked(math.MaxUint32+1)
	base := e.RoleStore.Published
	principal,revision := e.RoleStore.NextPrincipal(),e.RoleStore.NextRevision()
	request := &parser.CreateRole{Name:parser.Identifier{Text:"oid_failed_create"},Options:parser.RoleOptions{InitialMemberships:[]parser.InitialRoleMembership{{Kind:"in",Members:[]parser.RoleSpec{{Name:"oid_a"}}}}}}
	if _, err := e.Execute(op,request,0,nil); err == nil { t.Fatal("CREATE with exhausted membership IDs succeeded") }
	if e.RoleStore.Published != base || e.RoleStore.NextPrincipal() != principal || e.RoleStore.NextRevision() != revision || e.RoleStore.NextMembershipOID() != math.MaxUint32+1 {
		t.Fatal("failed CREATE consumed identities or published a prefix")
	}
	if _, exists := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).Lookup("oid_failed_create"); exists { t.Fatal("failed CREATE leaked role") }
}
