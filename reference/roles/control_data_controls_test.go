package reference_test

import (
 "bytes"
 "context"
 "database/sql"
 "errors"
 "fmt"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func controlSnapshot(s *fixture.RoleMembershipScenario) []byte {
 s.T.Helper();var dump bytes.Buffer
 if err:=stuntgres.Dump(s.P.Ctx,s.P.Native,&dump);err!=nil {s.T.Fatal(err)}
 return append([]byte(nil),dump.Bytes()...)
}
func controlViewSteps() []fixture.Step {
 steps:=[]fixture.Step{fixture.Select("SELECT id FROM control_sentinel",[]string{"id"},[]string{"INT4"},[][]any{{int64(7)}})}
 for i:=range controlRoleNames {
  steps=append(steps,
   fixture.Select(fmt.Sprintf("SELECT * FROM control_expanded_%d LIMIT 0",i),controlRoleColumns[i],controlRoleTypes[i],nil),
   fixture.Select(fmt.Sprintf("SELECT * FROM control_field_%d LIMIT 0",i),[]string{"value"},controlRoleTypes[i][:1],nil))
 }
 steps=append(steps,fixture.Select("SELECT has_function_privilege('control_actor','pg_control_checkpoint()','EXECUTE') AS a,has_function_privilege('control_actor','pg_control_system()','EXECUTE') AS b,has_function_privilege('control_actor','pg_control_init()','EXECUTE') AS c,has_function_privilege('control_actor','pg_control_recovery()','EXECUTE') AS d",[]string{"a","b","c","d"},[]string{"BOOL","BOOL","BOOL","BOOL"},[][]any{{false,true,true,true}}))
 return steps
}
func TestReferenceControlDataControls(t *testing.T) {
 var emitted []byte
 controlRoleCases(t,func(s *fixture.RoleMembershipScenario,_ *sql.DB) {
  if !s.Backend.PG {
   baseline:=controlSnapshot(s)
   for line:=range strings.SplitSeq(string(baseline),"\n") {
    for _,name:=range controlRoleNames {if strings.Contains(line,name) {s.T.Fatal("stateless default serialized",line)}}
   }
  }
  s.Exec("CREATE TABLE control_sentinel(id integer)")
  s.Run(fixture.Exec("INSERT INTO control_sentinel VALUES(7)",1))
  for i,name:=range controlRoleNames {
   s.Exec(fmt.Sprintf("CREATE VIEW control_expanded_%d AS SELECT * FROM pg_catalog.%s()",i,name))
   s.Exec(fmt.Sprintf("CREATE VIEW control_field_%d AS SELECT (pg_catalog.%s()).%s AS value",i,name,controlRoleColumns[i][0]))
  }
  s.Exec("REVOKE EXECUTE ON FUNCTION pg_control_checkpoint() FROM PUBLIC")
  s.Exec("REVOKE EXECUTE ON FUNCTION pg_control_system() FROM PUBLIC")
  s.Exec("GRANT EXECUTE ON FUNCTION pg_control_system() TO control_actor")
  witness:=func(){for _,step:=range controlViewSteps(){s.Run(step);s.Run(fixture.Prepared(step))}}
  witness()
  if s.Backend.PG {
   if len(emitted)==0 {s.T.Fatal("missing native emitted dump")}
   s.Exec("DROP OWNED BY control_actor CASCADE")
   for i:=range controlRoleNames {
    for _,kind:=range []string{"expanded","field"} {s.Exec(fmt.Sprintf("DROP VIEW control_%s_%d",kind,i))}
   }
   s.Exec("DROP TABLE control_sentinel")
   // Replay the native dump itself, including genuine view definitions and ACL repairs.
   if _,err:=s.C.ExecContext(s.P.Ctx,string(emitted));err!=nil {s.T.Fatal("actual native dump replay",err)}
   witness()
   for i:=range controlRoleNames {
    h:=predefinedHeld(s,fmt.Sprintf("SELECT * FROM control_expanded_%d",i));controlRoleAdmitted(s,h,i);h.Statement.Close()
   }
   return
  }
  retained,err:=s.C.QueryContext(s.P.Ctx,"SELECT * FROM control_expanded_0 LIMIT 0");if err!=nil {s.T.Fatal(err)};defer retained.Close()
  before,err:=retained.Columns();if err!=nil {s.T.Fatal(err)}
  emitted=controlSnapshot(s)
  for _,name:=range []string{"pg_control_init","pg_control_recovery"} {
   for line:=range strings.SplitSeq(string(emitted),"\n") {if strings.HasPrefix(line,"UPDATE pg_catalog.pg_proc SET proacl=") && strings.Contains(line,name) {s.T.Fatal("default builtin serialized",line)}}
  }
  if err:=stuntgres.Reset(s.P.Ctx,s.P.Native);err!=nil {s.T.Fatal(err)}
  if err:=stuntgres.Restore(s.P.Ctx,s.P.Native,bytes.NewReader(emitted));err!=nil {s.T.Fatal(err)}
  witness()
  after,err:=retained.Columns();if err!=nil || strings.Join(before,",")!=strings.Join(after,",") || len(after)!=18 {s.T.Fatal("retained descriptor",after,err)}
  columns,err:=retained.ColumnTypes()
  if err!=nil || len(columns)!=18 {s.T.Fatal("retained column types",err)}
  for i,column:=range columns {
   if column.Name()!=controlRoleColumns[0][i] || column.DatabaseTypeName()!=controlRoleTypes[0][i] {s.T.Fatal("retained typed descriptor",column)}
  }
  for i:=range controlRoleNames {
   for _,q:=range []string{fmt.Sprintf("SELECT * FROM control_expanded_%d",i),fmt.Sprintf("SELECT * FROM control_field_%d",i)} {
    h:=predefinedHeld(s,q);controlRoleAdmitted(s,h,i);h.Statement.Close()
   }
  }
  driver:=stuntgres.NewDriver();connector,err:=driver.OpenConnector("control-destination");if err!=nil {s.T.Fatal(err)}
  fresh:=sql.OpenDB(connector);defer fresh.Close()
  exec:=func(q string){s.T.Helper();if _,err:=fresh.ExecContext(s.P.Ctx,q);err!=nil {s.T.Fatal(err)}}
  exec("CREATE TABLE destination_sentinel(id integer); INSERT INTO destination_sentinel VALUES(91); CREATE ROLE destination_reader; GRANT EXECUTE ON FUNCTION pg_control_checkpoint() TO destination_reader")
  if err:=stuntgres.Restore(s.P.Ctx,fresh,bytes.NewReader(emitted));err==nil {s.T.Fatal("missing ACL principal accepted")}
  var id int
  if err:=fresh.QueryRowContext(s.P.Ctx,"SELECT id FROM destination_sentinel").Scan(&id);err!=nil || id!=91 {s.T.Fatal("failed restore replaced catalog",id,err)}
  var allowed bool
  if err:=fresh.QueryRowContext(s.P.Ctx,"SELECT has_function_privilege('destination_reader','pg_control_checkpoint()','EXECUTE')").Scan(&allowed);err!=nil || !allowed {s.T.Fatal("failed restore replaced ACL",allowed,err)}
  canceled,cancel:=context.WithCancel(s.P.Ctx);cancel()
  if err:=stuntgres.Restore(canceled,fresh,bytes.NewReader(emitted));!errors.Is(err,context.Canceled) {s.T.Fatal("canceled restore",err)}
  if err:=fresh.QueryRowContext(s.P.Ctx,"SELECT id FROM destination_sentinel").Scan(&id);err!=nil || id!=91 {s.T.Fatal("canceled restore changed catalog",id,err)}
  exec("CREATE ROLE control_actor LOGIN")
  if err:=stuntgres.Restore(s.P.Ctx,fresh,bytes.NewReader(emitted));err!=nil {s.T.Fatal(err)}
  wrapped,record:=fixture.RecordedNativePool(s.T,connector)
  c,err:=wrapped.Conn(s.P.Ctx);if err!=nil {s.T.Fatal(err)};defer c.Close()
  for _,step:=range controlViewSteps(){fixture.RunStep(s.T,s.P.Ctx,c,record,step)}
  for i,name:=range controlRoleNames {
   q:=fmt.Sprintf("SELECT * FROM control_expanded_%d",i)
   fixture.RunStep(s.T,s.P.Ctx,c,record,controlRoleFailure(q,"0A000",name+" physical control-file access is unsupported in embedded mode"))
  }
  if _,err:=c.ExecContext(s.P.Ctx,"SET ROLE control_actor");err!=nil {s.T.Fatal(err)}
  fixture.RunStep(s.T,s.P.Ctx,c,record,controlRoleFailure("SELECT pg_control_checkpoint()","42501","permission denied for function pg_control_checkpoint"))
  fixture.RunStep(s.T,s.P.Ctx,c,record,controlRoleFailure("SELECT pg_control_system()","0A000","pg_control_system physical control-file access is unsupported in embedded mode"))
 })
}
