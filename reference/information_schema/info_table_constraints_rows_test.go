package reference_test

import (
 "database/sql"
 "testing"

 "stuntgres/reference/fixture"
)

func TestReferenceInfoTableConstraintsRows(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  tcSeed(run)
  defer func() {
   run(fixture.Exec("DROP SERVER IF EXISTS tc_server CASCADE",0))
   run(fixture.Exec("DROP FOREIGN DATA WRAPPER IF EXISTS tc_fdw",0))
  }()
  for _,q:=range []string{
   "CREATE TABLE tc_scope.empty(v integer)",
   "CREATE UNIQUE INDEX tc_index_only ON tc_scope.empty(v)",
   "CREATE DOMAIN tc_scope.d AS integer CONSTRAINT domain_only CHECK(VALUE>0)",
   "CREATE TABLE tc_scope.domain_table(v tc_scope.d)",
   "CREATE TABLE tc_scope.excluded(v integer,CONSTRAINT ex EXCLUDE USING btree(v WITH =))",
   "CREATE VIEW tc_scope.view_only AS SELECT v FROM tc_scope.empty",
   "CREATE MATERIALIZED VIEW tc_scope.mat_only AS SELECT v FROM tc_scope.empty WITH NO DATA",
   "CREATE FOREIGN DATA WRAPPER tc_fdw NO HANDLER NO VALIDATOR",
   "CREATE SERVER tc_server FOREIGN DATA WRAPPER tc_fdw",
   "CREATE FOREIGN TABLE tc_scope.foreign_only(v integer CONSTRAINT foreign_check CHECK(v>0)) SERVER tc_server",
  } { run(fixture.Exec(q,0)) }
  where:="table_schema='tc_scope'"
  held:=dcHold(t,p,c,tcProjection(b.PG,where,nil).Query)
  want:=tcSeedRows(database)
  check:=func() { tcRoutes(t,p,c,r,held,tcProjection(b.PG,where,want)) }
  check()
  for _,q:=range []string{
   "ALTER TABLE tc_scope.a ADD CONSTRAINT pending_check CHECK(payload<100) NOT VALID",
   "ALTER TABLE tc_scope.a ADD CONSTRAINT pending_fk FOREIGN KEY(payload) REFERENCES tc_scope.parent(id) NOT VALID",
  } { run(fixture.Exec(q,0)) }
  want=append(want,tcRow(database,"tc_scope","a","pending_check","CHECK",nil),tcRow(database,"tc_scope","a","pending_fk","FOREIGN KEY",nil))
  check()
  constraintOID:=func(name string) int64 {
   t.Helper()
   var oid int64
   if err:=c.QueryRowContext(p.Ctx,"SELECT oid FROM pg_catalog.pg_constraint WHERE conrelid='tc_scope.a'::regclass AND conname=$1",name).Scan(&oid);err!=nil||oid<=0 { t.Fatalf("constraint %s identity=%d error=%v",name,oid,err) }
   return oid
  }
  originalOIDs:=map[string]int64{"pending_check":constraintOID("pending_check"),"pending_fk":constraintOID("pending_fk")}
  for _,valid:=range []bool{false,true} {
   if valid {
    // Replace the actual definitions with validated ADDs. This is not an
    // in-place VALIDATE action, and the replacement identities must differ.
    for _,q:=range []string{
     "ALTER TABLE tc_scope.a DROP CONSTRAINT pending_check",
     "ALTER TABLE tc_scope.a DROP CONSTRAINT pending_fk",
     "ALTER TABLE tc_scope.a ADD CONSTRAINT pending_check CHECK(payload<100)",
     "ALTER TABLE tc_scope.a ADD CONSTRAINT pending_fk FOREIGN KEY(payload) REFERENCES tc_scope.parent(id)",
    } { run(fixture.Exec(q,0)) }
    for name,oldOID:=range originalOIDs {
     if got:=constraintOID(name);got==oldOID { t.Fatalf("replacement %s retained old identity %d",name,got) }
    }
   }
   s:=fixture.Select("SELECT conname,convalidated FROM pg_catalog.pg_constraint WHERE conrelid='tc_scope.a'::regclass AND conname IN ('pending_check','pending_fk')",[]string{"conname","convalidated"},[]string{"NAME","BOOL"},[][]any{{"pending_check",valid},{"pending_fk",valid}});s.Bag=true
   run(s);run(fixture.Prepared(s));check()
  }
  run(fixture.Exec("ALTER TABLE tc_scope.a DROP CONSTRAINT pending_check",0))
  run(fixture.Exec("ALTER TABLE tc_scope.a DROP CONSTRAINT pending_fk",0))
  want=tcSeedRows(database);check()
 })
}

func TestReferenceInfoTableConstraintsRelationKinds(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  run:=func(s fixture.Step) { fixture.RunStep(t,p.Ctx,c,r,s) }
  for _,q:=range []string{
   "CREATE SCHEMA tc_scope",
   "CREATE TABLE tc_scope.partitioned(v integer,CONSTRAINT inherited CHECK(v>0)) PARTITION BY RANGE(v)",
   "CREATE TABLE tc_scope.leaf PARTITION OF tc_scope.partitioned FOR VALUES FROM(0) TO(10)",
   "CREATE TABLE tc_scope.ancestor(v integer,CONSTRAINT inherited CHECK(v>0))",
   "CREATE TABLE tc_scope.child() INHERITS(tc_scope.ancestor)",
  } { run(fixture.Exec(q,0)) }
  want:=[][]any{}
  for _,name:=range []string{"partitioned","leaf","ancestor","child"} { want=append(want,tcRow(database,"tc_scope",name,"inherited","CHECK",nil)) }
  s:=tcProjection(b.PG,"table_schema='tc_scope'",want)
  tcRoutes(t,p,c,r,dcHold(t,p,c,s.Query),s)
 })
}

func TestReferenceInfoTableConstraintsTemporary(t *testing.T) {
 tcScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,r *fixture.Recorder,database string) {
  other,err:=b.DB.Conn(p.Ctx);if err!=nil { t.Fatal(err) };defer other.Close()
  fixture.RunStep(t,p.Ctx,c,r,fixture.Exec("CREATE TEMP TABLE tc_temp(v integer CONSTRAINT own_check CHECK(v>0))",0))
  fixture.RunStep(t,p.Ctx,other,b.Recorder,fixture.Exec("CREATE TEMP TABLE tc_other_temp(v integer CONSTRAINT other_check CHECK(v>0))",0))
  defer func() {
   fixture.RunStep(t,p.Ctx,c,r,fixture.Exec("DROP TABLE tc_temp",0))
   fixture.RunStep(t,p.Ctx,other,b.Recorder,fixture.Exec("DROP TABLE tc_other_temp",0))
  }()
  var own,foreign string
  if err:=c.QueryRowContext(p.Ctx,"SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&own);err!=nil { t.Fatal(err) }
  if err:=other.QueryRowContext(p.Ctx,"SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&foreign);err!=nil { t.Fatal(err) }
  if own==foreign||own==""||foreign=="" { t.Fatal("temporary identities",own,foreign) }
  for _,entry:=range []struct{c *sql.Conn;schema,table,name string}{{c,own,"tc_temp","own_check"},{other,foreign,"tc_other_temp","other_check"}} {
   s:=tcProjection(b.PG,"table_name IN ('tc_temp','tc_other_temp')",[][]any{tcRow(database,entry.schema,entry.table,entry.name,"CHECK",nil)})
   tcRoutes(t,p,entry.c,r,dcHold(t,p,entry.c,s.Query),s)
  }
 })
}
