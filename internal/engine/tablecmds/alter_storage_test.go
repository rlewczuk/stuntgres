package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestAlterStorageDescriptorAndRoots(t *testing.T) {
	e := engine.New()
	enginetest.Run(t,e,`CREATE TABLE t(gone integer,a text,b numeric DEFAULT 13)`)
	enginetest.Run(t,e,`INSERT INTO t VALUES(0,'payload',13)`)
	enginetest.Run(t,e,`ALTER TABLE t DROP gone`)
	enginetest.Run(t,e,`CREATE INDEX storage_dup ON t(a,a)`)
	ref := enginetest.RoleTableRef(t,e,"t")
	indexRef := enginetest.RoleTableRef(t,e,"storage_dup")
	read := e.CatalogRead()
	old, oldIndex := read.MustRelation(ref.Address), read.MustRelation(indexRef.Address)
	store := e.Storage.Relations[ref.Address]
	ends, generation := store.Ends,store.Generation
	root := e.Storage.IndexRoots[indexRef.Address]
	enginetest.Run(t,e,`ALTER TABLE t ALTER a SET STORAGE EXTERNAL, ALTER a SET COMPRESSION pglz`)
	next,nextIndex := e.Catalog.State.MustRelation(ref.Address),e.Catalog.State.MustRelation(indexRef.Address)
	if next==old || next.Revision()!=old.Revision()+1 || next.Address()!=old.Address() || !reflect.DeepEqual(next.Attributes,old.Attributes) || &next.Columns[0]!=&old.Columns[0] || next.Owner!=old.Owner || !reflect.DeepEqual(next.Security,old.Security) || !reflect.DeepEqual(next.ColumnMeta.Defaults,old.ColumnMeta.Defaults) || read.Get(ref.Address)!=old || len(old.ColumnMeta.Storage)!=0 { t.Fatal("metadata predecessor changed") }
	if next.ColumnStorage(0)!=(catalog.ColumnStorage{Storage:"e",Compression:"p"}) || nextIndex.ColumnStorage(0)!=next.ColumnStorage(0) || nextIndex.ColumnStorage(1)!=(catalog.ColumnStorage{Storage:"x"}) || len(oldIndex.ColumnMeta.Storage)!=0 { t.Fatal("first-only index propagation or predecessor changed") }
	if nextIndex.Revision()!=oldIndex.Revision()+1 { t.Fatal("index policy list minted more than one revision") }
	if e.Storage.Relations[ref.Address]!=store || store.Ends!=ends || store.Generation!=generation || len(store.Versions)!=1 || e.Storage.IndexRoots[indexRef.Address]!=root { t.Fatal("metadata rewrote physical backing") }
	// An equal table assignment must still repair the first index attribute.
	changedIndex := nextIndex.WithColumnStorage(0,catalog.ColumnStorage{Storage:"m",Compression:"p"})
	changedIndex.ObjectRevision++
	e.Catalog.State.SetDescriptor(indexRef.Address,changedIndex)
	enginetest.Run(t,e,`ALTER TABLE t ALTER a SET STORAGE EXTERNAL`)
	if e.Catalog.State.MustRelation(indexRef.Address).ColumnStorage(0).Storage!="e" { t.Fatal("same-value assignment skipped index") }
	enginetest.Run(t,e,`ALTER TABLE t RENAME a TO renamed`)
	enginetest.Run(t,e,`ALTER TABLE t DROP renamed`)
	retired := e.Catalog.State.MustRelation(ref.Address)
	history := retired.ColumnMeta.Dropped[2]
	if history.Facts.Storage!="e" || history.Compression!="p" || len(retired.ColumnMeta.Storage)!=0 { t.Fatal("effective dropped history missing",history) }
	enginetest.Run(t,e,`ALTER TABLE t ADD renamed text`)
	readded := e.Catalog.State.MustRelation(ref.Address)
	if readded.Attributes[1]!=4 || readded.ColumnStorage(1)!=(catalog.ColumnStorage{Storage:"x"}) { t.Fatal("readd reused retired policy") }
}

func TestAlterStoragePublicationFences(t *testing.T) {
	for _,kind:=range []string{"cancel","reset","restore","role","auth","end","veto"} {
		t.Run(kind,func(t *testing.T) {
			e:=engine.New()
			enginetest.Run(t,e,`CREATE TABLE t(a text)`)
			enginetest.Run(t,e,`INSERT INTO t VALUES('payload')`)
			enginetest.Run(t,e,`CREATE INDEX storage_idx ON t(a)`)
			ref,indexRef:=enginetest.RoleTableRef(t,e,"t"),enginetest.RoleTableRef(t,e,"storage_idx")
			old,oldIndex:=e.Catalog.State.Get(ref.Address),e.Catalog.State.Get(indexRef.Address)
			store,root:=e.Storage.Relations[ref.Address],e.Storage.IndexRoots[indexRef.Address]
			ctx,cancel:=context.WithCancel(context.Background());defer cancel()
			op,cleanup,err:=enginetest.Operation(e,ctx);if err!=nil {t.Fatal(err)};defer cleanup()
			provider:=e.Catalog.Providers[ids.RelationClassID]
			veto:=errors.New("storage provider veto")
			e.Catalog.Providers[ids.RelationClassID]=alterStorageVetoProvider{Provider:provider, run:func(){
				e.Catalog.Providers[ids.RelationClassID]=provider
				switch kind {
				case "cancel":cancel()
				case "reset":if err:=engine.Reset(context.Background(),e);err!=nil {t.Fatal(err)}
				case "restore":if err:=engine.Restore(context.Background(),e,strings.NewReader("CREATE TABLE sentinel(i integer)"));err!=nil {t.Fatal(err)}
				case "role":enginetest.Run(t,e,"CREATE USER storage_changed")
				case "auth":op.Session.Mu.Lock();op.Session.Authorization.Revision++;op.Session.Mu.Unlock()
				case "end":op.Session.RequestEnd(session.CleanClose)
				}
			}, failure:func()error {if kind=="veto" {return veto};return nil}}
			_,err=e.Execute(op,enginetest.Statement(t,`ALTER TABLE t ALTER a SET STORAGE EXTERNAL, ALTER a SET COMPRESSION pglz`),0,nil)
			if err==nil {t.Fatal("rejected final publication succeeded")}
			if kind=="cancel" && !errors.Is(err,context.Canceled) || kind=="veto" && !errors.Is(err,veto) {t.Fatal(err)}
			if kind=="reset" || kind=="restore" { if !errors.Is(err,ids.ErrCatalogReplaced) {t.Fatal(err)} } else if e.Catalog.State.Get(ref.Address)!=old || e.Catalog.State.Get(indexRef.Address)!=oldIndex || e.Storage.Relations[ref.Address]!=store || e.Storage.IndexRoots[indexRef.Address]!=root { t.Fatal("failed stage leaked metadata/backing",err) }
			enginetest.LockEmpty(t,e)
		})
	}
}

type alterStorageVetoProvider struct {
	catalog.Provider
	run func()
	failure func()error
}
func (p alterStorageVetoProvider) Validate(view catalog.Read, d catalog.Descriptor) error {
	p.run()
	if err:=p.failure();err!=nil {return err}
	return p.Provider.Validate(view,d)
}

func TestAlterStorageRevisionExhaustion(t *testing.T) {
	for _, target := range []string{"t", "storage_exhausted"} {
		t.Run(target, func(t *testing.T) {
			e := engine.New()
			enginetest.Run(t,e,`CREATE TABLE t(a text)`)
			enginetest.Run(t,e,`CREATE INDEX storage_exhausted ON t(a)`)
			ref,indexRef := enginetest.RoleTableRef(t,e,"t"),enginetest.RoleTableRef(t,e,"storage_exhausted")
			selected := ref
			if target != "t" { selected = indexRef }
			old := e.Catalog.State.MustRelation(selected.Address)
			exhausted := *old
			exhausted.ObjectRevision = ids.DefinitionRevision(^uint64(0))
			e.Catalog.State.SetDescriptor(selected.Address,&exhausted)
			before,store,root := e.Catalog.State,e.Storage.Relations[ref.Address],e.Storage.IndexRoots[indexRef.Address]
			_,err := enginetest.Execute(e,context.Background(),enginetest.Statement(t,`ALTER TABLE t ALTER a SET STORAGE EXTERNAL`),nil)
			if err == nil || e.Catalog.State != before || e.Catalog.State.Get(selected.Address) != &exhausted || e.Storage.Relations[ref.Address] != store || e.Storage.IndexRoots[indexRef.Address] != root {
				t.Fatal("revision exhaustion published or rewrote backing",err)
			}
			enginetest.LockEmpty(t,e)
		})
	}
}
