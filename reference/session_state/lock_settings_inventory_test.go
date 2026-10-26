package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLockSettingsInventory(t *testing.T) {
	physicalCases(t, []string{"lock_inventory_login"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE lock_inventory_login LOGIN PASSWORD 'role-core-fixture'", 0))
		ordinary := s.login("lock_inventory_login")
		for _, actor := range []*physicalCase{s, ordinary} {
			for _, prepared := range []bool{false, true} {
				actor.lockInventory(prepared, "1000", "default")
				actor.lockReads(prepared, "1s")
				actor.lockAll(prepared)
			}
		}
		for _, grant := range []string{"GRANT pg_read_all_settings TO lock_inventory_login", "GRANT SET ON PARAMETER deadlock_timeout TO lock_inventory_login", "REVOKE pg_read_all_settings FROM lock_inventory_login", "REVOKE SET ON PARAMETER deadlock_timeout FROM lock_inventory_login"} {
			s.run(fixture.Exec(grant, 0))
			ordinary.lockInventory(true, "1000", "default")
			ordinary.lockReads(true, "1s")
			ordinary.lockAll(true)
		}
		for _, query := range []struct{ sql, column string }{{"SHOW DEADLOCK_TIMEOUT", "deadlock_timeout"}, {"SELECT current_setting('DEADLOCK_TIMEOUT')", "current_setting"}} {
			held, err := s.conn.PrepareContext(s.p.Ctx, query.sql)
			if err != nil { s.t.Fatal(err) }
			s.run(clientText("SHOW deadlock_timeout", "deadlock_timeout", "1s"))
			for _, tc := range []struct{ sql, want string }{{"SET deadlock_timeout='2s'", "2s"}, {"RESET deadlock_timeout", "1s"}} {
				s.run(fixture.Exec(tc.sql, 0))
				step := clientText(query.sql, query.column, tc.want)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor:s.conn, Statement:held, SQL:query.sql}, s.recorder, step)
			}
			held.Close()
		}
	})
}

func (s *physicalCase) lockAll(prepared bool) {
	s.t.Helper()
	query := "SHOW ALL"
	stmt, err := s.conn.PrepareContext(s.p.Ctx, query)
	if err != nil { s.t.Fatal(err) }
	defer stmt.Close()
	var rows *sql.Rows
	if prepared {
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, query)
	}
	if err != nil { s.t.Fatal(err) }
	defer rows.Close()
	columns := []string{"name", "setting", "description"}
	gotColumns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(gotColumns, columns) { s.t.Fatal(gotColumns, err) }
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(columns) { s.t.Fatal(types, err) }
	for i, typ := range types {
		if typ.Name() != columns[i] || typ.DatabaseTypeName() != "TEXT" || typ.ScanType() != reflect.TypeFor[string]() { s.t.Fatal("SHOW ALL column", i, typ) }
		// SHOW utility columns are nonnullable, unlike pg_settings relation columns.
		if nullable, known := typ.Nullable(); nullable || known == s.pg { s.t.Fatal("SHOW ALL nullability", typ.Name(), nullable, known) }
		if length, known := typ.Length(); !known || length != math.MaxInt64 { s.t.Fatal("SHOW ALL length", typ.Name(), length, known) }
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known { s.t.Fatal("SHOW ALL decimal size", typ.Name(), precision, scale, known) }
	}
	seen := map[string]bool{}
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil { s.t.Fatal(err) }
		for _, d := range lockSettings {
			if name == d.name {
				if seen[name] || value != d.show || description != d.description { s.t.Fatal("SHOW ALL", name, value, description) }
				seen[name] = true
			}
		}
	}
	if err := rows.Err(); err != nil || len(seen) != 5 { s.t.Fatal("SHOW ALL inventory", seen, err) }
}
