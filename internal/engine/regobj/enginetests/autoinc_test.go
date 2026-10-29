package regobj_test_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
)

func TestAutoincTextSequenceNames(t *testing.T) {
	e:=engine.New()
	op,done:=enginetest.RoleOperation(t,e,"postgres");defer done()
	for _,q:=range []string{`CREATE SEQUENCE "123"`,`CREATE SEQUENCE "-"`,`CREATE SCHEMA "Mixed Schema"`,`CREATE SEQUENCE "Mixed Schema"."S ' Q"`} { enginetest.RoleExecute(t,e,op,q) }
	e.Mu.Lock()
	auth,err:=e.AuthorizationLocked(op)
	if err!=nil { e.Mu.Unlock();t.Fatal(err) }
	path,err:=core.ReferencePath(auth)
	e.Mu.Unlock()
	if err!=nil { t.Fatal(err) }
	capture:=regobj.InputCapture{Authority:regobj.Authority{View:auth.View,Environment:auth},Path:path,Database:op.Session.DatabaseName,Token:op.Session.SessionToken}
	resolver:=regobj.NewClassInput(capture)
	read:=e.CatalogRead()
	public:=ids.ObjectAddress{ClassID:ids.NamespaceClassID,ObjectID:ids.PublicNamespaceOID}
	mixed:=read.NameAddress(ids.NameKey{ClassID:ids.NamespaceClassID,Name:"Mixed Schema"})
	for _,tc:=range []struct{text,name string; namespace ids.ObjectAddress}{
		{"123","123",public},{"-","-",public},{`"Mixed Schema"."S ' Q"`,"S ' Q",mixed},
	} {
		want:=read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:tc.namespace,Name:tc.name})
		got,err:=resolver.ResolveRelationName(context.Background(),tc.text)
		if err!=nil || got!=uint32(want.ObjectID) || want==(ids.ObjectAddress{}) { t.Fatal(tc.text,got,want,err) }
	}
	for _,tc:=range []struct{text string;want uint32}{{"123",123},{"-",0}} {
		got,err:=resolver.ResolveRegclass(context.Background(),tc.text)
		if err!=nil || got!=tc.want { t.Fatal("regclass shortcut changed",tc.text,got,err) }
	}
	for _,text:=range []string{"missing","a..s"} {
		if _,err:=resolver.ResolveRelationName(context.Background(),text);err==nil { t.Fatal("invalid name accepted",text) }
	}
	// This is a captured definition read, not a second namespace walker or a
	// generator operation; resolving a name never marks its sequence called.
	for _,name:=range []string{"123","-"} {
		d,ok:=read.Relation(read.NameAddress(ids.NameKey{ClassID:ids.RelationClassID,Namespace:public,Name:name}))
		if !ok || d.RelationKind!=catalog.SequenceRelation { t.Fatal("lost real sequence",name) }
	}
}
