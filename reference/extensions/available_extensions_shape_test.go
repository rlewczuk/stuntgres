package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

const availableExtensionsComment = "text search dictionary that removes accents"

var availableExtensionsColumns = []string{"name", "default_version", "installed_version", "comment"}
var availableExtensionsTypes = []string{"NAME", "TEXT", "TEXT", "TEXT"}
var availableVersionsColumns = []string{"name", "version", "installed", "superuser", "trusted", "relocatable", "schema", "requires", "comment"}
var availableVersionsTypes = []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL", "NAME", "_NAME", "TEXT"}

func availableExtensionsCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool)) {
	t.Helper()
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		body(t, p, c, r, pg)
	})
}

// These rows follow REL_18_0 unaccent.control and system_views.sql, not native output.
func availableExtensionsRow(installed any) [][]any {
	return [][]any{{"unaccent", "1.1", installed, availableExtensionsComment}}
}
func availableVersionsRow(installed bool) [][]any {
	return [][]any{{"unaccent", "1.1", installed, true, true, true, nil, nil, availableExtensionsComment}}
}
func availableExtensionsStep(query string, versions bool, rows [][]any, pg bool, args ...any) fixture.Step {
	columns, types := availableExtensionsColumns, availableExtensionsTypes
	if versions {
		columns, types = availableVersionsColumns, availableVersionsTypes
	}
	s := fixture.Select(query, columns, types, rows, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { availableExtensionsMetadata(t, rows, columns, types, pg) }
	return s
}
func availableExtensionsMetadata(t *testing.T, rows *sql.Rows, columns, names []string, pg bool) {
	t.Helper()
	got, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(got, columns) {
		t.Fatal("available extension columns", got, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != len(names) {
		t.Fatal("available extension types", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[string]()
		if names[i] == "BOOL" {
			scan = reflect.TypeFor[bool]()
		} else if pg && (names[i] == "NAME" || names[i] == "_NAME") {
			scan = reflect.TypeFor[any]()
		}
		if typ.Name() != columns[i] || typ.DatabaseTypeName() != names[i] || typ.ScanType() != scan {
			t.Fatalf("column %d: %s %s %v; want %s %s %v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType(), columns[i], names[i], scan)
		}
		nullable, known := typ.Nullable()
		if pg && (nullable || known) || !pg && (!nullable || !known) {
			t.Fatal("view attribute nullability", i, nullable, known, pg)
		}
		length, known := typ.Length()
		wantLength, wantKnown := int64(0), false
		if names[i] == "TEXT" {
			wantLength, wantKnown = 9223372036854775807, true
		}
		if length != wantLength || known != wantKnown {
			t.Fatal("view attribute length", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("view attribute decimal", i, precision, scale, known)
		}
	}
}
func availableExtensionsBoth(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, s)
	fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
}
func availableExtensionsState(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, pg bool, installed bool) {
	t.Helper()
	var version any
	if installed {
		version = "1.1"
	}
	availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_catalog.pg_available_extensions WHERE name='unaccent'", false, availableExtensionsRow(version), pg))
	availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_catalog.pg_available_extension_versions WHERE name='unaccent'", true, availableVersionsRow(installed), pg))
}

func TestReferenceAvailableExtensionsShape(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, schema := range []string{"", "pg_catalog."} {
			for _, versions := range []bool{false, true} {
				view, want := "pg_available_extensions", availableExtensionsRow(nil)
				if versions {
					view, want = "pg_available_extension_versions", availableVersionsRow(false)
				}
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM "+schema+view+" WHERE name='unaccent'", versions, want, pg))
				for _, predicate := range []string{"FALSE", "name='available_absent'"} {
					availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM "+schema+view+" WHERE "+predicate, versions, [][]any{}, pg))
				}
			}
		}
	})
}
