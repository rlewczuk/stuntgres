package core_test

import (
	"context"
	"errors"
	"testing"
	"time"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func heapUnitNestedRetention(t *testing.T) {
	for _,tc:=range []struct{name string;setup []string;invoke string}{
		{"sql_routine",[]string{"CREATE FUNCTION retained_nested_run() RETURNS integer LANGUAGE SQL AS $$UPDATE retained_nested SET n=2; UPDATE retained_nested SET n=3; SELECT 1$$"},"SELECT retained_nested_run()"},
		{"rule",[]string{"CREATE TABLE retained_driver(n integer)","CREATE RULE retained_updates AS ON INSERT TO retained_driver DO ALSO (UPDATE retained_nested SET n=2; UPDATE retained_nested SET n=3)"},"INSERT INTO retained_driver VALUES(1)"},
		{"trigger",[]string{"CREATE TABLE retained_driver(n integer)","CREATE FUNCTION retained_nested_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN UPDATE retained_nested SET n=2; UPDATE retained_nested SET n=3; RETURN NULL; END$$","CREATE TRIGGER retained_updates AFTER INSERT ON retained_driver FOR EACH ROW EXECUTE FUNCTION retained_nested_trigger()"},"INSERT INTO retained_driver VALUES(1)"},
		{"exception",[]string{"CREATE FUNCTION retained_nested_run() RETURNS integer LANGUAGE plpgsql AS $$BEGIN UPDATE retained_nested SET n=2; BEGIN UPDATE retained_nested SET n=99; RAISE EXCEPTION 'discard private version'; EXCEPTION WHEN OTHERS THEN PERFORM 1; END; UPDATE retained_nested SET n=3; RETURN 1; END$$"},"SELECT retained_nested_run()"},
	}{
		t.Run(tc.name,func(t *testing.T){
			e:=engine.New()
			pgstatindexUnitInstalled(t,e)
			for _,q:=range []string{"CREATE TABLE retained_nested(n integer)","INSERT INTO retained_nested VALUES(1)","CREATE INDEX retained_nested_index ON retained_nested(n)"} {enginetest.Run(t,e,q)}
			for _,q:=range tc.setup {enginetest.Run(t,e,q)}
			base:=pgstatindexCaptureFixture(t,e,nil,"retained_nested_index")
			s:=sessionStart(t,e,nil)
			sessionSQL(t,s,"BEGIN")
			sessionSQL(t,s,tc.invoke)
			writes:=s.Transactions.Writes[base.Parent.Address()]
			if len(writes)!=1 || heapPredecessorCount(writes[0].PrivateHistory)!=2 {t.Fatal("nested owner lost real repeated UPDATE versions",writes)}
			typ,_:=e.CatalogRead().LookupType(23)
			discarded,err:=typ.Input(context.Background(),"99",-1)
			if err!=nil {t.Fatal(err)}
			for prior:=writes[0].PrivateHistory;prior!=nil;prior=prior.Next {if sqltype.SameDatum(prior.Version.Values[0],discarded) {t.Fatal("exception rollback resurrected a discarded version")}}
			private:=writes[0].PrivateHistory
			sessionSQL(t,s,"COMMIT")
			after:=pgstatindexCaptureFixture(t,e,nil,"retained_nested_index")
			if heapPredecessorCount(after.Store.Retained)!=2 || after.Store.Versions[0].PrivateHistory!=nil || heapPredecessorCount(private)!=2 {t.Fatal("nested publication stranded or mutated physical ownership")}
			sessionSQL(t,s,"BEGIN")
			sessionSQL(t,s,"UPDATE retained_nested SET n=4")
			abandoned:=s.Transactions.Writes[base.Parent.Address()][0].PrivateHistory
			s.RequestEnd(session.CleanClose)
			ctx,cancel:=context.WithTimeout(context.Background(),5*time.Second)
			err=s.WaitEnd(ctx)
			cancel()
			if err!=nil || s.Transactions.Writes!=nil || heapPredecessorCount(abandoned)!=1 || e.Storage.Relations[base.Parent.Address()].Retained!=after.Store.Retained {t.Fatal("session abort stranded a private root or changed committed history",err)}
		})
	}
}

func heapUnitHeapReplacements(t *testing.T) {
	e:=engine.New()
	pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE replace_heap(n integer)","INSERT INTO replace_heap VALUES(1)","CREATE INDEX replace_index ON replace_heap(n)","UPDATE replace_heap SET n=2"} {enginetest.Run(t,e,q)}
	base:=pgstatindexCaptureFixture(t,e,nil,"replace_index")
	if heapPredecessorCount(base.Store.Retained)!=1 {t.Fatal("fixture lacks committed physical history")}
	s:=sessionStart(t,e,nil)
	for _,q:=range []string{"BEGIN","UPDATE replace_heap SET n=3","ALTER TABLE replace_heap ADD COLUMN s text"} {sessionSQL(t,s,q)}
	shape:=s.Transactions.Catalog.Storage[base.Parent.Address()]
	if shape.Replacement==nil || shape.Replacement.Retained==nil || len(shape.Replacement.Retained.Version.Values)!=2 || len(base.Store.Retained.Version.Values)!=1 {t.Fatal("projection changed old payload or lost physical history")}
	projected:=shape.Replacement
	writes:=s.Transactions.Writes[base.Parent.Address()]
	if len(writes)!=1 || writes[0].PrivateHistory==nil || len(writes[0].PrivateHistory.Version.Values)!=2 {t.Fatal("projection omitted private predecessor payload")}
	sessionSQL(t,s,"ALTER TABLE replace_heap ALTER COLUMN n TYPE bigint")
	rewritten:=s.Transactions.Catalog.Storage[base.Parent.Address()].Replacement
	if rewritten==projected || rewritten.Retained!=nil || projected.Retained==nil {t.Fatal("genuine rewrite retained an old physical history root")}
	for _,row:=range s.Transactions.Writes[base.Parent.Address()] {if row.PrivateHistory!=nil {t.Fatal("rewrite retained private old-layout history")}}
	sessionSQL(t,s,"ROLLBACK")
	if e.Storage.Relations[base.Parent.Address()].Retained!=base.Store.Retained || len(base.Store.Retained.Version.Values)!=1 {t.Fatal("rollback did not preserve original immutable heap")}
	for _,q:=range []string{"BEGIN","TRUNCATE replace_heap"} {sessionSQL(t,s,q)}
	truncated:=s.Transactions.Catalog.Storage[base.Parent.Address()].Replacement
	if truncated.Retained!=nil || len(truncated.Versions)!=0 {t.Fatal("TRUNCATE revived old physical versions")}
	sessionSQL(t,s,"COMMIT")
	if e.Storage.Relations[base.Parent.Address()]!=truncated || truncated.Retained!=nil || base.Store.Retained==nil {t.Fatal("TRUNCATE publication changed an old capture")}
}

func heapUnitLogicalReplacement(t *testing.T) {
	e:=engine.New()
	ext:=pgstatindexUnitInstalled(t,e)
	for _,q:=range []string{"CREATE TABLE control_heap(n integer)","INSERT INTO control_heap VALUES(1)","CREATE INDEX control_index ON control_heap(n)","UPDATE control_heap SET n=2","UPDATE control_heap SET n=3"} {enginetest.Run(t,e,q)}
	old:=pgstatindexCaptureFixture(t,e,nil,"control_index")
	if heapPredecessorCount(old.Store.Retained)!=2 || len(old.Root.Entries)!=1 {t.Fatal("fixture lacks committed heap history or current index membership")}
	s:=sessionStart(t,e,nil)
	for _,q:=range []string{"BEGIN","UPDATE control_heap SET n=4"} {sessionSQL(t,s,q)}
	private:=s.Transactions.Writes[old.Parent.Address()][0]
	before:=e.CatalogRead()
	bad:=catalog.CloneState(before.State())
	bad.RemoveDescriptor(ext.Members[0])
	if _,err:=e.ReplaceCatalog(context.Background(),e,e.Catalog.Read(bad));err==nil {t.Fatal("replacement accepted an invalid real provider manifest")}
	ctx,cancel:=context.WithCancel(context.Background())
	boundary:=&cancelAfterPreflightContext{Context:ctx,cancel:cancel}
	_,err:=e.ReplaceCatalog(boundary,e,before)
	cancel()
	if !errors.Is(err,context.Canceled) {t.Fatal("replacement did not preserve real context cancellation",err)}
	failed:=pgstatindexCaptureFixture(t,e,nil,"control_index")
	if e.CatalogRead().State()!=before.State() || failed.Root!=old.Root || failed.Store.Retained!=old.Store.Retained || !storage.SameVersionRoot(failed.Store.Versions,old.Store.Versions) || s.Transactions.Writes[old.Parent.Address()][0].PrivateHistory!=private.PrivateHistory {t.Fatal("failed replacement changed committed or private physical roots")}
	if _,err:=e.ReplaceCatalog(context.Background(),e,before);err!=nil {t.Fatal(err)}
	imported:=pgstatindexCaptureFixture(t,e,nil,"control_index")
	if imported.Store.Retained!=nil || len(imported.Root.Entries)!=1 || len(imported.Store.Versions)!=1 || imported.Store.Versions[0].PrivateHistory!=nil || imported.Store.Versions[0].Version==old.Store.Versions[0].Version || !sqltype.SameDatum(imported.Store.Versions[0].Values[0],old.Store.Versions[0].Values[0]) {t.Fatal("logical replacement imported private/dead history or reused an old physical incarnation")}
	if heapPredecessorCount(old.Store.Retained)!=2 || len(old.Root.Entries)!=1 || private.PrivateHistory.Version.Born==0 {t.Fatal("logical replacement mutated retained old captures")}
	s.RequestEnd(session.CleanClose)
	ctx,cancel=context.WithTimeout(context.Background(),5*time.Second)
	err=s.WaitEnd(ctx)
	cancel()
	if err!=nil {t.Fatal(err)}
}
