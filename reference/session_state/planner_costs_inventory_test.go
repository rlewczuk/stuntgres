package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePlannerCostsInventory(t *testing.T) {
	physicalCases(t, []string{"planner_cost_inventory_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE planner_cost_inventory_login LOGIN PASSWORD 'role-core-fixture'", 0))
		ordinary := s.login("planner_cost_inventory_login")
		for _, observer := range []*physicalCase{s, ordinary} {
			for _, prepared := range []bool{false, true} { observer.plannerCostInventory(prepared) }
		}
	})
}

func (s *physicalCase) plannerCostInventory(prepared bool) {
	s.t.Helper()
	query := "SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name IN ("+plannerCostNames()+") ORDER BY name"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query); if e != nil { s.t.Fatal(e) }; defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else { rows, err = s.conn.QueryContext(s.p.Ctx, query) }
	if err != nil { s.t.Fatal(err) }
	statisticsSettingsCheckMetadata(s.t, rows, s.pg, physicalColumns, physicalTypes)
	count := 0
	for rows.Next() {
		cells, dest := make([]any, 17), make([]any, 17)
		for i := range cells { dest[i] = &cells[i] }
		if err := rows.Scan(dest...); err != nil { s.t.Fatal(err) }
		for i := range cells { cells[i] = fixture.Normalize(cells[i]) }
		if count >= 13 { s.t.Fatal("extra planner cost row", cells) }
		d := plannerCostSettings[count]
		want := []any{d.name,d.boot,resourceNullable(d.unit),"Query Tuning / Planner Cost Constants",d.description,resourceNullable(d.extra),"user",d.kind,"default",d.min,d.max,nil,d.boot,d.boot,nil,nil,false}
		if !reflect.DeepEqual(cells,want) { s.t.Fatalf("%s got %#v want %#v",d.name,cells,want) }
		count++
	}
	if err := rows.Err(); err != nil || count != 13 { s.t.Fatal(count,err) }; rows.Close()
	for _, d := range plannerCostSettings { s.resourceValue(strings.ToUpper(d.name),d.show,d.boot,"default",prepared) }
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx,"SHOW ALL"); if e != nil { s.t.Fatal(e) }; defer stmt.Close(); rows,err=stmt.QueryContext(s.p.Ctx)
	} else { rows,err=s.conn.QueryContext(s.p.Ctx,"SHOW ALL") }
	if err != nil { s.t.Fatal(err) }; defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name,value,description string
		if err:=rows.Scan(&name,&value,&description); err!=nil { s.t.Fatal(err) }
		for _,d:=range plannerCostSettings { if name==d.name { if found[name] || value!=d.show || description!=d.description { s.t.Fatal(name,value,description) }; found[name]=true } }
	}
	if err:=rows.Err(); err!=nil || len(found)!=13 { s.t.Fatal(len(found),err) }
}

func TestReferencePlannerCostsAuthority(t *testing.T) {
	physicalCases(t,[]string{"planner_cost_authority_login"},func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE planner_cost_authority_login LOGIN PASSWORD 'role-core-fixture'",0))
		ordinary:=s.login("planner_cost_authority_login")
		for _,d:=range plannerCostSettings {
			ordinary.plannerCostSet(d.name,"2",false)
			show:="2"; if d.kind=="integer" { show="16kB" }
			ordinary.plannerCostValue(d,show,"2","session",true)
		}
		denied:=physicalDiagnostic("42501",`permission denied to set parameter "temp_file_limit"`)
		for _,prepared:=range []bool{false,true} {
			ordinary.run(physicalError("SET temp_file_limit='bogus'",denied,prepared,false))
			ordinary.run(physicalError("SELECT set_config($1,$2,false)",denied,prepared,true,"temp_file_limit","bogus"))
		}
		s.run(fixture.Exec("GRANT pg_read_all_settings TO planner_cost_authority_login",0))
		for _,d:=range plannerCostSettings {
			ordinary.run(fixture.Select("SELECT context FROM pg_settings WHERE name=$1",[]string{"context"},[]string{"TEXT"},[][]any{{"user"}},d.name))
		}
		ordinary.run(fixture.Exec("RESET ALL",0))
	})
}
