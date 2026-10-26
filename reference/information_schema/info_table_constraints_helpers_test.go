package reference_test

import (
 "database/sql"
 "math"
 "reflect"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

var tcColumns = strings.Fields("constraint_catalog constraint_schema constraint_name table_catalog table_schema table_name constraint_type is_deferrable initially_deferred enforced nulls_distinct")
var tcTypes = strings.Fields("NAME NAME NAME NAME NAME NAME VARCHAR VARCHAR VARCHAR VARCHAR VARCHAR")
var tcRoles = []string{"tc_owner", "tc_reader", "tc_member", "tc_set", "tc_group", "tc_noinherit"}

func tcScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *fixture.Recorder, string)) {
 t.Helper()
 for _, schema := range []string{"tc_scope", "tc_moved", "tc_private"} { fixture.SchemaReferenceCleanup(t, schema) }
 fixture.RoleReferenceCleanup(t, tcRoles)
 p := fixture.NewScenario(t)
 for _, b := range p.Backends() {
  t.Run(b.Name, func(t *testing.T) {
   c, err := b.DB.Conn(p.Ctx)
   if err != nil { t.Fatal(err) }
   t.Cleanup(func() {
    defer c.Close()
    for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS tc_scope CASCADE", "DROP SCHEMA IF EXISTS tc_moved CASCADE", "DROP SCHEMA IF EXISTS tc_private CASCADE"} {
     if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Error(err) }
    }
   })
   var database string
   if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil || database == "" { t.Fatal(database, err) }
   body(t, p, b, c, b.Recorder, database)
  })
 }
}

func tcMetadata(t *testing.T, columns []*sql.ColumnType, pg bool) {
 t.Helper()
 if len(columns) != 11 { t.Fatalf("column count %d", len(columns)) }
 for i, c := range columns {
  length, lengthOK := c.Length()
  precision, scale, decimalOK := c.DecimalSize()
  nullable, nullableOK := c.Nullable()
  scan := reflect.TypeFor[string]()
  if pg && i < 6 { scan = reflect.TypeFor[any]() }
  wantLength := int64(0)
  if i == 6 { wantLength = math.MaxInt64; if pg { wantLength = -5 } } else if i > 6 { wantLength = 3 }
  if c.Name() != tcColumns[i] || c.DatabaseTypeName() != tcTypes[i] || c.ScanType() != scan || length != wantLength || lengthOK != (i >= 6) || precision != 0 || scale != 0 || decimalOK || nullable != !pg || nullableOK != !pg {
   t.Fatalf("column %d: %s %s scan=%v length=%d/%t decimal=%d/%d/%t nullable=%t/%t", i,c.Name(),c.DatabaseTypeName(),c.ScanType(),length,lengthOK,precision,scale,decimalOK,nullable,nullableOK)
  }
 }
}

func tcProjection(pg bool, where string, want [][]any) fixture.Step {
 s := fixture.Select("SELECT "+strings.Join(tcColumns, ",")+" FROM information_schema.table_constraints WHERE "+where, tcColumns, tcTypes, want)
 s.Bag = true
 s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { columns, err := rows.ColumnTypes(); if err != nil { t.Fatal(err) }; tcMetadata(t,columns,pg) }
 s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
  if value == nil {
   if column != 10 { t.Fatalf("unexpected NULL column %d", column) }
   for _, candidate := range want { if candidate[column] == nil { return true } }
   t.Fatal("unexpected NULL"); return true
  }
  text, ok := value.(string)
  if pg && column < 6 { bytes, yes := value.([]byte); text,ok=string(bytes),yes }
  if !ok { t.Fatalf("raw column %d=%T(%v)",column,value,value) }
  for _, candidate := range want { if candidate[column] == text { return true } }
  t.Fatalf("unexpected raw column %d=%q",column,text); return true
 }
 return s
}

func tcRow(database, schema, table, name, kind string, distinct any) []any {
 return []any{database,schema,name,database,schema,table,kind,"NO","NO","YES",distinct}
}

func tcSeed(run func(fixture.Step)) {
 for _, q := range []string{
  "CREATE SCHEMA tc_scope",
  "CREATE TABLE tc_scope.parent(id integer CONSTRAINT p_nn NOT NULL, CONSTRAINT p_pk PRIMARY KEY(id))",
  "CREATE TABLE tc_scope.a(id integer CONSTRAINT a_nn NOT NULL,v integer,payload integer, CONSTRAINT same CHECK(v>0),CONSTRAINT a_u UNIQUE(v),CONSTRAINT a_nd UNIQUE NULLS NOT DISTINCT(payload),CONSTRAINT a_fk FOREIGN KEY(v) REFERENCES tc_scope.parent(id))",
  "CREATE TABLE tc_scope.b(v integer,CONSTRAINT same CHECK(v<10))",
 } { run(fixture.Exec(q,0)) }
}

func tcSeedRows(database string) [][]any {
 return [][]any{
  tcRow(database,"tc_scope","parent","p_nn","CHECK",nil),
  tcRow(database,"tc_scope","parent","p_pk","PRIMARY KEY",nil),
  tcRow(database,"tc_scope","a","a_nn","CHECK",nil),
  tcRow(database,"tc_scope","a","same","CHECK",nil),
  tcRow(database,"tc_scope","a","a_u","UNIQUE","YES"),
  tcRow(database,"tc_scope","a","a_nd","UNIQUE","NO"),
  tcRow(database,"tc_scope","a","a_fk","FOREIGN KEY",nil),
  tcRow(database,"tc_scope","b","same","CHECK",nil),
 }
}

func tcRoutes(t *testing.T,p *fixture.Pair,c *sql.Conn,r *fixture.Recorder,held fixture.HeldStatement,s fixture.Step) {
 t.Helper()
 fixture.RunStep(t,p.Ctx,c,r,s)
 fixture.RunStep(t,p.Ctx,c,r,fixture.Prepared(s))
 fixture.RunStep(t,p.Ctx,held,r,s)
}

func tcMissing(run func(fixture.Step)) {
 d := fixture.ControlsReferenceDiagnostic("42703",`column "missing" does not exist`)
 d.Position="8"
 for _, suffix := range []string{""," WHERE FALSE"} {
  for _, prepared := range []bool{false,true} {
   s:=fixture.ErrorDiagnostic("SELECT missing FROM information_schema.table_constraints"+suffix,d)
   s.SelectRows,s.Prepared,s.ErrorStage=true,prepared,"execute"
   if prepared { s.ErrorStage="prepare" }
   run(s)
  }
 }
}

func tcDrain(t *testing.T, rows *sql.Rows, want [][]any) {
 t.Helper()
 defer rows.Close()
 used:=make([]bool,len(want)); count:=0
 for rows.Next() {
  cells:=make([]any,11); targets:=make([]any,11)
  for i:=range cells { targets[i]=&cells[i] }
  if err:=rows.Scan(targets...); err!=nil { t.Fatal(err) }
  for i,v:=range cells { if b,ok:=v.([]byte);ok { cells[i]=string(b) } }
  match:=false
  for i,v:=range want { if !used[i]&&reflect.DeepEqual(cells,v) { used[i]=true;match=true;break } }
  if !match { t.Fatalf("unexpected retained row %v",cells) };count++
 }
 if rows.Err()!=nil||count!=len(want) { t.Fatalf("retained rows=%d want=%d error=%v",count,len(want),rows.Err()) }
}
