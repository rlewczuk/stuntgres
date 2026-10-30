package reference_test

import (
 "context"
 "database/sql"
 "fmt"
 "strings"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)
type slotSignature struct { name string; oid int64; types,labels string; args []string; defaults int }
var slotSignatures=[]slotSignature{
 {"pg_create_physical_replication_slot",3779,"name,boolean,boolean","slot_name,immediately_reserve,temporary",[]string{"'rsl_absent'","false","false"},2},
 {"pg_create_logical_replication_slot",3786,"name,name,boolean,boolean,boolean","slot_name,plugin,temporary,twophase,failover",[]string{"'rsl_absent'","'test_decoding'","false","false","false"},3},
 {"pg_drop_replication_slot",3780,"name","",[]string{"'rsl_absent'"},0},
 {"pg_copy_physical_replication_slot",4221,"name,name","src_slot_name,dst_slot_name",[]string{"'rsl_absent'","'BAD'"},0},
 {"pg_copy_physical_replication_slot",4220,"name,name,boolean","src_slot_name,dst_slot_name,temporary",[]string{"'rsl_absent'","'BAD'","false"},0},
 {"pg_copy_logical_replication_slot",4224,"name,name","src_slot_name,dst_slot_name",[]string{"'rsl_absent'","'BAD'"},0},
 {"pg_copy_logical_replication_slot",4223,"name,name,boolean","src_slot_name,dst_slot_name,temporary",[]string{"'rsl_absent'","'BAD'","false"},0},
 {"pg_copy_logical_replication_slot",4222,"name,name,boolean,name","src_slot_name,dst_slot_name,temporary,plugin",[]string{"'rsl_absent'","'BAD'","false","'unlisted'"},0},
 {"pg_replication_slot_advance",3878,"name,pg_lsn","slot_name,upto_lsn",[]string{"'rsl_absent'","'0/1'"},0},
}
func slotCases(t *testing.T,body func(*fixture.RoleMembershipScenario,*sql.DB)) { t.Helper(); predefinedCases(t,[]string{"rsl_actor","rsl_repl","rsl_member"},body) }
func slotCall(sig slotSignature,args []string) string { return sig.name+"("+strings.Join(args,",")+")" }
func slotFields(sig slotSignature) ([]string,[]string) { second:="lsn"; if sig.oid==3878 { second="end_lsn" }; return []string{"slot_name",second},[]string{"NAME","PG_LSN"} }
func slotFailure(s *fixture.RoleMembershipScenario,q string,d fixture.Diagnostic) { s.T.Helper(); predefinedFailure(s,q,d,true,"execute") }
func slotBoundary(logical bool) fixture.Diagnostic { message:="pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode"; if logical { message="pg_create_logical_replication_slot logical decoding and WAL providers are unsupported in embedded mode" }; return fixture.Diagnostic{Code:"0A000",Message:message} }
// Query the VOID result rather than comparing backend-specific SELECT command
// tags exposed by database/sql Exec's RowsAffected.
func slotDrop(s *fixture.RoleMembershipScenario,name string) { s.T.Helper(); predefinedSelect(s,"SELECT pg_drop_replication_slot("+fileListLiteral(name)+")",[]string{"pg_drop_replication_slot"},[]string{"VOID"},[][]any{{""}}) }
// Temporary slots are cleaned by their pinned physical owner's fixture teardown;
// only inactive permanent slots may be acquired from this independent connection.
func slotCleanup(t *testing.T,db *sql.DB,names ...string) { t.Helper(); t.Cleanup(func(){ ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second); defer cancel(); for _,name:=range names { var exists bool; if err:=db.QueryRowContext(ctx,"SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1 AND NOT temporary)",name).Scan(&exists); err!=nil { t.Error(err); continue }; if exists { if _,err:=db.ExecContext(ctx,"SELECT pg_drop_replication_slot($1)",name); err!=nil { t.Error(err) } } } }) }
func slotCreate(s *fixture.RoleMembershipScenario,name string,logical,reserve,temporary bool) string {
 s.T.Helper(); fn:="pg_create_physical_replication_slot"; args:=fmt.Sprintf("'%s',%t,%t",name,reserve,temporary)
 if logical { fn="pg_create_logical_replication_slot"; args=fmt.Sprintf("'%s','test_decoding',%t",name,temporary) }
 q:="SELECT * FROM "+fn+"("+args+")"
 if !s.Backend.PG { slotFailure(s,q,slotBoundary(logical)); return "" }
 var got string; var lsn sql.NullString
 if err:=s.C.QueryRowContext(s.P.Ctx,q).Scan(&got,&lsn); err!=nil { s.T.Fatal(q,err) }
 if got!=name || (logical||reserve)!=lsn.Valid { s.T.Fatalf("create %s = %s/%v",name,got,lsn) }
 return lsn.String
}
