package control

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestReplacementRoutineBodyRemap(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	body := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 930001}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "body_original", ObjectRevision: 3, Owner: source.Bootstrap}
	outer := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 930002}, ObjectNamespace: body.Namespace(), ObjectName: "outer", ObjectRevision: 4, Owner: source.Bootstrap, Body: []catalog.RoutineBodyEdge{{Routine: body}, {Routine: body}}}
	body.Body = []catalog.RoutineBodyEdge{{Routine: outer}}
	current := *body
	current.ObjectRevision = 9
	current.ObjectName = "body_current"
	for _, p := range []*catalog.Procedure{&current, outer} {
		source.State.SetDescriptor(p.Address(), p)
		source.State.BindName(catalog.DescriptorNameKey(p), p.Address())
	}
	next := catalog.CloneState(source.State)
	bodyAddress, outerAddress := body.Address(), outer.Address()
	bodyAddress.ObjectID += 100
	outerAddress.ObjectID += 100
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{body.Address(): bodyAddress, outer.Address(): outerAddress}
	if err := RemapReplacementIdentities(source.CatalogView, next, nil, mapping, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	mapped := next.MustProcedure(outerAddress)
	captured := mapped.Body[0].Routine
	if captured.Address() != bodyAddress || captured.Revision() != body.Revision() || captured.Name() != body.Name() || next.MustProcedure(bodyAddress).Revision() != current.Revision() {
		t.Fatal("address remap replaced original body provenance with current declaration")
	}
	if mapped.Body[1].Routine != captured || captured.Body[0].Routine != mapped {
		t.Fatal("shared or cyclic body association lost")
	}
	if outer.Address() == outerAddress || body.Address() == bodyAddress || outer.Body[0].Routine != body || body.Body[0].Routine != outer {
		t.Fatal("source declaration graph mutated")
	}
}
