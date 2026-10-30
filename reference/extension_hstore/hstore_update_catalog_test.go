package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func hstoreUpdateCatalog(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _,tc:=range []struct{signature,symbol string}{
 {"hs_concat(hstore,hstore)","hstore_concat"},{"delete(hstore,text)","hstore_delete"},{"delete(hstore,text[])","hstore_delete_array"},{"delete(hstore,hstore)","hstore_delete_hstore"},{"slice(hstore,text[])","hstore_slice_to_hstore"},
 } {
 q:=`SELECT p.prosrc,p.probin,p.proisstrict,p.proretset,p.provolatile::text,p.proparallel::text,p.prorettype='hstore'::regtype AS result,l.lanname::text AS language FROM (SELECT * FROM pg_proc WHERE oid=$1::regprocedure) p JOIN pg_language l ON l.oid=p.prolang`
 hstoreRun(t,p,b,c,fixture.Select(q,[]string{"prosrc","probin","proisstrict","proretset","provolatile","proparallel","result","language"},[]string{"TEXT","TEXT","BOOL","BOOL","TEXT","TEXT","BOOL","TEXT"},[][]any{{tc.symbol,"$libdir/hstore",true,false,"i","s",true,"c"}},tc.signature))
 hstoreRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},tc.signature))
 }
 for _,tc:=range []struct{signature,right,backer string}{
 {"||(hstore,hstore)","hstore","hs_concat(hstore,hstore)"},{"-(hstore,text)","text","delete(hstore,text)"},{"-(hstore,text[])","text[]","delete(hstore,text[])"},{"-(hstore,hstore)","hstore","delete(hstore,hstore)"},
 } {
 q:=`SELECT oprleft='hstore'::regtype AS lhs,oprright=$2::regtype AS rhs,oprresult='hstore'::regtype AS result,oprcode=$3::regprocedure AS backing,oprrest=0 AS restriction,oprjoin=0 AS joining,oprnegate=0 AS no_negator,oprcom=0 AS no_commutator,oprcanhash,oprcanmerge FROM pg_operator WHERE oid=$1::regoperator`
 hstoreRun(t,p,b,c,fixture.Select(q,[]string{"lhs","rhs","result","backing","restriction","joining","no_negator","no_commutator","oprcanhash","oprcanmerge"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true,true,true,false,false}},tc.signature,tc.right,tc.backer))
 hstoreRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_operator'::regclass AND objid=$1::regoperator AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},tc.signature))
 }
}

func TestReferenceHstoreUpdateCatalog(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 hstoreRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='hs_concat'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
 hstoreInstall(t,p,b,c);hstoreUpdateCatalog(t,p,b,c)
 for _,prepared:=range []bool{false,true}{s:=fixture.Select(`SELECT 'a'::text || 'b'::text AS s,7-2 AS n`,[]string{"s","n"},[]string{"TEXT","INT4"},[][]any{{"ab",int64(5)}});s.Prepared=prepared;hstoreRun(t,p,b,c,s)}
 })
}
