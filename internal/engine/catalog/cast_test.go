package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestCastDefinitionPairIndex(t *testing.T) {
	state, _, _ := catalogReadFixture()
	d := &Cast{OID: 910, ObjectRevision: 1, Source: sqltype.Xid, Target: sqltype.Float4, Context: "e", Method: "b"}
	state.SetDescriptor(d.Address(), d)
	state.BindName(DescriptorNameKey(d), d.Address())
	original := Read{state: state}
	if got, ok := original.Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("missing directed pair")
	}
	if _, ok := original.Cast(d.Target, d.Source); ok {
		t.Fatal("fabricated reverse pair")
	}
	clone := CloneState(state)
	clone.RemoveDescriptor(d.Address())
	if _, ok := (Read{state: clone}).Cast(d.Source, d.Target); ok {
		t.Fatal("removed pair retained")
	}
	if got, ok := original.Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("removal mutated predecessor")
	}
	clone.ResetMaps()
	if _, ok := (Read{state: clone}).Cast(d.Source, d.Target); ok {
		t.Fatal("reset retained pair")
	}
}
func TestCastDefinitionCandidateIsolation(t *testing.T) {
	state, _, _ := catalogReadFixture()
	base := Read{state: state}
	candidate := NewCandidate(base)
	d := &Cast{OID: ids.OID(911), ObjectRevision: 1, Source: sqltype.Xid, Target: sqltype.Float4, Context: "e", Method: "b"}
	if err := candidate.Create(d); err != nil {
		t.Fatal(err)
	}
	if got, ok := candidate.Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("candidate pair missing")
	}
	if _, ok := base.Cast(d.Source, d.Target); ok {
		t.Fatal("candidate published early")
	}
	if got, ok := (Read{state: candidate.State()}).Cast(d.Source, d.Target); !ok || got != d {
		t.Fatal("overlay lost pair")
	}
	if err := candidate.Create(&Cast{OID: 912, ObjectRevision: 1, Source: d.Source, Target: d.Target, Context: "i", Method: "i"}); err == nil {
		t.Fatal("duplicate pair accepted")
	}
}

func TestCastDefinitionPrincipalRemap(t *testing.T) {
	state := NewState(1)
	cast := &Cast{
		OID: 910, ObjectRevision: 7, Source: sqltype.Xid, Target: sqltype.Float4,
		Function:   ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 911},
		InputCast:  ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: 912},
		ResultCast: ids.ObjectAddress{ClassID: ids.CastClassID, ObjectID: 913},
		Context:    "e", Method: "f",
		Extension: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 914},
	}
	state.SetDescriptor(cast.Address(), cast)
	state.BindName(DescriptorNameKey(cast), cast.Address())
	candidate := CloneState(state)
	unexpected := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		t.Fatal("ownerless cast requested principal authority", ref)
		return authorization.PrincipalRef{}, nil
	}
	if err := RemapPrincipals(candidate, PrincipalRemap{
		Principal: unexpected,
		SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
			return unexpected(ref)
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, view := range []Read{{state: state}, {state: candidate}} {
		got, ok := view.Cast(cast.Source, cast.Target)
		if !ok || got != cast || view.Get(cast.Address()) != cast || view.NameAddress(DescriptorNameKey(cast)) != cast.Address() {
			t.Fatal("principal remap changed ownerless descriptor or object references", got)
		}
	}
}

func TestCatalogVectorCoercionEligibility(t *testing.T) {
	types:=make(map[sqltype.ID]*sqltype.Descriptor)
	for _,d:=range sqltype.Builtins(){types[d.ID()]=d}
	view:=Read{state:NewState(1)}
	for _,vector:=range []sqltype.ID{sqltype.Int2Vector,sqltype.OidVector}{
		target:=types[vector].Usage()
		for mode:=sqltype.Implicit;mode<=sqltype.Explicit;mode++{
			if err:=CanCoerce(view,target,target,mode);err!=nil{t.Fatal("vector identity rejected",vector,mode,err)}
			for _,array:=range []sqltype.ID{1005,1007,1016,1028}{
				if err:=CanCoerce(view,types[array].Usage(),target,mode);err==nil{t.Fatal("generic array gained vector coercion",array,vector,mode)}
			}
			array:=types[types[vector].Element().AssociatedArray()]
			if err:=CanCoerce(view,target,array.Usage(),mode);err!=nil{t.Fatal("canonical container lowering rejected",vector,mode,err)}
		}
	}
	candidates:=[]sqltype.OperatorInputs{{Left:2277,Right:2277},{Left:sqltype.OidVector,Right:sqltype.OidVector}}
	can:=func(source *sqltype.Descriptor,target sqltype.ID)bool{return CanCoerce(view,source.Usage(),types[target].Usage(),sqltype.Implicit)==nil}
	if i,err:=sqltype.ResolveOperatorWithCoercion(candidates,types[1007],types[1007],false,can);err!=nil||i!=0{t.Fatal("exact anyarray operator lost to vector",i,err)}
	if _,err:=sqltype.ResolveOperatorWithCoercion(candidates,types[1007],types[1016],false,can);err==nil{t.Fatal("different ordinary arrays gained an operator through vectors")}
	if i,err:=sqltype.ResolveOperatorWithCoercion(candidates,types[sqltype.OidVector],types[sqltype.OidVector],false,can);err!=nil||i!=1{t.Fatal("exact vector operator lost",i,err)}
}

func TestCastDefinitionExtensionMembership(t *testing.T) {
	state, ns, _ := catalogReadFixture()
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 || typ.ID() == sqltype.Int8 {
			d := &Type{SQLType: typ, ObjectRevision: 1}
			state.SetDescriptor(d.Address(), d)
		}
	}
	cast := &Cast{OID: 920, ObjectRevision: 1, Source: sqltype.Int4, Target: sqltype.Int8, Context: "e", Method: "i", Extension: ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 921}}
	ext := &Extension{ObjectAddress: cast.Extension, ObjectRevision: 1, ObjectName: "cast_owner", Version: "1", TargetNamespace: ns.Address(), Members: []ids.ObjectAddress{cast.Address()}}
	for _, d := range []Descriptor{ext, cast} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(DescriptorNameKey(d), d.Address())
	}
	view := Read{state: state}
	if ExtensionMembership(cast) != ext.Address() || cast.Namespace() != (ids.ObjectAddress{}) {
		t.Fatal("cast acquired namespace or lost direct membership")
	}
	if err := (CastProvider{}).Validate(view, cast); err != nil {
		t.Fatal("ownerless member rejected", err)
	}
	for _, cause := range []string{"missing_extension", "missing_member", "generated_owner"} {
		t.Run(cause, func(t *testing.T) {
			candidate := CloneState(state)
			bad, owner := *cast, *ext
			switch cause {
			case "missing_extension":
				bad.Extension.ObjectID++
			case "missing_member":
				owner.Members = nil
			case "generated_owner":
				bad.GeneratedRange = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(cast.Source)}
			}
			candidate.SetDescriptor(bad.Address(), &bad)
			candidate.SetDescriptor(owner.Address(), &owner)
			if err := (CastProvider{}).Validate(Read{state: candidate}, &bad); err == nil {
				t.Fatal("invalid ownerless membership accepted", cause)
			}
			if got, ok := view.Cast(cast.Source, cast.Target); !ok || got != cast || view.Get(ext.Address()) != ext {
				t.Fatal("candidate changed retained descriptor/pair/owner")
			}
		})
	}
}
