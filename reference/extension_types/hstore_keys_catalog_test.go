package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func hstoreKeysCatalog(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _,tc:=range []struct{signature,symbol,result string}{
 {"exist(hstore,text)","hstore_exists","boolean"},{"exists_all(hstore,text[])","hstore_exists_all","boolean"},{"exists_any(hstore,text[])","hstore_exists_any","boolean"},{"hs_contains(hstore,hstore)","hstore_contains","boolean"},{"hs_contained(hstore,hstore)","hstore_contained","boolean"},{"akeys(hstore)","hstore_akeys","text[]"},{"avals(hstore)","hstore_avals","text[]"},
 } {
 q:=`SELECT p.prosrc,p.probin,p.proisstrict,p.proretset,p.provolatile::text,p.proparallel::text,p.prorettype=$2::regtype AS result,l.lanname::text AS language FROM (SELECT * FROM pg_proc WHERE oid=$1::regprocedure) p JOIN pg_language l ON l.oid=p.prolang`
 hstoreRun(t,p,b,c,fixture.Select(q,[]string{"prosrc","probin","proisstrict","proretset","provolatile","proparallel","result","language"},[]string{"TEXT","TEXT","BOOL","BOOL","TEXT","TEXT","BOOL","TEXT"},[][]any{{tc.symbol,"$libdir/hstore",true,false,"i","s",true,"c"}},tc.signature,tc.result))
 hstoreRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_proc'::regclass AND objid=$1::regprocedure AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},tc.signature))
 }
 for _,tc:=range []struct{signature,right,backer,commutator string}{
 {"?(hstore,text)","text","exist(hstore,text)",""},{"?&(hstore,text[])","text[]","exists_all(hstore,text[])",""},{"?|(hstore,text[])","text[]","exists_any(hstore,text[])",""},{"@>(hstore,hstore)","hstore","hs_contains(hstore,hstore)","<@(hstore,hstore)"},{"<@(hstore,hstore)","hstore","hs_contained(hstore,hstore)","@>(hstore,hstore)"},
 } {
 q:=`SELECT oprleft='hstore'::regtype AS lhs,oprright=$2::regtype AS rhs,oprresult=16 AS result,oprcode=$3::regprocedure AS backing,oprrest=5040 AS restriction,oprjoin=5041 AS joining,oprnegate=0 AS no_negator,oprcanhash,oprcanmerge FROM pg_operator WHERE oid=$1::regoperator`
 hstoreRun(t,p,b,c,fixture.Select(q,[]string{"lhs","rhs","result","backing","restriction","joining","no_negator","oprcanhash","oprcanmerge"},[]string{"BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL","BOOL"},[][]any{{true,true,true,true,true,true,true,false,false}},tc.signature,tc.right,tc.backer))
 comm:=`oprcom=0`;args:=[]any{tc.signature};if tc.commutator!="" {comm=`oprcom=$2::regoperator`;args=append(args,tc.commutator)}
 hstoreRun(t,p,b,c,fixture.Select("SELECT "+comm+" AS reciprocal FROM pg_operator WHERE oid=$1::regoperator",[]string{"reciprocal"},[]string{"BOOL"},[][]any{{true}},args...))
 hstoreRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_depend WHERE classid='pg_operator'::regclass AND objid=$1::regoperator AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore') AND deptype='e'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},tc.signature))
 }
}

func TestReferenceHstoreKeysCatalog(t *testing.T) {
 hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){hstoreInstall(t,p,b,c);hstoreKeysCatalog(t,p,b,c)})
}
