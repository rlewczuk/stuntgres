package reference_test

import (
 "context"
 "database/sql"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

var controlRoleNames = []string{"pg_control_checkpoint", "pg_control_system", "pg_control_init", "pg_control_recovery"}
var controlRoleColumns = [][]string{{"checkpoint_lsn","redo_lsn","redo_wal_file","timeline_id","prev_timeline_id","full_page_writes","next_xid","next_oid","next_multixact_id","next_multi_offset","oldest_xid","oldest_xid_dbid","oldest_active_xid","oldest_multi_xid","oldest_multi_dbid","oldest_commit_ts_xid","newest_commit_ts_xid","checkpoint_time"}, {"pg_control_version","catalog_version_no","system_identifier","pg_control_last_modified"}, {"max_data_alignment","database_block_size","blocks_per_segment","wal_block_size","bytes_per_wal_segment","max_identifier_length","max_index_columns","max_toast_chunk_size","large_object_chunk_size","float8_pass_by_value","data_page_checksum_version","default_char_signedness"}, {"min_recovery_end_lsn","min_recovery_end_timeline","backup_start_lsn","backup_end_lsn","end_of_backup_record_required"}}
var controlRoleTypes = [][]string{{"PG_LSN","PG_LSN","TEXT","INT4","INT4","BOOL","TEXT","OID","XID","XID","XID","OID","XID","XID","OID","XID","XID","TIMESTAMPTZ"}, {"INT4","INT4","INT8","TIMESTAMPTZ"}, {"INT4","INT4","INT4","INT4","INT4","INT4","INT4","INT4","INT4","BOOL","INT4","BOOL"}, {"PG_LSN","INT4","PG_LSN","PG_LSN","BOOL"}}
var controlRoleOIDs = []int{3442,3441,3444,3443}

func controlRoleCases(t *testing.T,body func(*fixture.RoleMembershipScenario,*sql.DB)) {
 t.Helper()
 predefinedCases(t,[]string{"control_actor"},func(s *fixture.RoleMembershipScenario,db *sql.DB) {
  if s.Backend.PG {
   originals:=make([]sql.NullString,4)
   for i,oid:=range controlRoleOIDs {if err:=s.C.QueryRowContext(s.P.Ctx,"SELECT proacl::text FROM pg_proc WHERE oid=$1",oid).Scan(&originals[i]);err!=nil {s.T.Fatal(err)}}
   defer func(){
    ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
    if _,err:=s.C.ExecContext(ctx,"RESET ROLE; DROP OWNED BY control_actor CASCADE");err!=nil {s.T.Error(err)}
    for i,raw:=range originals {var value any;if raw.Valid {value=raw.String};if _,err:=s.C.ExecContext(ctx,"UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=$2",value,controlRoleOIDs[i]);err!=nil {s.T.Error(err)}}
   }()
  }
  body(s,db)
 })
}
func controlRoleFailure(q,code,message string) fixture.Step {return walRoleFailure(q,code,message)}
func controlRoleAdmitted(s *fixture.RoleMembershipScenario,executor fixture.HeldStatement,i int) {
 s.T.Helper()
 if !s.Backend.PG {
  failure:=controlRoleFailure(executor.SQL,"0A000",controlRoleNames[i]+" physical control-file access is unsupported in embedded mode")
  fixture.RunStep(s.T,s.P.Ctx,s.C,s.Record,failure)
  fixture.RunStep(s.T,s.P.Ctx,s.C,s.Record,fixture.Prepared(failure))
  walRoleHeld(s,executor,failure)
  return
 }
 check:=func(rows *sql.Rows,err error) {
  s.T.Helper();if err!=nil {s.T.Fatal(err)};defer rows.Close()
  if !rows.Next(){s.T.Fatal("missing physical row",rows.Err())}
  cols,err:=rows.Columns();if err!=nil {s.T.Fatal(err)}
  values:=make([]any,len(cols));dest:=make([]any,len(cols));for j:=range dest {dest[j]=&values[j]}
  if err:=rows.Scan(dest...);err!=nil {s.T.Fatal(err)}
  for _,v:=range values {if v==nil {s.T.Fatal("NULL physical value")}}
  if rows.Next() || rows.Err()!=nil {s.T.Fatal("physical cardinality",rows.Err())}
 }
 check(s.C.QueryContext(s.P.Ctx,executor.SQL))
 stmt,err:=s.C.PrepareContext(s.P.Ctx,executor.SQL);if err!=nil {s.T.Fatal(err)}
 check(stmt.QueryContext(s.P.Ctx));stmt.Close()
 check(executor.Statement.QueryContext(s.P.Ctx))
}
func TestReferenceControlDataAuthorization(t *testing.T) {
 controlRoleCases(t,func(s *fixture.RoleMembershipScenario,db *sql.DB) {
  admin,err:=db.Conn(s.P.Ctx);if err!=nil {s.T.Fatal(err)};defer admin.Close()
  mutate:=func(q string){s.T.Helper();if _,err:=admin.ExecContext(s.P.Ctx,q);err!=nil {s.T.Fatal(err)}}
  s.Exec("SET ROLE control_actor")
  for i,name:=range controlRoleNames {
   sig:="pg_catalog."+name+"()"
   var handles []fixture.HeldStatement
   for _,q:=range []string{"SELECT "+sig,"SELECT * FROM "+sig,"SELECT ("+sig+")."+controlRoleColumns[i][0]} {h:=predefinedHeld(s,q);defer h.Statement.Close();handles=append(handles,h);controlRoleAdmitted(s,h,i)}
   for shape,h:=range handles {
    names,types:=controlRoleColumns[i],controlRoleTypes[i]
    if shape==0 {names=[]string{name};types=[]string{"RECORD"}}
    if shape==2 {names=names[:1];types=types[:1]}
    zero:=fixture.Select(h.SQL+" LIMIT 0",names,types,nil)
    fixture.RunStep(s.T,s.P.Ctx,s.C,s.Record,zero)
    fixture.RunStep(s.T,s.P.Ctx,s.C,s.Record,fixture.Prepared(zero))
   }
   privilege:=func(want bool) {predefinedSelect(s,"SELECT has_function_privilege('control_actor','"+sig+"','EXECUTE') AS allowed",[]string{"allowed"},[]string{"BOOL"},[][]any{{want}})}
   privilege(true)
   mutate("REVOKE EXECUTE ON FUNCTION "+sig+" FROM PUBLIC");privilege(false)
   denied:=func(){
    for _,h:=range handles {
     failure:=controlRoleFailure(h.SQL,"42501","permission denied for function "+name)
     walRoleHeld(s,h,failure);s.Run(failure);s.Run(fixture.Prepared(failure))
     // Admission belongs to the ordinary executor even when LIMIT suppresses physical demand.
     zero:=controlRoleFailure(h.SQL+" LIMIT 0","42501","permission denied for function "+name)
     s.Run(zero);s.Run(fixture.Prepared(zero))
    }
   }
   denied()
   malformed:="SELECT "+name+"(NULL)"
   predefinedFailure(s,malformed,fixture.Diagnostic{Code:"42883",Message:"function "+name+"(unknown) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)},true,"prepare")
   mutate("GRANT EXECUTE ON FUNCTION "+sig+" TO control_actor");privilege(true)
   for _,h:=range handles {controlRoleAdmitted(s,h,i)}
   mutate("REVOKE EXECUTE ON FUNCTION "+sig+" FROM control_actor");privilege(false);denied()
   mutate("GRANT EXECUTE ON FUNCTION "+sig+" TO PUBLIC");privilege(true)
   for _,h:=range handles {controlRoleAdmitted(s,h,i)}
   s.Exec("RESET ROLE")
   s.Exec("BEGIN")
   s.Exec("REVOKE EXECUTE ON FUNCTION "+sig+" FROM PUBLIC")
   s.Exec("ROLLBACK")
   s.Exec("SET ROLE control_actor")
   privilege(true);controlRoleAdmitted(s,handles[0],i)
  }
  s.Exec("RESET ROLE")
 })
}
