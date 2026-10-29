package reference_test

import (
 "database/sql"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

var ppsColumns = []string{"name", "statement", "prepare_time", "parameter_types", "result_types", "from_sql", "generic_plans", "custom_plans"}
var ppsTypes = []string{"TEXT", "TEXT", "TIMESTAMPTZ", "_REGTYPE", "_REGTYPE", "BOOL", "INT8", "INT8"}

func ppsClock(t *testing.T, p *fixture.Pair, c *sql.Conn) time.Time {
 t.Helper()
 var instant time.Time
 if err := c.QueryRowContext(p.Ctx, "SELECT statement_timestamp()").Scan(&instant); err != nil { t.Fatal(err) }
 return instant
}
func ppsCreated(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) time.Time {
 t.Helper()
 var instant time.Time
 if err := c.QueryRowContext(p.Ctx, "SELECT prepare_time FROM pg_catalog.pg_prepared_statements WHERE name=$1", name).Scan(&instant); err != nil { t.Fatal(err) }
 return instant
}
func ppsSignature(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, source, parameters string, results any, fromSQL bool) {
 t.Helper()
 sqlPreparedStep(t,p,b,c,fixture.Select("SELECT name,statement,parameter_types::text,result_types::text,from_sql FROM pg_catalog.pg_prepared_statements WHERE name=$1", []string{"name","statement","parameter_types","result_types","from_sql"}, []string{"TEXT","TEXT","TEXT","TEXT","BOOL"}, [][]any{{name,source,parameters,results,fromSQL}},name))
}
func ppsAbsent(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,name string) {
 t.Helper()
 sqlPreparedStep(t,p,b,c,fixture.Select("SELECT name FROM pg_catalog.pg_prepared_statements WHERE name=$1",[]string{"name"},[]string{"TEXT"},[][]any{},name))
}

func TestReferencePGPreparedStatementsSchema(t *testing.T) {
 sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  for _, qualified := range []string{"pg_catalog.pg_prepared_statements","pg_prepared_statements"} {
   q := "SELECT * FROM "+qualified+" WHERE false"
   sqlPreparedStep(t,p,b,c,fixture.Select(q,ppsColumns,ppsTypes,[][]any{}))
   stmt,err:=c.PrepareContext(p.Ctx,q); if err!=nil {t.Fatal(err)}
   sqlPreparedStep(t,p,b,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},fixture.Select(q,ppsColumns,ppsTypes,[][]any{}))
   if err:=stmt.Close();err!=nil {t.Fatal(err)}
  }
  source:="PREPARE pps_schema AS SELECT 1"
  sqlPreparedStep(t,p,b,c,fixture.Exec(source,0));created:=ppsCreated(t,p,c,"pps_schema").UTC()
  var generic,custom any;if b.PG {generic=int64(0);custom=int64(0)}
  q:="SELECT * FROM pg_catalog.pg_prepared_statements WHERE name='pps_schema'"
  full:=fixture.Select(q,ppsColumns,ppsTypes,[][]any{{"pps_schema",source,created,"{}","{integer}",true,generic,custom}})
  sqlPreparedStep(t,p,b,c,full)
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
  sqlPreparedStep(t,p,b,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},full)
  sqlPreparedStep(t,p,b,c,fixture.Select("SELECT a.attname,a.atttypid::bigint FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_class c ON a.attrelid=c.oid JOIN pg_catalog.pg_namespace n ON c.relnamespace=n.oid WHERE n.nspname='pg_catalog' AND c.relname='pg_prepared_statements' AND a.attnum>0 ORDER BY a.attnum",[]string{"attname","atttypid"},[]string{"NAME","INT8"},[][]any{{"name",int64(25)},{"statement",int64(25)},{"prepare_time",int64(1184)},{"parameter_types",int64(2211)},{"result_types",int64(2211)},{"from_sql",int64(16)},{"generic_plans",int64(20)},{"custom_plans",int64(20)}}))
 })
}

func TestReferencePGPreparedStatementsDefinitions(t *testing.T) {
 sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  source:="/* prepared metadata */\nPREPARE pps_t(integer,text,boolean) AS SELECT $1 AS i,$2 AS s,$3 AS b"
  before:=ppsClock(t,p,c)
  sqlPreparedStep(t,p,b,c,fixture.Exec(source,0))
  after:=ppsClock(t,p,c); created:=ppsCreated(t,p,c,"pps_t")
  if created.Before(before)||created.After(after) {t.Fatalf("prepare clock %v outside [%v,%v]",created,before,after)}
  ppsSignature(t,p,b,c,"pps_t",source,"{integer,text,boolean}","{integer,text,boolean}",true)
  sqlPreparedStep(t,p,b,c,fixture.Select("SELECT parameter_types[1]::oid::bigint,parameter_types[2]::oid::bigint,parameter_types[3]::oid::bigint,result_types[1]::oid::bigint,result_types[2]::oid::bigint,result_types[3]::oid::bigint FROM pg_prepared_statements WHERE name='pps_t'",[]string{"parameter_types","parameter_types","parameter_types","result_types","result_types","result_types"},[]string{"INT8","INT8","INT8","INT8","INT8","INT8"},[][]any{{int64(23),int64(25),int64(16),int64(23),int64(25),int64(16)}}))
  for _,v:=range []int64{7,8} {
   q:="EXECUTE pps_t(7,'value',true)";if v==8 {q="EXECUTE pps_t(8,'value',true)"}
   sqlPreparedStep(t,p,b,c,fixture.Select(q,[]string{"i","s","b"},[]string{"INT4","TEXT","BOOL"},[][]any{{v,"value",true}}))
   if !ppsCreated(t,p,c,"pps_t").Equal(created) {t.Fatal("execution changed prepare time")}
   ppsSignature(t,p,b,c,"pps_t",source,"{integer,text,boolean}","{integer,text,boolean}",true)
  }
  sqlPreparedStep(t,p,b,c,fixture.Exec("CREATE TABLE pps_insert(v integer)",0))
  for _,tc:=range []struct{name,source,parameters string;results any}{
   {"pps_i","PREPARE pps_i AS SELECT $1::integer AS i,$2::text AS s","{integer,text}","{integer,text}"},
   {"pps_u","PREPARE pps_u(unknown) AS SELECT $1","{text}","{text}"},
   {"pps_unused","PREPARE pps_unused(integer,text) AS SELECT 1","{integer,text}","{integer}"},
   {"pps_typmod","PREPARE pps_typmod(varchar(7)) AS SELECT $1","{\"character varying\"}","{\"character varying\"}"},
   {"pps_none","PREPARE pps_none AS SELECT 1 WHERE false","{}","{integer}"},
   {"pps_zero","PREPARE pps_zero AS SELECT FROM pg_catalog.pg_prepared_statements","{}","{}"},
   {"pps_write","PREPARE pps_write(integer) AS INSERT INTO pps_insert VALUES($1)","{integer}",nil},
   {"PpsCase","PREPARE \"PpsCase\" AS SELECT 1","{}","{integer}"},
   {"ppscase","PREPARE PpsCase AS SELECT 2","{}","{integer}"},
  } {
   sqlPreparedStep(t,p,b,c,fixture.Exec(tc.source,0));ppsSignature(t,p,b,c,tc.name,tc.source,tc.parameters,tc.results,true)
  }
  sqlPreparedStep(t,p,b,c,sqlPreparedScalar("SELECT count(*) FROM pps_insert","count","INT8",int64(0)))
  sqlPreparedStep(t,p,b,c,fixture.Exec("CREATE DOMAIN pps_domain AS integer",0))
  sqlPreparedStep(t,p,b,c,fixture.Exec("PREPARE pps_domain_stmt(pps_domain) AS SELECT $1",0))
  var oid,parameter,result int64
  if err:=c.QueryRowContext(p.Ctx,"SELECT oid::bigint FROM pg_type WHERE typname='pps_domain'").Scan(&oid);err!=nil {t.Fatal(err)}
  if err:=c.QueryRowContext(p.Ctx,"SELECT parameter_types[1]::oid::bigint,result_types[1]::oid::bigint FROM pg_prepared_statements WHERE name='pps_domain_stmt'").Scan(&parameter,&result);err!=nil {t.Fatal(err)}
  if oid<=0||parameter!=oid||result!=oid {t.Fatalf("domain identities %d %d %d",oid,parameter,result)}
  sqlPreparedStep(t,p,b,c,fixture.Exec("BEGIN",0))
  var transaction time.Time
  if err:=c.QueryRowContext(p.Ctx,"SELECT transaction_timestamp()").Scan(&transaction);err!=nil {t.Fatal(err)}
  before=ppsClock(t,p,c)
  sqlPreparedStep(t,p,b,c,fixture.Exec("PREPARE pps_transaction AS SELECT 3",0))
  after=ppsClock(t,p,c);created=ppsCreated(t,p,c,"pps_transaction")
  if created.Before(before)||created.After(after)||created.Before(transaction) {t.Fatal("transaction clock used for prepare",transaction,before,created,after)}
  sqlPreparedStep(t,p,b,c,fixture.Exec("ROLLBACK",0))
  if !ppsCreated(t,p,c,"pps_transaction").Equal(created) {t.Fatal("rollback changed definition")}
 })
}

func TestReferencePGPreparedStatementsCounters(t *testing.T) {
 sqlPreparedScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  sqlPreparedStep(t,p,b,c,fixture.Exec("PREPARE pps_count AS SELECT 7",0))
  for i:=range 3 {
   var generic,custom any
   if b.PG {generic=int64(i);custom=int64(0)}
   sqlPreparedStep(t,p,b,c,fixture.Select("SELECT generic_plans,custom_plans FROM pg_prepared_statements WHERE name='pps_count'",[]string{"generic_plans","custom_plans"},[]string{"INT8","INT8"},[][]any{{generic,custom}}))
   if i<2 {sqlPreparedStep(t,p,b,c,sqlPreparedScalar("EXECUTE pps_count","?column?","INT4",int64(7)))}
  }
  sqlPreparedStep(t,p,b,c,fixture.Exec("PREPARE pps_custom(integer) AS SELECT $1",0))
  for _,mode:=range []string{"force_custom_plan","force_generic_plan"} {
   sqlPreparedStep(t,p,b,c,fixture.Exec("SET plan_cache_mode="+mode,0))
   sqlPreparedStep(t,p,b,c,sqlPreparedScalar("EXECUTE pps_custom(7)","?column?","INT4",int64(7)))
   var generic,custom any
   if b.PG {custom=int64(1);generic=int64(0);if mode=="force_generic_plan" {generic=int64(1)}}
   sqlPreparedStep(t,p,b,c,fixture.Select("SELECT generic_plans,custom_plans FROM pg_prepared_statements WHERE name='pps_custom'",[]string{"generic_plans","custom_plans"},[]string{"INT8","INT8"},[][]any{{generic,custom}}))
  }
  sqlPreparedStep(t,p,b,c,fixture.ErrorDiagnostic("EXECUTE pps_missing",sqlPreparedMissing("pps_missing")))
  var generic,custom any
  if b.PG {generic=int64(1);custom=int64(1)}
  sqlPreparedStep(t,p,b,c,fixture.Select("SELECT generic_plans,custom_plans FROM pg_prepared_statements WHERE name='pps_custom'",[]string{"generic_plans","custom_plans"},[]string{"INT8","INT8"},[][]any{{generic,custom}}))
  sqlPreparedStep(t,p,b,c,fixture.Exec("RESET plan_cache_mode",0))
 })
}

