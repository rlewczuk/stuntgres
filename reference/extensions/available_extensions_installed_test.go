package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func availableExtensionsHeld(t *testing.T, p *fixture.Pair, stmt *sql.Stmt, versions, pg bool, want [][]any, args ...any) {
	t.Helper()
	rows, err := stmt.QueryContext(p.Ctx, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, types := availableExtensionsColumns, availableExtensionsTypes
	if versions {
		columns, types = availableVersionsColumns, availableVersionsTypes
	}
	availableExtensionsMetadata(t, rows, columns, types, pg)
	got := [][]any{}
	for rows.Next() {
		cells, dest := make([]any, len(columns)), make([]any, len(columns))
		for i := range dest {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range cells {
			if value == nil {
				continue
			}
			if pg && (types[i] == "NAME" || types[i] == "_NAME") {
				bytes, ok := value.([]byte)
				if !ok {
					t.Fatalf("pq name transport %d: %T", i, value)
				}
				cells[i] = string(bytes)
			} else if types[i] == "BOOL" {
				if _, ok := value.(bool); !ok {
					t.Fatalf("boolean transport %d: %T", i, value)
				}
			} else if _, ok := value.(string); !ok {
				t.Fatalf("string transport %d: %T", i, value)
			}
		}
		got = append(got, cells)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, want, true); !ok {
		t.Fatalf("held rows %#v want %#v", got, want)
	}
}

func TestReferenceAvailableExtensionsInstalled(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		queries := []string{"SELECT * FROM pg_available_extensions WHERE name=$1", "SELECT * FROM pg_available_extension_versions WHERE name=$1"}
		statements := make([]*sql.Stmt, len(queries))
		for i, query := range queries {
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			statements[i] = stmt
		}
		join := fixture.Select("SELECT a.name,a.installed_version,x.extversion FROM pg_available_extensions a JOIN pg_extension x ON a.name=x.extname WHERE a.name='unaccent'", []string{"name", "installed_version", "extversion"}, []string{"NAME", "TEXT", "TEXT"}, [][]any{})
		availableExtensionsBoth(t, p, c, r, join)
		availableExtensionsHeld(t, p, statements[0], false, pg, availableExtensionsRow(nil), "unaccent")
		availableExtensionsHeld(t, p, statements[1], true, pg, availableVersionsRow(false), "unaccent")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA ext_addon", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		availableExtensionsState(t, p, c, r, pg, true)
		availableExtensionsHeld(t, p, statements[0], false, pg, availableExtensionsRow("1.1"), "unaccent")
		availableExtensionsHeld(t, p, statements[1], true, pg, availableVersionsRow(true), "unaccent")
		for i, stmt := range statements {
			availableExtensionsHeld(t, p, stmt, i == 1, pg, [][]any{}, "available_absent")
		}
		join.Rows = [][]any{{"unaccent", "1.1", "1.1"}}
		availableExtensionsBoth(t, p, c, r, join)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE VIEW available_installed_status AS SELECT name,installed_version FROM pg_available_extensions WHERE name='unaccent'", 0))
		availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT * FROM available_installed_status", []string{"name", "installed_version"}, []string{"NAME", "TEXT"}, [][]any{{"unaccent", "1.1"}}))
		availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT name,installed_version FROM (SELECT name,installed_version FROM pg_available_extensions) a WHERE name='unaccent'", []string{"name", "installed_version"}, []string{"NAME", "TEXT"}, [][]any{{"unaccent", "1.1"}}))
		availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT ext_addon.unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
	})
}

func TestReferenceAvailableExtensionsUnknownInstalledVersion(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		availableExtensionsState(t, p, c, r, pg, true)
		if !pg {
			// Public unknown-version installation is deliberately unavailable. The
			// native pure projection unit supplies its independently specified witness.
			return
		}
		statements := make([]*sql.Stmt, 2)
		for i, view := range []string{"pg_available_extensions", "pg_available_extension_versions"} {
			stmt, err := c.PrepareContext(p.Ctx, "SELECT * FROM "+view+" WHERE name=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			statements[i] = stmt
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		defer c.ExecContext(p.Ctx, "ROLLBACK")
		for _, version := range []string{"available-unknown", "1.0"} {
			// Real PostgreSQL-only catalog mutation, always rolled back. This is
			// oracle grounding, not a native SQL/catalog-mutation capability.
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("UPDATE pg_catalog.pg_extension SET extversion=$1 WHERE extname='unaccent'", 1, version))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='unaccent'", false, availableExtensionsRow(version), pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='unaccent'", true, availableVersionsRow(false), pg))
			availableExtensionsHeld(t, p, statements[0], false, pg, availableExtensionsRow(version), "unaccent")
			availableExtensionsHeld(t, p, statements[1], true, pg, availableVersionsRow(false), "unaccent")
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='unaccent' AND version=$1", true, [][]any{}, pg, version))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		availableExtensionsState(t, p, c, r, pg, true)
		availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT unaccent('Hôtel')", []string{"unaccent"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
	})
}

// Retained native cursors own finalized rows and metadata independently of later
// catalog publication. PostgreSQL cursor/connection leasing is not this API.
func availableExtensionsRetained(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	availableExtensionsMetadata(t, rows, availableExtensionsColumns, availableExtensionsTypes, pg)
	got := [][]any{}
	for rows.Next() {
		var name, def, comment string
		var installed any
		if err := rows.Scan(&name, &def, &installed, &comment); err != nil {
			t.Fatal(err)
		}
		got = append(got, []any{name, def, installed, comment})
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("retained availability", got, want, err)
	}
}
