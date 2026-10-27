//go:build !noservertests

package reference_test

import (
	"testing"
	"stuntgres/reference/fixture"
)

func TestReferencePlannerSettingsPortalAdmission(t *testing.T) {
	f:=fixture.NewServerFixture(t)
	for _, b:=range f.Backends() {
		t.Run(b.Name,func(t *testing.T) {
			w:=serverAuthenticated(t,f.DSNFor(b))
			for _, q:=range []string{"CREATE TABLE pm_portal(k integer)","INSERT INTO pm_portal VALUES(2),(1),(1),(3)","BEGIN","SET enable_hashagg=off"} { serverWantCode(t,w.query(t,q),"") }
			defer func(){ serverWantCode(t,w.query(t,"ROLLBACK"),"") }()
			q:="SELECT DISTINCT k FROM pm_portal ORDER BY k"
			frames:=serverCycle(t,w,serverParse("pm_statement",q),serverBind("pm_old","pm_statement",nil,nil))
			serverWantCode(t,frames,"")
			frames=serverCycle(t,w,serverExecute("pm_old",1))
			serverWantCode(t,frames,""); serverWantValues(t,frames,[][]string{{"1"}}); serverWantTags(t,frames,"DsZ")
			serverWantCode(t,w.query(t,"SET enable_hashagg=on"),"")
			frames=serverCycle(t,w,serverExecute("pm_old",0))
			serverWantCode(t,frames,""); serverWantValues(t,frames,[][]string{{"2"},{"3"}})
			frames=serverCycle(t,w,serverBind("pm_new","pm_statement",nil,nil),serverExecute("pm_new",0))
			serverWantCode(t,frames,""); serverWantValues(t,frames,[][]string{{"1"},{"2"},{"3"}})
			serverWantCode(t,serverCycle(t,w,serverClose('P',"pm_old"),serverClose('P',"pm_new"),serverClose('S',"pm_statement")),"")
			serverWantCode(t,w.query(t,"COMMIT"),"")
		})
	}
}
