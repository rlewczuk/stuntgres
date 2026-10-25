package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent release expectations, not engine registry data.
type compatibleSetting struct {
	name, initial, alternate, description, extra string
}

var compatibleSettings = []compatibleSetting{
	{"allow_alter_system", "on", "off", "Allows running the ALTER SYSTEM command.", "Can be set to off for environments where global configuration changes should be made using a different method."},
	{"array_nulls", "on", "off", "Enables input of NULL elements in arrays.", "When turned on, unquoted NULL in an array input value means a null value; otherwise it is taken literally."},
	{"backslash_quote", "safe_encoding", "on", `Sets whether "\'" is allowed in string literals.`, ""},
	{"escape_string_warning", "on", "off", "Warn about backslash escapes in ordinary string literals.", ""},
	{"lo_compat_privileges", "off", "on", "Enables backward compatibility mode for privilege checks on large objects.", "Skips privilege checks when reading or modifying large objects, for compatibility with PostgreSQL releases prior to 9.0."},
	{"quote_all_identifiers", "off", "on", "When generating SQL fragments, quote all identifiers.", ""},
	{"standard_conforming_strings", "on", "off", "Causes '...' strings to treat backslashes literally.", ""},
	{"synchronize_seqscans", "on", "off", "Enables synchronized sequential scans.", ""},
	{"transform_null_equals", "off", "on", `Treats "expr=NULL" as "expr IS NULL".`, "When turned on, expressions of the form expr = NULL (or NULL = expr) are treated as expr IS NULL, that is, they return true if expr evaluates to the null value, and false otherwise. The correct behavior of expr = NULL is to always return null (unknown)."},
}

func compatibleNames() string {
	out := ""
	for _, tc := range compatibleSettings {
		if out != "" {
			out += ","
		}
		out += physicalLiteral(tc.name)
	}
	return out
}

func (s *physicalCase) compatibleReads(tc compatibleSetting, value string, prepared bool) {
	s.clientReads(clientSetting{name: tc.name}, value, prepared)
}

func compatibleMetadata(tc compatibleSetting, value, source string) []any {
	category := "Version and Platform Compatibility / Previous PostgreSQL Versions"
	context, kind := "user", "bool"
	var extra, enums any
	if tc.extra != "" {
		extra = tc.extra
	}
	if tc.name == "allow_alter_system" || tc.name == "transform_null_equals" {
		category = "Version and Platform Compatibility / Other Platforms and Clients"
	}
	if tc.name == "allow_alter_system" {
		context = "sighup"
	}
	if tc.name == "lo_compat_privileges" {
		context = "superuser"
	}
	if tc.name == "backslash_quote" {
		kind, enums = "enum", "{safe_encoding,on,off}"
	}
	return []any{tc.name, value, nil, category, tc.description, extra, context, kind, source, nil, nil, enums, tc.initial, tc.initial, nil, nil, false}
}

func (s *physicalCase) compatibleMetadata(tc compatibleSetting, value, source string, prepared bool) {
	step := fixture.Select("SELECT "+physicalColumnsSQL+" FROM pg_settings WHERE name="+physicalLiteral(tc.name), physicalColumns, physicalTypes, [][]any{compatibleMetadata(tc, value, source)})
	step.Prepared = prepared
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		statisticsSettingsCheckMetadata(t, rows, s.pg, physicalColumns, physicalTypes)
	}
	s.run(step)
}

func (s *physicalCase) compatibleVisible(prepared bool) {
	var want [][]any
	for _, tc := range compatibleSettings {
		want = append(want, []any{tc.name})
	}
	step := fixture.Select("SELECT name FROM pg_settings WHERE name IN ("+compatibleNames()+") ORDER BY name", []string{"name"}, []string{"TEXT"}, want)
	step.Prepared = prepared
	s.run(step)
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, e := s.conn.PrepareContext(s.p.Ctx, "SHOW ALL")
		if e != nil {
			s.t.Fatal(e)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(s.p.Ctx)
	} else {
		rows, err = s.conn.QueryContext(s.p.Ctx, "SHOW ALL")
	}
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"name", "setting", "description"}) {
		s.t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		s.t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "TEXT" {
			s.t.Fatal(typ)
		}
	}
	seen := make(map[string]bool)
	for rows.Next() {
		var name, value, description string
		if err := rows.Scan(&name, &value, &description); err != nil {
			s.t.Fatal(err)
		}
		for _, tc := range compatibleSettings {
			if name == tc.name {
				if seen[name] || value != tc.initial || description != tc.description {
					s.t.Fatal(name, value, description)
				}
				seen[name] = true
			}
		}
	}
	if rows.Err() != nil || len(seen) != 9 {
		s.t.Fatal(seen, rows.Err())
	}
}

func TestReferenceCompatibleSettingsInventory(t *testing.T) {
	for _, tc := range compatibleSettings {
		t.Run(tc.name, func(t *testing.T) {
			physicalCases(t, nil, func(s *physicalCase) {
				for _, prepared := range []bool{false, true} {
					s.compatibleReads(tc, tc.initial, prepared)
					s.compatibleMetadata(tc, tc.initial, "default", prepared)
					if tc.name == "allow_alter_system" {
						continue
					}
					step := fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.alternate), 0)
					step.Prepared = prepared
					s.run(step)
					s.compatibleReads(tc, tc.alternate, prepared)
					s.compatibleMetadata(tc, tc.alternate, "session", prepared)
					step = clientText("SELECT set_config($1,$2,false)", "set_config", tc.alternate)
					step.Args, step.Prepared = []any{tc.name, tc.alternate}, prepared
					s.run(step)
					for _, query := range []string{"SET " + tc.name + "=DEFAULT", "RESET " + tc.name} {
						step = fixture.Exec(query, 0)
						step.Prepared = prepared
						s.run(step)
						s.compatibleReads(tc, tc.initial, prepared)
						s.compatibleMetadata(tc, tc.initial, "default", prepared)
					}
				}
			})
		})
	}
	t.Run("visible-inventories", func(t *testing.T) {
		physicalCases(t, []string{"compatible_inventory_login"}, func(s *physicalCase) {
			s.run(fixture.Exec("CREATE ROLE compatible_inventory_login LOGIN PASSWORD 'role-core-fixture'", 0))
			ordinary := s.login("compatible_inventory_login")
			for _, actor := range []*physicalCase{s, ordinary} {
				for _, prepared := range []bool{false, true} {
					actor.compatibleVisible(prepared)
				}
			}
		})
	})
}
