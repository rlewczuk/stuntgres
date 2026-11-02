package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func TestBtreeGINNetworkBinding(t *testing.T) {
	e, idx, root := networkEngine(t, false)
	view := e.CatalogRead()
	foreign, foreignIndex, _ := networkEngine(t, false)
	for col, tc := range networkPrivateCases {
		class := idx.SQLIndex.Keys[col].Class
		metadata, ok := view.IndexClass(ids.OID(class.OID()))
		if !ok {
			t.Fatal("missing class")
		}
		descriptor, ok := view.OperatorClass(ids.OID(class.OID()))
		if !ok {
			t.Fatal("missing actual class")
		}
		family, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(class.FamilyOID())})
		if !ok {
			t.Fatal("missing family")
		}
		if !class.IsGin() || class.InputOID() != uint32(tc.input) || class.StorageOID() != 0 || metadata.OID() != class.OID() || class.Support(6).Routine != 0 {
			t.Fatal("class shape", tc.typ, class)
		}
		reject := func(state *catalog.State, label string) {
			t.Helper()
			if _, err := routine.BindClassSupport(view.WithState(state), class); err == nil {
				t.Fatal("detached binding accepted", tc.typ, label)
			}
		}
		for slot := 1; slot <= 5; slot++ {
			member, ok := view.FamilySupport(ids.OID(class.FamilyOID()), tc.input, tc.input, int16(slot))
			if !ok {
				t.Fatal("missing exact associated support", tc.typ, slot)
			}
			owner := ids.ObjectAddress{}
			if slot == 2 || slot == 3 {
				owner = descriptor.Address()
			}
			if member.InternalOwner != owner {
				t.Fatal("support dependency owner differs from released GIN policy", tc.typ, slot, member.InternalOwner, owner)
			}
			proc := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: member.Function})
			stamp := class.Support(slot)
			if stamp.Member != uint32(member.Address().ObjectID) || stamp.Routine != uint32(member.Function) || stamp.Left != uint32(tc.input) || stamp.Right != uint32(tc.input) || stamp.Revision != uint64(member.Revision()) || stamp.ClassRevision != uint64(descriptor.Revision()) || stamp.FamilyRevision != uint64(family.Revision()) || stamp.RoutineRevision != uint64(proc.Revision()) || stamp.Authority == nil {
				t.Fatal("support lost actual effective revision/seal", tc.typ, slot, stamp)
			}
			if slot == 1 && member.Function != tc.comparator {
				t.Fatal("wrong actual comparator", tc.typ, member.Function)
			}
			state := catalog.CloneState(view.State())
			state.RemoveDescriptor(member.Address())
			reject(state, "missing support")
			state = catalog.CloneState(view.State())
			wrongOwner := *member
			wrongOwner.InternalOwner = descriptor.Address()
			if owner != (ids.ObjectAddress{}) {
				wrongOwner.InternalOwner = ids.ObjectAddress{}
			}
			state.SetDescriptor(wrongOwner.Address(), &wrongOwner)
			reject(state, "wrong exact support dependency owner")
			state = catalog.CloneState(view.State())
			changed := *proc
			changed.ObjectRevision = proc.Revision() + 1
			state.SetDescriptor(changed.Address(), &changed)
			reject(state, "stale routine")
			state = catalog.CloneState(view.State())
			wrong := *member
			wrong.Function = 333
			wrong.ObjectRevision = member.Revision() + 1
			state.SetDescriptor(wrong.Address(), &wrong)
			reject(state, "wrong comparator/helper")
			if slot != 1 {
				state = catalog.CloneState(view.State())
				unsealed := *proc
				unsealed.Implementation = nil
				state.SetDescriptor(unsealed.Address(), &unsealed)
				reject(state, "same-name unsealed helper")
			}
		}
		for strategy, oid := range tc.operators {
			member, ok := view.FamilyOperator(ids.OID(class.FamilyOID()), tc.search, tc.search, int16(strategy+1), 's')
			if !ok || member.Operator != oid || member.InternalOwner != (ids.ObjectAddress{}) {
				t.Fatal("search operand graph", tc.typ, strategy, member)
			}
			op, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: oid})
			if !ok {
				t.Fatal("missing genuine operator")
			}
			proc := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: op.Function})
			stamp := class.Search(strategy + 1)
			if stamp.Operator != uint32(oid) || stamp.Routine != uint32(op.Function) || stamp.OperatorRevision != uint64(op.Revision()) || stamp.RoutineRevision != uint64(proc.Revision()) {
				t.Fatal("search lost actual executable stamps", tc.typ, strategy, stamp)
			}
			state := catalog.CloneState(view.State())
			state.RemoveDescriptor(member.Address())
			reject(state, "missing search including fifth")
			state = catalog.CloneState(view.State())
			changed := *member
			changed.ObjectRevision = member.Revision() + 1
			state.SetDescriptor(changed.Address(), &changed)
			reject(state, "stale fifth/member")
			state = catalog.CloneState(view.State())
			wrongOwner := *member
			wrongOwner.InternalOwner = descriptor.Address()
			state.SetDescriptor(wrongOwner.Address(), &wrongOwner)
			reject(state, "wrong search dependency owner")
			state = catalog.CloneState(view.State())
			state.RemoveDescriptor(op.Address())
			reject(state, "missing operator")
		}
		for _, address := range []ids.ObjectAddress{descriptor.Address(), family.Address()} {
			state := catalog.CloneState(view.State())
			state.RemoveDescriptor(address)
			reject(state, "missing class/family")
		}
		state := catalog.CloneState(view.State())
		changedClass := *descriptor
		changedClass.ObjectRevision = descriptor.Revision() + 1
		state.SetDescriptor(changedClass.Address(), &changedClass)
		reject(state, "stale class revision")
		state = catalog.CloneState(view.State())
		changedFamily := *family
		changedFamily.ObjectRevision = family.Revision() + 1
		state.SetDescriptor(changedFamily.Address(), &changedFamily)
		reject(state, "stale family revision")
		state = catalog.CloneState(view.State())
		forged := *descriptor
		forged.Origin = nil
		state.SetDescriptor(forged.Address(), &forged)
		reject(state, "unsealed class")
		other := foreignIndex.SQLIndex.Keys[col].Class
		if other.OID() != class.OID() || other.Support(1).Authority == class.Support(1).Authority {
			t.Fatal("foreign authority fixture")
		}
		if _, err := routine.BindClassSupport(foreign.CatalogRead(), class); err == nil {
			t.Fatal("foreign numeric installation authorized")
		}
		if _, err := routine.BindClassSupport(view, other); err == nil {
			t.Fatal("reverse foreign authority authorized")
		}
	}
	proc := view.MustProcedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 4119})
	if proc.Builtin == nil || proc.Builtin.Providers != functions.IndexSupportRoutineProvider {
		t.Fatal("4119 lacks its genuine builtin execution provider", proc.Builtin)
	}
	if len(root.Entries) != 0 || len(root.Gin.Columns) != 6 {
		t.Fatal("empty root lost bindings")
	}
	// Existing lawful directional input admission, without widening either type.
	enginetest.Run(t, e, "CREATE INDEX network_cidr_inet ON network_private USING gin(cidr inet_ops)")
	enginetest.Run(t, e, "CREATE TABLE network_int4(v integer)")
	enginetest.Run(t, e, "CREATE INDEX network_int4_i ON network_int4 USING gin(v oid_ops)")
	cidr, _ := view.LookupType(sqltype.Cidr)
	if _, err := sqltype.BindGinTokens(cidr.Usage(), idx.SQLIndex.Keys[0].Class); err != nil {
		t.Fatal("inet_ops rejected concrete CIDR", err)
	}
	inet, _ := view.LookupType(sqltype.Inet)
	if _, err := sqltype.BindGinTokens(inet.Usage(), idx.SQLIndex.Keys[1].Class); err == nil {
		t.Fatal("cidr_ops admitted INET")
	}
}
