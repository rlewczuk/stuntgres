package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const rowSecurityMetadataSQL = "SELECT name,setting,unit,category,short_desc,extra_desc,context,vartype,source,min_val,max_val,enumvals,boot_val,reset_val,sourcefile,sourceline,pending_restart FROM pg_settings WHERE name='row_security'"

var rowSecurityMetadataColumns = []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
var rowSecurityMetadataTypes = []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}

func rowSecurityMetadataRow(value, source string) []any {
	return []any{"row_security", value, nil, "Client Connection Defaults / Statement Behavior", "Enables row security.", "When enabled, row security will be applied to all users.", "user", "bool", source, nil, nil, nil, "on", "on", nil, nil, false}
}

func rowSecurityMetadata(s *membershipScenario, value, source string) {
	s.t.Helper()
	step := fixture.Select(rowSecurityMetadataSQL, rowSecurityMetadataColumns, rowSecurityMetadataTypes, [][]any{rowSecurityMetadataRow(value, source)})
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 17 {
			t.Fatal(types, err)
		}
		for i, typ := range types {
			if typ.Name() != rowSecurityMetadataColumns[i] || typ.DatabaseTypeName() != rowSecurityMetadataTypes[i] {
				t.Fatalf("column %d: %v", i, typ)
			}
			nullable, known := typ.Nullable()
			if known == s.backend.PG || (!s.backend.PG && !nullable) || (s.backend.PG && nullable) {
				t.Fatalf("column %s nullable=(%v,%v)", typ.Name(), nullable, known)
			}
			if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
				t.Fatalf("column %s decimal=(%d,%d,%v)", typ.Name(), precision, scale, known)
			}
			if rowSecurityMetadataTypes[i] == "TEXT" {
				if typ.ScanType() != reflect.TypeFor[string]() {
					t.Fatalf("column %s scan=%v", typ.Name(), typ.ScanType())
				}
				if length, known := typ.Length(); !known || length != math.MaxInt64 {
					t.Fatalf("column %s length=(%d,%v)", typ.Name(), length, known)
				}
			}
			if rowSecurityMetadataTypes[i] != "TEXT" {
				scan := reflect.TypeFor[string]()
				switch rowSecurityMetadataTypes[i] {
				case "_TEXT":
					if s.backend.PG {
						scan = reflect.TypeFor[any]()
					}
				case "INT4":
					scan = reflect.TypeFor[int64]()
					if s.backend.PG {
						scan = reflect.TypeFor[int32]()
					}
				case "BOOL":
					scan = reflect.TypeFor[bool]()
				}
				if typ.ScanType() != scan {
					t.Fatalf("column %s scan=%v want %v", typ.Name(), typ.ScanType(), scan)
				}
				if length, known := typ.Length(); length != 0 || known {
					t.Fatalf("column %s length=(%d,%v)", typ.Name(), length, known)
				}
			}
		}
	}
	s.run(step)
}

func TestReferenceRowSecurityMetadata(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		rowSecurityMetadata(s, "on", "default")
		// Provenance follows override presence, not equality with the boot text.
		// SET to boot text must not be mistaken for RESET/default provenance.
		for _, tc := range []struct{ name, sql, value, source string }{
			{"set_off", "SET row_security=off", "off", "session"},
			{"set_boot", "SET row_security=on", "on", "session"},
			{"set_default", "SET row_security=DEFAULT", "on", "default"},
			{"reset", "RESET row_security", "on", "default"},
			{"set_again", "SET row_security=off", "off", "session"},
			{"reset_all", "RESET ALL", "on", "default"},
		} {
			rowSecurityCase(s, tc.name, func() {
				s.exec(tc.sql)
				rowSecurityValue(s, tc.value)
				rowSecurityMetadata(s, tc.value, tc.source)
			})
		}
		predefinedSelect(s, "SELECT set_config('row_security','off',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"off"}})
		rowSecurityMetadata(s, "off", "session")
		predefinedSelect(s, "SELECT set_config('row_security',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"on"}})
		rowSecurityMetadata(s, "on", "default")
		for _, value := range []string{"on", "off"} {
			s.exec("SET row_security=" + value)
			accessRoleBool(s, "SELECT row_security_active('t'::text) AS v", true)
			accessRoleBool(s, "SELECT row_security_active('t'::regclass::oid) AS v", true)
		}
		for _, actor := range []string{"rls_owner", "rls_bypass", ""} {
			s.actor(actor)
			accessRoleBool(s, "SELECT row_security_active('t'::text) AS v", false)
		}
		s.actor("rls_owner")
		s.exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		accessRoleBool(s, "SELECT row_security_active('t'::regclass::oid) AS v", false)
		// Native eager rows survive same-session mutation. PostgreSQL must
		// drain its live protocol stream before issuing the companion SET.
		rows, err := s.c.QueryContext(s.p.Ctx, rowSecurityMetadataSQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer rows.Close()
		if !s.backend.PG {
			s.exec("SET row_security=on")
		}
		values := make([]any, 17)
		dest := make([]any, 17)
		for i := range values {
			dest[i] = &values[i]
		}
		if !rows.Next() {
			s.t.Fatal("missing held metadata row", rows.Err())
		}
		if err := rows.Scan(dest...); err != nil {
			s.t.Fatal(err)
		}
		for i := range values {
			values[i] = fixture.Normalize(values[i])
		}
		if !reflect.DeepEqual(values, rowSecurityMetadataRow("off", "session")) {
			s.t.Fatalf("held metadata: %#v", values)
		}
		if rows.Next() || rows.Err() != nil {
			s.t.Fatal("held metadata tail", rows.Err())
		}
		rows.Close()
		if s.backend.PG {
			s.exec("SET row_security=on")
		}
		rowSecurityMetadata(s, "on", "session")
	})
}
