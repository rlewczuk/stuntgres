package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestRoutineBodyPrincipalRemap(t *testing.T) {
	source, target := new(authorization.Domain), new(authorization.Domain)
	owner, reader := source.Principal(10), source.Principal(20)
	body := &Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001}, ObjectRevision: 3, Owner: owner, SecurityDefiner: true, Display: RoutineDisplay{Language: SQLRoutineLanguage, Source: " SELECT $1 "},
		ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader}, Privileges: authorization.ExecutePrivilege}}}}
	outer := &Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90002}, ObjectRevision: 4, Owner: owner, Display: RoutineDisplay{Language: CRoutineLanguage, Source: "original_symbol"},
		Body: []RoutineBodyEdge{{Routine: body}, {Routine: body}}}
	// A current COW revision must not replace the original body association.
	current := *body
	current.ObjectRevision = 9
	body.Body = []RoutineBodyEdge{{Routine: outer}}
	state := NewState(1)
	state.SetDescriptor(body.Address(), &current)
	state.SetDescriptor(outer.Address(), outer)
	mapPrincipal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if ref != owner && ref != reader {
			t.Fatalf("unexpected source principal %v", ref)
		}
		return target.Principal(ref.ID() + 100), nil
	}
	if err := RemapPrincipals(state, PrincipalRemap{Principal: mapPrincipal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return mapPrincipal(ref)
	}}); err != nil {
		t.Fatal(err)
	}
	mapped := state.MustProcedure(outer.Address())
	captured := mapped.Body[0].Routine
	if mapped == outer || captured == body || mapped.Display != outer.Display || captured.Display != body.Display || mapped.Owner != target.Principal(110) || captured.Owner != mapped.Owner || captured.Revision() != body.Revision() || state.MustProcedure(body.Address()).Revision() != current.Revision() {
		t.Fatal("principal remap lost original declaration provenance")
	}
	if mapped.Body[1].Routine != captured || captured.Body[0].Routine != mapped {
		t.Fatal("shared or cyclic body associations were not preserved")
	}
	entry := captured.ACL.Entries[0]
	if entry.Grantor != target.Principal(110) || entry.Grantee.Role != target.Principal(120) || entry.Privileges != authorization.ExecutePrivilege {
		t.Fatal("body ACL principals were not remapped", entry)
	}
	if body.Owner != owner || body.ACL.Entries[0].Grantor != owner || body.Body[0].Routine != outer || outer.Body[0].Routine != body {
		t.Fatal("source declaration graph mutated")
	}
}
