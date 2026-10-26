package reference_test

import (
 "database/sql"
 "math"
 "reflect"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceVacuumSettingsInventory(t *testing.T) {
 physicalCases(t,[]string{"vacuum_inventory_login","vacuum_inventory_reader"},func(s *physicalCase) {
  s.run(fixture.Exec("CREATE ROLE vacuum_inventory_login LOGIN PASSWORD 'role-core-fixture'",0))
  s.run(fixture.Exec("CREATE ROLE vacuum_inventory_reader",0))
  ordinary:=s.login("vacuum_inventory_login")
  for _,observer:=range []*physicalCase{s,ordinary} { for _,prepared:=range []bool{false,true} { observer.vacuumInventory(prepared,observer==s) } }
  s.run(fixture.Exec("GRANT pg_read_all_settings TO vacuum_inventory_reader",0))
  s.run(fixture.Exec("GRANT vacuum_inventory_reader TO vacuum_inventory_login WITH INHERIT TRUE",0))
  for _,prepared:=range []bool{false,true} { ordinary.vacuumInventory(prepared,true) }
 })
}

func (s *physicalCase) vacuumInventory(prepared, provenance bool) {
 s.t.Helper()
 query:="SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE "+vacuumPredicate()+" ORDER BY name"
 var rows *sql.Rows
 var err error
 if prepared { stmt,e:=s.conn.PrepareContext(s.p.Ctx,query); if e!=nil { s.t.Fatal(e) }; defer stmt.Close(); rows,err=stmt.QueryContext(s.p.Ctx) } else { rows,err=s.conn.QueryContext(s.p.Ctx,query) }
 if err!=nil { s.t.Fatal(err) }; defer rows.Close()
 statisticsSettingsCheckMetadata(s.t,rows,s.pg,physicalColumns,physicalTypes)
 count:=0
 for rows.Next() {
  cells,dest:=make([]any,17),make([]any,17); for i:=range cells { dest[i]=&cells[i] }
  if err:=rows.Scan(dest...); err!=nil { s.t.Fatal(err) }; for i:=range cells { cells[i]=fixture.Normalize(cells[i]) }
  if count>=len(vacuumSettings) { s.t.Fatal("extra vacuum row",cells) }
  d:=vacuumSettings[count]
  var sourcefile,sourceline any
  if s.pg && provenance && d.name=="autovacuum_worker_slots" {
   sourcefile,sourceline="/var/lib/postgresql/18/docker/postgresql.conf",int64(687)
  }
  want:=[]any{d.name,d.raw,resourceNullable(d.unit),d.category,d.description,resourceNullable(d.extra),d.context,d.kind,s.vacuumInitialSource(d),resourceNullable(d.min),resourceNullable(d.max),nil,d.raw,d.raw,sourcefile,sourceline,false}
  if !reflect.DeepEqual(cells,want) { s.t.Fatalf("%s catalog got %#v want %#v",d.name,cells,want) }; count++
 }
 if err:=rows.Err(); err!=nil || count!=28 { s.t.Fatal("vacuum inventory cardinality",count,err) }; rows.Close()
 for _,d:=range vacuumSettings { for _,name:=range []string{d.name,strings.ToUpper(d.name)} { s.vacuumValue(name,d.show,d.raw,"default",prepared) } }
 if prepared { stmt,e:=s.conn.PrepareContext(s.p.Ctx,"SHOW ALL"); if e!=nil { s.t.Fatal(e) }; defer stmt.Close(); rows,err=stmt.QueryContext(s.p.Ctx) } else { rows,err=s.conn.QueryContext(s.p.Ctx,"SHOW ALL") }
 if err!=nil { s.t.Fatal(err) }; defer rows.Close()
 columns,err:=rows.Columns()
 if err!=nil || !reflect.DeepEqual(columns,[]string{"name","setting","description"}) { s.t.Fatal(columns,err) }
 types,err:=rows.ColumnTypes()
 if err!=nil || len(types)!=3 { s.t.Fatal(types,err) }
 for i,typ:=range types {
  nullable,known:=typ.Nullable()
  length,lengthKnown:=typ.Length()
  precision,scale,decimalKnown:=typ.DecimalSize()
  if typ.Name()!=columns[i] || typ.DatabaseTypeName()!="TEXT" || typ.ScanType()!=reflect.TypeFor[string]() || known==s.pg || nullable || length!=math.MaxInt64 || !lengthKnown || precision!=0 || scale!=0 || decimalKnown { s.t.Fatalf("SHOW ALL column %s metadata nullable=(%v,%v) length=(%d,%v)",typ.Name(),nullable,known,length,lengthKnown) }
 }
 found:=map[string]bool{}
 for rows.Next() {
  var name,value,description string; if err:=rows.Scan(&name,&value,&description); err!=nil { s.t.Fatal(err) }
  for _,d:=range vacuumSettings { if name==d.name { if found[name] || value!=d.show || description!=d.description { s.t.Fatal("SHOW ALL",name,value,description) }; found[name]=true } }
 }
 if err:=rows.Err(); err!=nil || len(found)!=28 { s.t.Fatal("SHOW ALL inventory",len(found),err) }
}
