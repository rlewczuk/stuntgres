package control

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// These are generic ownerless-member mechanics; genuine installed hstore
// manifests and actual emitted-SQL replay are covered by their owning suites.
func TestCastExtensionReplayOwnership(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	ext := &catalog.Extension{ObjectAddress: fdwUnitAddress(ids.ExtensionClassID,930100), ObjectRevision:1, ObjectName:"cast_owner", Version:"1", TargetNamespace:fdwUnitAddress(ids.NamespaceClassID,ids.PublicNamespaceOID), Owner:source.Bootstrap}
	cast := &catalog.Cast{OID:930101,ObjectRevision:2,Source:sqltype.Int4,Target:sqltype.Int8,Method:"i",Context:"e",Extension:ext.Address()}
	ext.Members=[]ids.ObjectAddress{cast.Address()}
	for _,d:=range []catalog.Descriptor{ext,cast}{source.State.SetDescriptor(d.Address(),d);source.State.BindName(catalog.DescriptorNameKey(d),d.Address())}
	view:=source.CatalogView
	if err:=catalog.ValidateExtensionMembership(view,cast);err!=nil{t.Fatal("ownerless reciprocal placement",err)}
	if oid,err:=predecessor(view,cast.Address(),ids.ObjectAddress{});err!=nil || oid!=ext.Address().ObjectID{t.Fatal("cast replay must depend on extension, not omitted cast event",oid,err)}
	s:=&dumpScript{Capture:&Capture{CatalogView:view,Graph:make(dump.Graph)},declarations:make(map[ids.OID]dump.Statement)}
	if err:=s.castEvents();err!=nil{t.Fatal(err)}
	if _,exists:=s.Graph[dump.Event{OID:cast.OID}];exists{t.Fatal("extension cast acquired standalone event")}
	if err:=s.castDeclarations();err!=nil{t.Fatal(err)}
	if _,exists:=s.declarations[cast.OID];exists{t.Fatal("extension cast emitted duplicate CREATE CAST")}
	// Consumer recipes contain semantics, not a duplicated membership assertion.
	recipe:=*cast;recipe.OID=0;recipe.ObjectRevision=0;recipe.Extension=ids.ObjectAddress{}
	if !sameReplayCast(cast,&recipe){t.Fatal("semantic recipe incorrectly requires catalog identity")}
	for _,mutate:=range []func(*catalog.Cast){func(d *catalog.Cast){d.Context="i"},func(d *catalog.Cast){d.Function=fdwUnitAddress(ids.ProcedureClassID,930102)}}{
		bad:=recipe;mutate(&bad);if sameReplayCast(cast,&bad){t.Fatal("distinct semantic recipe admitted")}
	}
}

func TestCastExtensionReplacementRemap(t *testing.T) {
	source, _, _, _ := fdwUnitSource()
	ext:=&catalog.Extension{ObjectAddress:fdwUnitAddress(ids.ExtensionClassID,930100),ObjectRevision:1,ObjectName:"cast_owner",Version:"1",TargetNamespace:fdwUnitAddress(ids.NamespaceClassID,ids.PublicNamespaceOID),Owner:source.Bootstrap}
	cast:=&catalog.Cast{OID:930101,ObjectRevision:2,Source:sqltype.Int4,Target:sqltype.Int8,Method:"i",Context:"e",Extension:ext.Address()}
	ext.Members=[]ids.ObjectAddress{cast.Address()}
	for _,d:=range []catalog.Descriptor{ext,cast}{source.State.SetDescriptor(d.Address(),d);source.State.BindName(catalog.DescriptorNameKey(d),d.Address())}
	next:=catalog.CloneState(source.State)
	mappedExtension,mappedCast:=ext.Address(),cast.Address();mappedExtension.ObjectID+=100;mappedCast.ObjectID+=100
	mapping:=map[ids.ObjectAddress]ids.ObjectAddress{ext.Address():mappedExtension,cast.Address():mappedCast}
	if err:=RemapReplacementIdentities(source.CatalogView,next,nil,mapping,nil,nil,nil);err!=nil{t.Fatal(err)}
	got,ok:=next.Get(mappedCast).(*catalog.Cast)
	if !ok || got==cast || got.Extension!=mappedExtension || got.Address()!=mappedCast || got.Revision()!=cast.Revision(){t.Fatal("cast lost destination extension authority",got)}
	installation:=next.MustExtension(mappedExtension)
	if len(installation.Members)!=1 || installation.Members[0]!=mappedCast{t.Fatal("reciprocal member did not remap",installation)}
	if cast.Extension!=ext.Address() || ext.Members[0]!=cast.Address() || source.State.Get(cast.Address())!=cast{t.Fatal("remap mutated retained source catalog")}
}
