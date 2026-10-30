package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceRelationStatisticsRestoreClear(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a int)")
 s.run(fixture.Exec("INSERT INTO rst_t VALUES(7),(8)",2))
 s.restore("'relpages',18::integer,'reltuples',21::real,'relallvisible',24::integer,'relallfrozen',27::integer",true)
 s.stats(18,21,24,27)
 s.restore("'reltuples',21.5::real",true);s.stats(18,21.5,24,27)
 s.restore("",true);s.stats(18,21.5,24,27)
 s.restore("'relpages',-17::integer,'reltuples',-1::real,'relallvisible',-24::integer,'relallfrozen',-27::integer",true)
 s.stats(-17,-1,-24,-27)
 s.run(fixture.Select("SELECT count(*) FROM rst_t",[]string{"count"},[]string{"INT8"},[][]any{{int64(2)}}))
 s.run(fixture.Select("SELECT a FROM rst_t ORDER BY a",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)},{int64(8)}}))
 s.run(fixture.Select("SELECT pg_restore_attribute_stats('schemaname','public','relname','rst_t','attname','a','inherited',false,'null_frac',.25::real,'n_distinct',9::real)",[]string{"pg_restore_attribute_stats"},[]string{"BOOL"},[][]any{{true}}))
 s.clear();s.stats(0,-1,0,0)
 s.run(fixture.Select("SELECT null_frac,n_distinct FROM pg_stats WHERE schemaname='public' AND tablename='rst_t' AND attname='a' AND NOT inherited",[]string{"null_frac","n_distinct"},[]string{"FLOAT4","FLOAT4"},[][]any{{float64(.25),float64(9)}}))
 s.run(fixture.Select("SELECT oid::integer,proname::text,prorettype::integer,provariadic::integer,proisstrict,provolatile::text,proparallel::text,proargnames::text,proargmodes::text,proallargtypes IS NULL AS noall FROM pg_proc WHERE oid IN (6362,6397) ORDER BY oid",[]string{"oid","proname","prorettype","provariadic","proisstrict","provolatile","proparallel","proargnames","proargmodes","noall"},[]string{"INT4","TEXT","INT4","INT4","BOOL","TEXT","TEXT","TEXT","TEXT","BOOL"},[][]any{{int64(6362),"pg_restore_relation_stats",int64(16),int64(2276),false,"v","u","{kwargs}","{v}",true},{int64(6397),"pg_clear_relation_stats",int64(2278),int64(0),false,"v","u","{schemaname,relname}",nil,true}}))
})}

func TestReferenceRelationStatisticsValidation(t *testing.T){withRelationStats(t,func(s relationStatsScenario){
 s.exec("CREATE TABLE rst_t(a int)")
 s.restore("'relpages',18,'reltuples',21.5::real,'relallvisible',24,'relallfrozen',27",true)
 s.warnings("'relpages','nope','reltuples',22.5::real,'relallvisible',31",relationStatsWarning("01000",`argument "relpages" has type text, expected type integer`))
 s.stats(18,22.5,31,27)
 s.warnings("'unknown',1,'relpages',19",relationStatsWarning("01000",`unrecognized argument name: "unknown"`));s.stats(19,22.5,31,27)
 *s.notices=nil;s.restore("'unknown',NULL,'version','ignored','version',NULL",true)
 if len(*s.notices)!=0{s.t.Fatal(*s.notices)}
 s.warnings("'relpages',1::bigint,'reltuples',22.5::numeric",relationStatsWarning("01000",`argument "relpages" has type bigint, expected type integer`),relationStatsWarning("01000",`argument "reltuples" has type numeric, expected type real`))
 for _,v:=range []string{"-2","'NaN'","'Infinity'","'-Infinity'"}{
  message:=`argument "reltuples" must be a finite value`;if v=="-2"{message=`argument "reltuples" must not be less than -1.0`}
  s.warnings("'reltuples',"+v+"::real,'relpages',20",relationStatsWarning("22023",message));s.stats(20,22.5,31,27)
 }
 s.restore("'RELPAGES',21,'relpages',22,'relpages',NULL,'reltuples',-2::real,'reltuples',23.5::real,'version',1",true);s.stats(22,23.5,31,27)
 s.warnings("'relpages',23,'relpages','wrong'",relationStatsWarning("01000",`argument "relpages" has type text, expected type integer`));s.stats(23,23.5,31,27)
 s.restore("'schemaname','absent','schemaname','public','relname','missing','relname','rst_t'",true)
 s.run(fixture.Select("SELECT pg_restore_relation_stats(VARIADIC '{schemaname,public,relname,rst_t}'::text[])",[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{true}}))
 *s.notices=nil
 s.run(fixture.Select("SELECT pg_restore_relation_stats(VARIADIC '{schemaname,public,relname,rst_t,relpages,9}'::text[])",[]string{"pg_restore_relation_stats"},[]string{"BOOL"},[][]any{{false}}))
 if len(*s.notices)!=1||(*s.notices)[0]!=relationStatsWarning("01000",`argument "relpages" has type text, expected type integer`){s.t.Fatal(*s.notices)}
 s.stats(23,23.5,31,27)
 *s.notices=nil;s.fail("SELECT pg_restore_relation_stats('unknown',1,'relpages',99)","22023",`argument "schemaname" must not be null`)
 if len(*s.notices)!=1||(*s.notices)[0]!=relationStatsWarning("01000",`unrecognized argument name: "unknown"`){s.t.Fatal(*s.notices)}
 s.stats(23,23.5,31,27)
 // U+017F folds to ASCII s in Go EqualFold, but never in C/UTF8 pg_strcasecmp.
 *s.notices=nil
 s.fail("SELECT pg_restore_relation_stats('ſchemaname','public','relname','rst_t','reltuples',99::real)","22023",`argument "schemaname" must not be null`)
 if len(*s.notices)!=1||(*s.notices)[0]!=relationStatsWarning("01000",`unrecognized argument name: "ſchemaname"`){s.t.Fatal(*s.notices)}
 s.stats(23,23.5,31,27)
 s.warnings("'verſion',1,'reltuples',24.5::real",relationStatsWarning("01000",`unrecognized argument name: "verſion"`))
 s.stats(23,24.5,31,27)
})}
