package reference_test

import (
 "database/sql"
 "fmt"
 "reflect"
 "regexp"
 "strconv"
 "strings"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

var controlNames = []string{"pg_control_checkpoint", "pg_control_system", "pg_control_init", "pg_control_recovery"}
var controlColumns = [][]string{{"checkpoint_lsn","redo_lsn","redo_wal_file","timeline_id","prev_timeline_id","full_page_writes","next_xid","next_oid","next_multixact_id","next_multi_offset","oldest_xid","oldest_xid_dbid","oldest_active_xid","oldest_multi_xid","oldest_multi_dbid","oldest_commit_ts_xid","newest_commit_ts_xid","checkpoint_time"}, {"pg_control_version","catalog_version_no","system_identifier","pg_control_last_modified"}, {"max_data_alignment","database_block_size","blocks_per_segment","wal_block_size","bytes_per_wal_segment","max_identifier_length","max_index_columns","max_toast_chunk_size","large_object_chunk_size","float8_pass_by_value","data_page_checksum_version","default_char_signedness"}, {"min_recovery_end_lsn","min_recovery_end_timeline","backup_start_lsn","backup_end_lsn","end_of_backup_record_required"}}
var controlTypes = [][]string{{"PG_LSN","PG_LSN","TEXT","INT4","INT4","BOOL","TEXT","OID","XID","XID","XID","OID","XID","XID","OID","XID","XID","TIMESTAMPTZ"}, {"INT4","INT4","INT8","TIMESTAMPTZ"}, {"INT4","INT4","INT4","INT4","INT4","INT4","INT4","INT4","INT4","BOOL","INT4","BOOL"}, {"PG_LSN","INT4","PG_LSN","PG_LSN","BOOL"}}

func controlMetadata(pg bool,names,types []string) func(*testing.T,*sql.Rows) {
 return func(t *testing.T, rows *sql.Rows) {
  cols,err:=rows.ColumnTypes();if err!=nil || len(cols)!=len(names) {t.Fatal(cols,err)}
  for i,c:=range cols {
   // Keep SQL shape expectations independent of adapter carriers. Native
   // value.go scanType and pq v1.12.3 fieldDesc.Type own these mappings.
   var scan reflect.Type
   switch types[i] {
   case "TEXT":scan=reflect.TypeFor[string]()
   case "PG_LSN","RECORD":scan=reflect.TypeFor[string]();if pg {scan=reflect.TypeFor[any]()}
   case "OID":scan=reflect.TypeFor[int64]();if pg {scan=reflect.TypeFor[any]()}
   case "XID":scan=reflect.TypeFor[[]byte]();if pg {scan=reflect.TypeFor[any]()}
   case "INT4":scan=reflect.TypeFor[int64]();if pg {scan=reflect.TypeFor[int32]()}
   case "INT8":scan=reflect.TypeFor[int64]()
   case "BOOL":scan=reflect.TypeFor[bool]()
   case "TIMESTAMPTZ":scan=reflect.TypeFor[time.Time]()
   default:t.Fatalf("unspecified control metadata type %q",types[i])
   }
   if c.Name()!=names[i] || c.DatabaseTypeName()!=types[i] || c.ScanType()!=scan {t.Fatalf("column %d: %s/%s/%v want %s/%s/%v",i,c.Name(),c.DatabaseTypeName(),c.ScanType(),names[i],types[i],scan)}
   if nullable,known:=c.Nullable();nullable || known {t.Fatal("computed nullability",nullable,known)}
   // Unconstrained TEXT reports an unbounded length; non-character control
   // types have no length metadata, even when their ScanType is string.
   var wantLength int64
   wantLengthOK:=types[i]=="TEXT"
   if wantLengthOK {wantLength=1<<63-1}
   if length,ok:=c.Length();length!=wantLength || ok!=wantLengthOK {t.Fatalf("column %d (%s/%s): length=%d/%v want %d/%v",i,c.Name(),types[i],length,ok,wantLength,wantLengthOK)}
   if precision,scale,ok:=c.DecimalSize();precision!=0 || scale!=0 || ok {t.Fatalf("column %d (%s/%s): decimal=%d,%d/%v want 0,0/false",i,c.Name(),types[i],precision,scale,ok)}
  }
 }
}
func controlMetadataCases(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 for i,name:=range controlNames {
  for _,mode:=range []string{"plain","qualified","uppercase","alias","ordinality","scalar","expanded","field"} {
   q:="SELECT * FROM "+name+"() LIMIT 0"; names,types:=controlColumns[i],controlTypes[i]
   switch mode {
   case "qualified":q="SELECT * FROM pg_catalog."+name+"() LIMIT 0"
   case "uppercase":q="SELECT * FROM PG_CATALOG."+strings.ToUpper(name)+"() LIMIT 0"
   case "alias":q="SELECT * FROM "+name+"() AS c(first) LIMIT 0";names=append([]string(nil),names...);names[0]="first"
   case "ordinality":q="SELECT * FROM "+name+"() WITH ORDINALITY LIMIT 0";names=append(append([]string(nil),names...),"ordinality");types=append(append([]string(nil),types...),"INT8")
   case "scalar":q="SELECT "+name+"() LIMIT 0";names=[]string{name};types=[]string{"RECORD"}
   case "expanded":q="SELECT ("+name+"()).* LIMIT 0"
   case "field":q="SELECT ("+name+"())."+names[0]+" AS value LIMIT 0";names=[]string{"value"};types=types[:1]
   }
   s:=fixture.Select(q,names,types,nil);s.CheckMetadata=controlMetadata(b.PG,names,types)
   fixture.RunStep(t,p.Ctx,c,b.Recorder,s);fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s));recoveryHeld(t,p,b,c,s)
  }
 }
}
func TestReferenceControlDataMetadata(t *testing.T) {recoveryScenario(t,controlMetadataCases)}

func controlPhysical(t *testing.T,rows *sql.Rows,types []string) {
 t.Helper();defer rows.Close()
 if !rows.Next(){t.Fatal("missing physical row",rows.Err())}
 values:=make([]any,len(types));dest:=make([]any,len(types));for i:=range dest {dest[i]=&values[i]}
 if err:=rows.Scan(dest...);err!=nil {t.Fatal(err)}
 for i,v:=range values {
  if v==nil {t.Fatal("NULL physical member",i)}
  v=fixture.Normalize(v)
  switch types[i] {
  case "RECORD":s,ok:=v.(string);if !ok || !strings.HasPrefix(s,"(") || !strings.HasSuffix(s,")") || len(s)<=2 {t.Fatal(v)}
  case "PG_LSN":if !regexp.MustCompile(`^[0-9A-F]+/[0-9A-F]+$`).MatchString(fmt.Sprint(v)) {t.Fatal(v)}
  case "TEXT":if _,ok:=v.(string);!ok {t.Fatal(v)}
  case "BOOL":if _,ok:=v.(bool);!ok {t.Fatal(v)}
  case "TIMESTAMPTZ":if stamp,ok:=v.(time.Time);!ok || stamp.IsZero() {t.Fatal(v)}
  default:if _,err:=strconv.ParseInt(fmt.Sprint(v),10,64);err!=nil {t.Fatal(v,err)}
  }
 }
 if rows.Next() || rows.Err()!=nil {t.Fatal("extra row or streaming failure",rows.Err())}
}
func controlDemand(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,i int,q string,types []string) {
 for _,mode:=range []string{"direct","prepared","held"} {
  if !b.PG {
   s:=walFailure(q,"0A000",controlNames[i]+" physical control-file access is unsupported in embedded mode")
   switch mode {case "direct":fixture.RunStep(t,p.Ctx,c,b.Recorder,s);case "prepared":fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Prepared(s));case "held":recoveryHeld(t,p,b,c,s)}
   continue
  }
  var rows *sql.Rows;var err error
  if mode=="direct" {rows,err=c.QueryContext(p.Ctx,q)} else {
   stmt,e:=c.PrepareContext(p.Ctx,q);if e!=nil {t.Fatal(e)}
   for attempt:=0;attempt<2;attempt++ {rows,err=stmt.QueryContext(p.Ctx);if err!=nil {t.Fatal(err)};controlPhysical(t,rows,types);if mode=="prepared" {break}}
   stmt.Close();continue
  }
  if err!=nil {t.Fatal(err)};controlPhysical(t,rows,types)
 }
}
func controlDemands(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
 for i,name:=range controlNames {
  for _,qualified:=range []bool{false,true} {
   call:=name+"()";if qualified {call="pg_catalog."+call}
   controlDemand(t,p,b,c,i,"SELECT "+call,[]string{"RECORD"})
   controlDemand(t,p,b,c,i,"SELECT * FROM "+call,controlTypes[i])
   controlDemand(t,p,b,c,i,"SELECT ("+call+").*",controlTypes[i])
   controlDemand(t,p,b,c,i,"SELECT ("+call+")."+controlColumns[i][0],controlTypes[i][:1])
  }
  if b.PG {
   var valid bool
   q:="SELECT system_identifier <> 0 AS valid FROM pg_control_system()"
   if i==2 {q="SELECT max_data_alignment > 0 AND database_block_size > 0 AND blocks_per_segment > 0 AND wal_block_size > 0 AND bytes_per_wal_segment > 0 AND max_identifier_length > 0 AND max_index_columns > 0 AND max_toast_chunk_size > 0 AND large_object_chunk_size > 0 AS valid FROM pg_control_init()"}
   if i==1 || i==2 {if err:=c.QueryRowContext(p.Ctx,q).Scan(&valid);err!=nil || !valid {t.Fatal(valid,err)}}
  }
 }
}
func TestReferenceControlDataDemand(t *testing.T) {recoveryScenario(t,controlDemands)}
func TestReferenceControlDataFeatureSmoke(t *testing.T) {
 recoveryScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
  controlMetadataCases(t,p,b,c);controlDemands(t,p,b,c)
  for _,name:=range controlNames {
   s:=fixture.ImplicitError("SELECT "+name+"(NULL)",fixture.Diagnostic{Code:"42883",Message:"function "+name+"(unknown) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)});s.SelectRows=true
   fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
  }
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE VIEW control_smoke AS SELECT * FROM pg_catalog.pg_control_checkpoint()",0))
  fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT * FROM control_smoke LIMIT 0",controlColumns[0],controlTypes[0],nil))
 })
}
