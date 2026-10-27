package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceGeqoInventory(t *testing.T) {
 physicalCases(t,nil,func(s *physicalCase) {
  for _, prepared := range []bool{false,true} { s.geqoInventory(prepared) }
 })
}

func (s *physicalCase) geqoInventory(prepared bool) {
 rows := make([][]any,0,len(geqoSettings))
 for _, d := range geqoSettings {
  rows = append(rows,[]any{d.name,d.boot,nil,"Query Tuning / Genetic Query Optimizer",d.description,resourceNullable(d.extra),"user",d.kind,"default",resourceNullable(d.min),resourceNullable(d.max),nil,d.boot,d.boot,nil,nil,false})
 }
 step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name IN ("+geqoNames()+") ORDER BY name",physicalColumns,physicalTypes,rows)
 step.Prepared = prepared
 step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { statisticsSettingsCheckMetadata(t,rows,s.pg,physicalColumns,physicalTypes) }
 s.run(step)
 for _, d := range geqoSettings { s.resourceValue(strings.ToUpper(d.name),d.boot,d.boot,"default",prepared) }
 query := "SHOW ALL"
 var result *sql.Rows
 var err error
 if prepared {
  stmt,e := s.conn.PrepareContext(s.p.Ctx,query); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
  result,err = stmt.QueryContext(s.p.Ctx)
 } else { result,err = s.conn.QueryContext(s.p.Ctx,query) }
 if err != nil { s.t.Fatal(err) }; defer result.Close()
 // SHOW utility columns are nonnullable; pg_settings relation columns are nullable.
 replicationShowAllMetadata(s.t,result,s.pg)
 found := map[string]bool{}
 for result.Next() {
  var name,value,description string
  if err := result.Scan(&name,&value,&description); err != nil { s.t.Fatal(err) }
  for _, d := range geqoSettings { if name == d.name {
   if found[name] || value != d.boot || description != d.description { s.t.Fatal(name,value,description) }; found[name] = true
  } }
 }
 if err := result.Err(); err != nil || len(found) != 7 { s.t.Fatal(len(found),err) }
}

func TestReferenceGeqoAuthority(t *testing.T) {
 physicalCases(t,[]string{"geqo_authority_login"},func(s *physicalCase) {
  s.run(fixture.Exec("CREATE ROLE geqo_authority_login LOGIN PASSWORD 'role-core-fixture'",0))
  ordinary := s.login("geqo_authority_login")
  for _, prepared := range []bool{false,true} {
   ordinary.geqoInventory(prepared)
   for _, d := range geqoSettings {
    ordinary.plannerCostSet(d.name,d.a,prepared); ordinary.geqoValue(d,d.a,"session",prepared)
    step := fixture.Select("SELECT set_config($1,$2,false)",[]string{"set_config"},[]string{"TEXT"},[][]any{{d.b}},d.name,d.b); step.Prepared = prepared; ordinary.run(step)
    ordinary.geqoValue(d,d.b,"session",prepared)
    v := geqoInvalidValues(d)[0]
    ordinary.run(physicalError("SET "+d.name+"="+physicalLiteral(v.input),v.diagnostic,prepared,false))
    ordinary.run(physicalError("SELECT set_config($1,$2,false)",v.diagnostic,prepared,true,d.name,v.input))
    ordinary.geqoValue(d,d.b,"session",prepared)
    ordinary.run(fixture.Exec("RESET "+d.name,0))
   }
   denied := physicalDiagnostic("42501", `permission denied to set parameter "temp_file_limit"`)
   ordinary.run(physicalError("SET temp_file_limit='bogus'",denied,prepared,false))
   ordinary.run(physicalError("SELECT set_config($1,$2,false)",denied,prepared,true,"temp_file_limit","bogus"))
  }
 })
}
