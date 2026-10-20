package analyze

import (
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// grantPrivileges decides from per-principal grants, so a result shows which
// principal a check ran as. A view's row marks need UPDATE on its child.
type grantPrivileges struct {
	user    authorization.PrincipalRef
	invalid authorization.PrincipalRef
	child   ids.ObjectAddress
	grants  map[authorization.PrincipalRef]map[ids.ObjectAddress]authorization.Privileges
}

var errInvalidPrincipal = errors.New("invalid principal")

func (g grantPrivileges) deny(a ids.ObjectAddress, mask authorization.Privileges) error {
	if g.grants[g.user][a]&mask != mask {
		return fmt.Errorf("denied %d on %d as %d", mask, a.ObjectID, g.user.ID())
	}
	return nil
}
func (g grantPrivileges) Check(req access.Requirement) error {
	return g.deny(req.Object.Address, req.Privileges)
}
func (g grantPrivileges) CheckColumns(ref ids.ObjectRef, _ *catalog.Relation, mask authorization.Privileges, _ []int) error {
	return g.deny(ref.Address, mask)
}
func (g grantPrivileges) WithCurrentUser(p authorization.PrincipalRef) (Privileges, error) {
	g.user = p
	if p == g.invalid {
		return g, errInvalidPrincipal
	}
	return g, nil
}
func (grantPrivileges) CheckViewWrite(Binding, parser.Statement) error {
	return errors.New("unexpected view write")
}
func (g grantPrivileges) CheckViewRowMarks(relation *catalog.Relation, mark plan.RowMark) error {
	if mark.Mode == 0 {
		return nil
	}
	if g.grants[g.user][g.child]&authorization.UpdatePrivilege == 0 {
		return fmt.Errorf("view row marks on %d as %d", relation.ObjectAddress.ObjectID, g.user.ID())
	}
	return nil
}

func TestCheckQuerySourcesChecksEachSourceAsItsPrincipal(t *testing.T) {
	domain := &authorization.Domain{}
	caller, owner, invalid := domain.Principal(100), domain.Principal(101), domain.Principal(102)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 5000}}
	view := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 5001}, RelationKind: catalog.ViewRelation}
	tableRef, viewRef := ids.ObjectRef{Address: table.ObjectAddress}, ids.ObjectRef{Address: view.ObjectAddress}
	both := authorization.SelectPrivilege | authorization.UpdatePrivilege
	auth := grantPrivileges{user: caller, invalid: invalid, child: table.ObjectAddress, grants: map[authorization.PrincipalRef]map[ids.ObjectAddress]authorization.Privileges{
		caller: {view.ObjectAddress: both},
		owner:  {table.ObjectAddress: both, view.ObjectAddress: both},
	}}
	forUpdate := plan.RowMark{Mode: lock.RowShare}
	cases := []struct {
		name    string
		sources []exec.Leaf
		want    string
	}{
		{"definer source reads as its principal", []exec.Leaf{{Ref: viewRef, Relation: view}, {Ref: tableRef, Relation: table, Principal: owner}}, ""},
		{"caller source reads as the caller", []exec.Leaf{{Ref: tableRef, Relation: table}}, "denied 1 on 5000 as 100"},
		{"invalid principal is rejected before its checks", []exec.Leaf{{Ref: tableRef, Relation: table, Principal: invalid}}, errInvalidPrincipal.Error()},
		{"analysis-only source needs no SELECT", []exec.Leaf{{Ref: tableRef, Relation: table, AnalysisOnly: true}}, ""},
		{"analysis-only source keeps its row mark", []exec.Leaf{{Ref: tableRef, Relation: table, AnalysisOnly: true, Mark: forUpdate}}, "denied 32 on 5000 as 100"},
		{"SELECT pass precedes row marks", []exec.Leaf{{Ref: viewRef, Relation: view, Mark: forUpdate}, {Ref: tableRef, Relation: table}}, "denied 1 on 5000 as 100"},
		{"view row marks run as the source principal", []exec.Leaf{{Ref: viewRef, Relation: view, Principal: owner, Mark: forUpdate}}, ""},
		{"view row marks run as the caller", []exec.Leaf{{Ref: viewRef, Relation: view, Mark: forUpdate}}, "view row marks on 5001 as 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckQuerySources(auth, tc.sources)
			if got := fmt.Sprint(err); tc.want == "" && err != nil || tc.want != "" && got != tc.want {
				t.Fatalf("CheckQuerySources() = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCheckBindingDeleteChecksDeleteBeforeReads(t *testing.T) {
	domain := &authorization.Domain{}
	caller := domain.Principal(100)
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 5000}}
	b := Binding{Ref: ids.ObjectRef{Address: table.ObjectAddress}, Relation: table, UsedColumns: []int{0}}
	for _, tc := range []struct {
		granted authorization.Privileges
		want    string
	}{
		{0, "denied 16 on 5000 as 100"},
		{authorization.DeletePrivilege, "denied 1 on 5000 as 100"},
		{authorization.DeletePrivilege | authorization.SelectPrivilege, "<nil>"},
	} {
		auth := grantPrivileges{user: caller, grants: map[authorization.PrincipalRef]map[ids.ObjectAddress]authorization.Privileges{caller: {table.ObjectAddress: tc.granted}}}
		if got := fmt.Sprint(CheckBinding(auth, b, &parser.Delete{})); got != tc.want {
			t.Fatalf("CheckBinding(DELETE) with %d = %s, want %s", tc.granted, got, tc.want)
		}
	}
}
