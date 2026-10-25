package fixture

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
)

var PGTableColumns = []string{"schemaname", "tablename", "tableowner", "tablespace", "hasindexes", "hasrules", "hastriggers", "rowsecurity"}

var PGTableTypes = []string{"NAME", "NAME", "NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}

var PGViewColumns = []string{"schemaname", "viewname", "viewowner", "definition"}

var PGViewTypes = []string{"NAME", "NAME", "NAME", "TEXT"}

func PGTableViewsOwner(b Backend) string {
	if b.PG {
		return "stuntgres"
	}
	return "postgres"
}

func PGTableViewMetadata(t *testing.T, types []*sql.ColumnType, columns, names []string, pg bool) {
	t.Helper()
	if len(types) != len(columns) {
		t.Fatalf("descriptor width: %v", types)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if names[i] == "NAME" && pg {
			scan = reflect.TypeFor[any]()
		}
		if names[i] == "BOOL" {
			scan = reflect.TypeFor[bool]()
		}
		length, sized := typ.Length()
		nullable, known := typ.Nullable()
		precision, scale, decimal := typ.DecimalSize()
		wantLength := int64(0)
		if names[i] == "TEXT" {
			wantLength = math.MaxInt64
		}
		if typ.Name() != columns[i] || typ.DatabaseTypeName() != names[i] || typ.ScanType() != scan || length != wantLength || sized != (names[i] == "TEXT") || nullable != !pg || known != !pg || precision != 0 || scale != 0 || decimal {
			t.Fatalf("column %d: %s %s scan=%v length=%d/%v nullable=%v/%v decimal=%d/%d/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), length, sized, nullable, known, precision, scale, decimal)
		}
	}
}

func PGTableViewSelect(b Backend, query string, columns, types []string, rows [][]any, args ...any) Step {
	s := Select(query, columns, types, rows, args...)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		descriptions, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		PGTableViewMetadata(t, descriptions, columns, types, b.PG)
	}
	return s
}

func PGTableRow(schema, name, owner string, indexed bool) []any {
	return []any{schema, name, owner, nil, indexed, false, false, false}
}

var PGViewBuiltinNames = []string{"pg_available_extension_versions", "pg_available_extensions", "pg_cursors", "pg_group", "pg_indexes", "pg_locks", "pg_matviews", "pg_prepared_xacts", "pg_roles", "pg_rules", "pg_sequences", "pg_settings", "pg_stat_all_tables", "pg_stat_progress_vacuum", "pg_stat_sys_tables", "pg_stat_user_tables", "pg_tables", "pg_timezone_abbrevs", "pg_timezone_names", "pg_user", "pg_user_mappings", "pg_views"}

func PGTableViewsNames(column string, names []string) string {
	terms := make([]string, len(names))
	for i, name := range names {
		terms[i] = column + "='" + name + "'"
	}
	return "(" + strings.Join(terms, " OR ") + ")"
}
