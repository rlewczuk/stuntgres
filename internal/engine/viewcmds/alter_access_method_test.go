package viewcmds_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/viewcmds"
)

func TestAlterAccessMethodMaterializedFence(t *testing.T) {
	for _,phase:=range []string{"private-definition","stale-publication"} {t.Run(phase,func(t *testing.T) {
		e:=engine.New();old:=refreshUnitRelation(t,e)
		s:=enginetest.StartSession(t,e,nil)
		enginetest.SessionSQL(t,s,"CREATE ACCESS METHOD method_mv_heap TYPE TABLE HANDLER heap_tableam_handler")
		base:=e.CatalogRead();method,_:=base.LookupAccessMethod("method_mv_heap")
		store:=e.Storage.Relations[old.Address()]
		stage:=e.BeginCatalogStage(base);defer e.DiscardCatalogStage(stage)
		if err:=viewcmds.StageMaterializedRows(e,t.Context(),stage,old,store,refreshUnitRows(t,old,1),true,ids.TxRef{});err!=nil {t.Fatal(err)}
		if phase=="private-definition" {
			// Both AMs are genuine; arbitrary defining metadata still cannot be
			// attached to the refresh constructor's storage replacement token.
			stage.MaterializedOrZero().Replacement.Descriptor.TableMethod=method.Address().ObjectID
			if _,err:=e.ValidateCatalogStage(t.Context(),stage);err==nil {t.Fatal("refresh constructor accepted changed method")}
			if e.CatalogRead().Get(old.Address())!=old || old.TableMethod!=2 || e.Storage.Relations[old.Address()]!=store {t.Fatal("invalid private refresh leaked")}
		} else {
			prepared,err:=e.ValidateCatalogStage(t.Context(),stage);if err!=nil {t.Fatal(err)}
			name:=`ALTER TABLE "public"."`+old.Name()+`" SET ACCESS METHOD method_mv_heap`
			enginetest.SessionSQL(t,s,name)
			current:=e.CatalogRead().MustRelation(old.Address())
			if _,err=enginetest.PublishCatalog(e,prepared,core.PublicationToken{Epoch:base.Epoch()});err==nil {t.Fatal("old-method refresh overwrote genuine transition")}
			if e.CatalogRead().Get(old.Address())!=current || current.TableMethod!=method.Address().ObjectID || old.TableMethod!=2 || e.Storage.Relations[old.Address()]!=store {t.Fatal("stale refresh changed method or rows")}
		}
	})}
}
