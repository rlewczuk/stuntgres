package core_test

import (
	"bytes"
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
)

func TestSnapshotSyncForeignActionRestore(t *testing.T) {
	e:=engine.New()
	enginetest.Run(t,e,"CREATE TABLE sync_restore_parent(id TEXT PRIMARY KEY)")
	enginetest.Run(t,e,"CREATE TABLE sync_restore_child(fk TEXT DEFAULT pg_export_snapshot() REFERENCES sync_restore_parent(id) ON DELETE SET DEFAULT)")
	old:=e.CatalogRead()
	oldParent:=enginetest.RoleTableRef(t,e,"sync_restore_parent").Address
	oldChild:=old.MustRelation(enginetest.RoleTableRef(t,e,"sync_restore_child").Address)
	var dump bytes.Buffer
	if err:=engine.Dump(context.Background(),e,&dump);err!=nil{t.Fatal(err)}
	if err:=engine.Restore(context.Background(),e,bytes.NewReader(dump.Bytes()));err!=nil{t.Fatal(err)}
	current:=e.CatalogRead()
	parent:=enginetest.RoleTableRef(t,e,"sync_restore_parent").Address
	child:=current.MustRelation(enginetest.RoleTableRef(t,e,"sync_restore_child").Address)
	if current.Epoch()==old.Epoch()||child==oldChild{t.Fatal("restore failed to create a new immutable descriptor incarnation")}
	check:=func(read catalog.Read,target *catalog.Relation){
		t.Helper()
		count:=0
		for address,owner:=range read.IncomingForeignActionOwners(parent){
			count++
			if address!=target.Address()||owner!=target||owner.Constraints.ForeignKeys[0].Target!=parent{t.Fatal("restored/private inverse retained stale child or target authority")}
		}
		if count!=1{t.Fatal("missing or duplicated restored inverse owner",count)}
	}
	check(current,child)
	stage:=e.BeginCatalogStage(current)
	defer e.DiscardCatalogStage(stage)
	next:=*child;next.ObjectRevision++;next.Comment="private current recipe"
	if err:=stage.Replace(&next);err!=nil{t.Fatal(err)}
	private:=catalog.NewRead(current.Owner(),stage.Candidate.State())
	check(private,&next)
	check(current,child)
	count:=0
	for _,owner:=range old.IncomingForeignActionOwners(oldParent){count++;if owner!=oldChild{t.Fatal("restore/private candidate mutated old index backing")}}
	if count!=1{t.Fatal("retained old inverse lost its owner")}
}
