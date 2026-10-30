package reference_test
import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)
func signalInitial(pg bool)fixture.Step{acl:=recoveryControlOwnerACL(pg);return fixture.Select("SELECT objoid::bigint AS oid,initprivs::text AS acl FROM pg_init_privs WHERE classoid=1255 AND objoid IN (2621,2622,4543) ORDER BY objoid",[]string{"oid","acl"},[]string{"INT8","TEXT"},[][]any{{int64(2621),acl},{int64(2622),acl},{int64(4543),acl}})}
func TestReferenceServerSignalCatalog(t *testing.T){signalScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,n *[]fixture.Diagnostic){
 q:=`SELECT oid::bigint AS oid,proname,pronamespace::bigint AS namespace,proowner::bigint AS owner,prolang::bigint AS language,prokind::text AS kind,prorettype::bigint AS result,proretset,proisstrict,provolatile::text AS volatility,proparallel::text AS parallel,procost,prorows,pronargdefaults,provariadic::bigint AS variadic,prosecdef,proleakproof,prosrc,proargtypes::text AS inputs,proallargtypes::text AS alltypes,proargmodes::text AS modes,proargnames::text AS names,proacl::text AS acl FROM pg_proc WHERE oid IN (2621,2622,4543) ORDER BY oid`
 names:=[]string{"oid","proname","namespace","owner","language","kind","result","proretset","proisstrict","volatility","parallel","procost","prorows","pronargdefaults","variadic","prosecdef","proleakproof","prosrc","inputs","alltypes","modes","names","acl"}
 types:=[]string{"INT8","NAME","INT8","INT8","INT8","TEXT","INT8","BOOL","BOOL","TEXT","TEXT","FLOAT4","FLOAT4","INT2","INT8","BOOL","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT","TEXT"}
 var rows [][]any
 for i,oid:=range []int64{2621,2622,4543}{inputs:="";if i==2{inputs="23"};rows=append(rows,[]any{oid,signalNames[i],int64(11),int64(10),int64(12),"f",int64(16),false,true,"v","s",float64(1),float64(0),int64(0),int64(0),false,false,signalNames[i],inputs,nil,nil,nil,recoveryControlOwnerACL(b.PG)})}
 signalRoutes(t,p,b,c,fixture.Select(q,names,types,rows));signalRoutes(t,p,b,c,signalInitial(b.PG));signalNotices(t,n)
})}
