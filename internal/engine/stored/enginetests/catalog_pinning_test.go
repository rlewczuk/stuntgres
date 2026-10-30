package enginetests_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestDependPinnedLanguageSeeds(t *testing.T) {
	e:=engine.New()
	s:=enginetest.StartSession(t,e,nil)
	// Install a genuine user language through ordinary DDL, not a fabricated
	// descriptor whose name/OID happens to resemble a bootstrap authority.
	enginetest.SessionSQL(t,s,"CREATE LANGUAGE pin_user_language HANDLER pg_catalog.plpgsql_call_handler")
	view:=e.CatalogRead()
	for _,tc:=range []struct{name string; fixed ids.OID; pinned bool}{
		{"internal",12,true},
		{"c",13,true},
		{"sql",14,true},
		{"plpgsql",0,false},
		{"pin_user_language",0,false},
	} {
		t.Run(tc.name,func(t *testing.T){
			address:=view.NameAddress(ids.NameKey{ClassID:ids.LanguageClassID,Name:tc.name})
			language,ok:=view.Get(address).(*catalog.Language)
			if !ok || language.Name()!=tc.name || address.ObjectID==0 {t.Fatalf("missing actual language %q: address=%v descriptor=%T",tc.name,address,view.Get(address))}
			if tc.fixed!=0 && address!=(ids.ObjectAddress{ClassID:ids.LanguageClassID,ObjectID:tc.fixed}) {t.Fatalf("language %q address=%v want fixed OID %d",tc.name,address,tc.fixed)}
			if got:=stored.DependPinned(view,nil,address);got!=tc.pinned {t.Errorf("actual language %q (%v) pinned=%v want %v",tc.name,address,got,tc.pinned)}
		})
	}
}
