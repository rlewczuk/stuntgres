package reference_test

import (
	"database/sql"
	"path"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceResourceSettingsInventory(t *testing.T) {
	physicalCases(t, []string{"resource_inventory_login", "resource_inventory_reader"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE resource_inventory_login LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("CREATE ROLE resource_inventory_reader", 0))
		ordinary := s.login("resource_inventory_login")
		for _, observer := range []*physicalCase{s, ordinary} {
			for _, prepared := range []bool{false, true} {
				observer.resourceInventory(prepared, observer == s)
			}
		}
		s.run(fixture.Exec("GRANT pg_read_all_settings TO resource_inventory_reader", 0))
		s.run(fixture.Exec("GRANT resource_inventory_reader TO resource_inventory_login WITH INHERIT TRUE", 0))
		ordinary.resourceInventory(true, true)
	})
}

func (s *physicalCase) resourceInventory(prepared, provenance bool) {
	s.t.Helper()
	query := "SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE "+resourcePredicate()+" ORDER BY name"
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, query)
		if e != nil { s.t.Fatal(e) }
		defer stmt.Close()
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
		if count >= len(resourceSettings) { s.t.Fatalf("extra resource row %v", cells) }
		d := resourceSettings[count]
		raw, _, source := s.resourceInitial(d)
		var sourcefile, sourceline any
		if s.pg && provenance && source == "configuration file" {
			// initdb's configuration pathname and line positions are environmental,
			// but only these two released initialized settings may expose them.
			file, ok := cells[14].(string)
			if !ok || !path.IsAbs(file) || path.Base(file) != "postgresql.conf" { s.t.Fatalf("invalid configuration provenance: %v", cells) }
			line, ok := cells[15].(int64)
			if !ok || line <= 0 { s.t.Fatalf("invalid configuration line: %v", cells) }
			sourcefile, sourceline = file, line
		}
		enums := d.enums
		// Debian's PostgreSQL build enables liburing; native intentionally
		// advertises only the simulated sync/worker profile.
		if s.pg && d.name == "io_method" { enums = "{sync,worker,io_uring}" }
		want := []any{d.name, raw, resourceNullable(d.unit), "Resource Usage / "+d.category, d.description, resourceNullable(d.extra), d.context, d.kind, source, resourceNullable(d.min), resourceNullable(d.max), resourceNullable(enums), d.boot, raw, sourcefile, sourceline, false}
		if !reflect.DeepEqual(cells, want) { s.t.Fatalf("%s pg_settings got %#v want %#v", d.name, cells, want) }
		count++
	}
	if err := rows.Err(); err != nil || count != 44 { s.t.Fatalf("resource count=%d want44: %v", count, err) }
	rows.Close()
	for _, d := range resourceSettings {
		raw, show, source := s.resourceInitial(d)
		s.resourceValue(strings.ToUpper(d.name), show, raw, source, prepared)
	}
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, "SHOW ALL")
		if e != nil { s.t.Fatal(e) }
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else { rows, err = s.conn.QueryContext(s.p.Ctx, "SHOW ALL") }
	if err != nil { s.t.Fatal(err) }
	defer rows.Close()
	found := map[string]bool{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil { s.t.Fatal(err) }
		for _, d := range resourceSettings {
			if name != d.name { continue }
			_, show, _ := s.resourceInitial(d)
			if found[name] || value != show || description != d.description { s.t.Fatalf("SHOW ALL resource %q %q %q", name, value, description) }
			found[name] = true
		}
	}
	if err := rows.Err(); err != nil || len(found) != 44 { s.t.Fatalf("SHOW ALL resource count=%d: %v", len(found), err) }
}

func TestReferenceResourceSettingsContext(t *testing.T) {
	for _, d := range resourceSettings {
		if d.context != "postmaster" && d.context != "sighup" { continue }
		t.Run(d.name, func(t *testing.T) {
			physicalCases(t, []string{"resource_context_login"}, func(s *physicalCase) {
				s.run(fixture.Exec("CREATE ROLE resource_context_login LOGIN PASSWORD 'role-core-fixture'", 0))
				ordinary := s.login("resource_context_login")
				peer := s.lockPeer()
				for phase, observer := range []*physicalCase{s, ordinary, ordinary} {
					if phase == 2 { s.run(fixture.Exec("GRANT SET ON PARAMETER "+d.name+" TO resource_context_login",0)) }
					for _, prepared := range []bool{false, true} {
						raw, _, _ := observer.resourceInitial(d)
						for _, command := range []string{"SET "+d.name+"="+physicalLiteral(d.boot), "SET "+d.name+"="+physicalLiteral(raw), "SET "+d.name+"='bogus'", "SET "+d.name+"=DEFAULT", "RESET "+d.name, "BEGIN; SET LOCAL "+d.name+"='bogus'"} {
							if strings.HasPrefix(command, "BEGIN;") { observer.run(fixture.Exec("BEGIN", 0)); command = strings.TrimPrefix(command, "BEGIN; ") }
							observer.run(physicalError(command, lockContext(d.name, d.context), prepared, false))
							if strings.HasPrefix(command, "SET LOCAL") { observer.run(fixture.Exec("ROLLBACK", 0)) }
						}
						for _, local := range []bool{false, true} {
							for _, value := range []any{raw,"bogus",nil} { observer.run(physicalError("SELECT set_config($1,$2,$3)", lockContext(d.name,d.context), prepared,true,d.name,value,local)) }
						}
						observer.run(physicalError("SET "+d.name+" TO 1,2", physicalDiagnostic("22023", "SET "+d.name+" takes only one argument"), prepared, false))
					}
				}
				ordinary.run(fixture.Exec("RESET ALL", 0))
				for _, observer := range []*physicalCase{s,ordinary,peer} { raw,show,source := observer.resourceInitial(d); observer.resourceValue(d.name,show,raw,source,true) }
				s.run(fixture.Exec("REVOKE SET ON PARAMETER "+d.name+" FROM resource_context_login",0))
			})
		})
	}
}
