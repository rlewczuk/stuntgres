package regobj

import (
	"reflect"
	"strconv"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

func TestDroppedIdentityForeignKeyTriggers(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		label := "permanent"
		if temporary {
			label = "temporary"
		}
		t.Run(label, func(t *testing.T) {
			address := func(class, oid ids.OID) ids.ObjectAddress {
				return ids.ObjectAddress{ClassID: class, ObjectID: oid}
			}
			state := catalog.NewState(0)
			parentNS := &catalog.Namespace{ObjectAddress: address(2615, 21001), ObjectName: "Parent S"}
			childNS := &catalog.Namespace{ObjectAddress: address(2615, 21002), ObjectName: "Child S"}
			if temporary {
				childNS.ObjectName = "pg_temp_123"
				childNS.Temporary = new(ids.Domain).Session(1)
			}
			parent := &catalog.Relation{ObjectAddress: address(1259, 21003), ObjectNamespace: parentNS.Address(), ObjectName: "Parent T"}
			child := &catalog.Relation{ObjectAddress: address(1259, 21004), ObjectNamespace: childNS.Address(), ObjectName: "Child T"}
			fk := catalog.ForeignKeyConstraint{OID: 21005, Name: "fk!", Target: parent.Address(), TriggerOIDs: [4]ids.OID{21006, 21007, 21008, 21009}}
			child.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{fk}
			for _, d := range []catalog.Descriptor{parentNS, childNS, parent, child} {
				state.SetDescriptor(d.Address(), d)
				state.BindName(catalog.DescriptorNameKey(d), d.Address())
			}
			view := catalog.NewRead(nil, state)
			path := []ids.ObjectAddress{parentNS.Address(), childNS.Address()}
			captured := make([]catalog.DroppedObject, 0, 4)
			for slot, oid := range fk.TriggerOIDs {
				owner, ns, prefix := parent, parentNS, "a"
				if slot >= 2 {
					owner, ns, prefix = child, childNS, "c"
				}
				name := "RI_ConstraintTrigger_" + prefix + "_" + strconv.FormatUint(uint64(oid), 10)
				schema := ns.Name()
				isTemporary := ns.Temporary != (ids.SessionToken{})
				if isTemporary {
					schema = "pg_temp"
				}
				identity := catalog.Identifier(name) + " on " + catalog.Identifier(schema) + "." + catalog.Identifier(owner.Name())
				a := address(2620, oid)
				inquiry, err := IdentifyAddress(view, authorization.Read{}, path, a)
				if err != nil || !inquiry.Found || inquiry.Kind != "trigger" || inquiry.Schema != "" || inquiry.Name != "" || inquiry.Identity != identity || inquiry.Description != "trigger "+name+" on table "+catalog.Identifier(owner.Name()) || !reflect.DeepEqual(inquiry.Names, []string{schema, owner.Name(), name}) || len(inquiry.Args) != 0 {
					t.Fatalf("slot %d inquiry = %+v, %v; want %q on actual trigger relation", slot, inquiry, err, identity)
				}
				got, err := DroppedAddress(view, authorization.Read{}, path, a)
				if err != nil || !got.Found || got.Address != a || got.Kind != "trigger" || got.Identity != identity || !got.HasSchema || got.Schema != schema || got.HasName || got.Name != "" || got.Temporary != isTemporary || got.Original || got.Normal || !reflect.DeepEqual(got.Names, []string{schema, owner.Name(), name}) || got.Args == nil || len(got.Args) != 0 {
					t.Fatalf("slot %d dropped identity = %+v, %v; want %q schema=%q temporary=%v and NULL simple name", slot, got, err, identity, schema, isTemporary)
				}
				captured = append(captured, got)
			}
			// A replacement constraint with the same name cannot keep old trigger
			// identities alive. Captures remain detached from current metadata.
			replacement := *child
			replacementFK := fk
			replacementFK.OID = 21010
			replacementFK.TriggerOIDs = [4]ids.OID{21011, 21012, 21013, 21014}
			replacement.Constraints.ForeignKeys = []catalog.ForeignKeyConstraint{replacementFK}
			state.SetDescriptor(replacement.Address(), &replacement)
			for _, saved := range captured {
				missing, err := DroppedAddress(view, authorization.Read{}, path, saved.Address)
				if err != nil || !reflect.DeepEqual(missing, catalog.DroppedObject{}) {
					t.Fatalf("retired RI trigger = %+v, %v; want missing", missing, err)
				}
			}
			state.RemoveDescriptor(child.Address())
			state.RemoveDescriptor(parent.Address())
			state.RemoveDescriptor(parentNS.Address())
			state.RemoveDescriptor(childNS.Address())
			for _, saved := range captured {
				if !saved.Found || saved.Identity == "" || len(saved.Names) != 3 || saved.Args == nil {
					t.Fatalf("capture lost after owner removal: %+v", saved)
				}
			}
		})
	}
}
