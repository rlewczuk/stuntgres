package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAvailableExtensionsFiltering(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		a, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_available_extensions WHERE name=$1")
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		v, err := c.PrepareContext(p.Ctx, "SELECT * FROM pg_available_extension_versions WHERE name=$1 AND version=$2")
		if err != nil {
			t.Fatal(err)
		}
		defer v.Close()
		for _, installed := range []bool{false, true} {
			var installedVersion any
			if installed {
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
				installedVersion = "1.1"
			}
			for _, name := range []any{"unaccent", "available_absent", nil} {
				want := [][]any{}
				if name == "unaccent" {
					want = availableExtensionsRow(installedVersion)
				}
				availableExtensionsHeld(t, p, a, false, pg, want, name)
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name=$1", false, want, pg, name))
			}
			for _, version := range []any{"1.1", "1.0", "available-unknown", nil} {
				want := [][]any{}
				if version == "1.1" {
					want = availableVersionsRow(installed)
				}
				availableExtensionsHeld(t, p, v, true, pg, want, "unaccent", version)
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name=$1 AND version=$2", true, want, pg, "unaccent", version))
			}
			wantNull := availableExtensionsRow(nil)
			if installed {
				wantNull = [][]any{}
			}
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='unaccent' AND installed_version IS NULL", false, wantNull, pg))
			for _, state := range []bool{false, true} {
				want := [][]any{}
				if state == installed {
					want = availableVersionsRow(installed)
				}
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='unaccent' AND installed=$1", true, want, pg, state))
			}
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='unaccent' AND installed=$1", true, [][]any{}, pg, nil))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='lo'", false, [][]any{{"lo", "1.2", nil, "Large Object maintenance"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='lo' AND version='1.2'", true, [][]any{{"lo", "1.2", false, true, true, true, nil, nil, "Large Object maintenance"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='hstore'", false, [][]any{{"hstore", "1.8", nil, "data type for storing sets of (key, value) pairs"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='hstore' AND version='1.8'", true, [][]any{{"hstore", "1.8", false, true, true, true, nil, nil, "data type for storing sets of (key, value) pairs"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='seg'", false, [][]any{{"seg", "1.4", nil, "data type for representing line segments or floating-point intervals"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='seg' AND version='1.4'", true, [][]any{{"seg", "1.4", false, true, true, true, nil, nil, "data type for representing line segments or floating-point intervals"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='tablefunc'", false, [][]any{{"tablefunc", "1.0", nil, "functions that manipulate whole tables, including crosstab"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='tablefunc' AND version='1.0'", true, [][]any{{"tablefunc", "1.0", false, true, true, true, nil, nil, "functions that manipulate whole tables, including crosstab"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='tcn'", false, [][]any{{"tcn", "1.0", nil, "Triggered change notifications"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='tcn' AND version='1.0'", true, [][]any{{"tcn", "1.0", false, true, true, true, nil, nil, "Triggered change notifications"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name='plpgsql'", false, [][]any{{"plpgsql", "1.0", "1.0", "PL/pgSQL procedural language"}}, pg))
			availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name='plpgsql' AND version='1.0'", true, [][]any{{"plpgsql", "1.0", true, true, true, false, "pg_catalog", nil, "PL/pgSQL procedural language"}}, pg))
			if !pg {
				// Native availability is its embedded registry, including the genuine
				// installed bootstrap plpgsql extension.
				wantAvailable := append([][]any{{"fuzzystrmatch", "1.2", nil, "determine similarities and distance between strings"}, {"intagg", "1.1", nil, "integer aggregator and enumerator (obsolete)"}, {"intarray", "1.5", nil, "functions, operators, and index support for 1-D arrays of integers"}, {"pgcrypto", "1.4", nil, "cryptographic functions"}, {"pgrowlocks", "1.2", nil, "show row-level locking information"}}, availableExtensionsRow(installedVersion)...)
				wantVersions := append([][]any{{"fuzzystrmatch", "1.2", false, true, true, true, nil, nil, "determine similarities and distance between strings"}, {"intagg", "1.1", false, true, false, true, nil, nil, "integer aggregator and enumerator (obsolete)"}, {"intarray", "1.5", false, true, true, true, nil, nil, "functions, operators, and index support for 1-D arrays of integers"}, {"pgcrypto", "1.4", false, true, true, true, nil, nil, "cryptographic functions"}, {"pgrowlocks", "1.2", false, true, false, true, nil, nil, "show row-level locking information"}}, availableVersionsRow(installed)...)
				wantAvailable = append(wantAvailable[:3:3], append([][]any{{"isn", "1.3", nil, "data types for international product numbering standards"}}, wantAvailable[3:]...)...)
				wantVersions = append(wantVersions[:3:3], append([][]any{{"isn", "1.3", false, true, true, true, nil, nil, "data types for international product numbering standards"}}, wantVersions[3:]...)...)
				wantAvailable = append(wantAvailable[:4:4], append([][]any{{"lo", "1.2", nil, "Large Object maintenance"}}, wantAvailable[4:]...)...)
				wantVersions = append(wantVersions[:4:4], append([][]any{{"lo", "1.2", false, true, true, true, nil, nil, "Large Object maintenance"}}, wantVersions[4:]...)...)
				wantVersions = append([][]any{{"fuzzystrmatch", "1.1", false, true, true, true, nil, nil, "determine similarities and distance between strings"}}, wantVersions...)
				wantAvailable = append([][]any{{"earthdistance", "1.2", nil, "calculate great-circle distances on the surface of the Earth"}}, wantAvailable...)
				wantVersions = append([][]any{{"earthdistance", "1.2", false, true, false, true, nil, "{cube}", "calculate great-circle distances on the surface of the Earth"}}, wantVersions...)
				wantAvailable = append([][]any{{"cube", "1.5", nil, "data type for multidimensional cubes"}}, wantAvailable...)
				wantVersions = append([][]any{{"cube", "1.5", false, true, true, true, nil, nil, "data type for multidimensional cubes"}}, wantVersions...)
				wantAvailable = append([][]any{{"citext", "1.8", nil, "data type for case-insensitive character strings"}}, wantAvailable...)
				wantVersions = append([][]any{{"citext", "1.8", false, true, true, true, nil, nil, "data type for case-insensitive character strings"}}, wantVersions...)
				wantAvailable = append(wantAvailable, []any{"uuid-ossp", "1.1", nil, "generate universally unique identifiers (UUIDs)"})
				wantVersions = append(wantVersions, []any{"uuid-ossp", "1.1", false, true, true, true, nil, nil, "generate universally unique identifiers (UUIDs)"})
				wantAvailable = append([][]any{{"amcheck", "1.5", nil, "functions for verifying relation integrity"}}, wantAvailable...)
				wantVersions = append([][]any{{"amcheck", "1.5", false, true, false, true, nil, nil, "functions for verifying relation integrity"}}, wantVersions...)
				wantAvailable = append(wantAvailable[:5:5], append([][]any{{"hstore", "1.8", nil, "data type for storing sets of (key, value) pairs"}}, wantAvailable[5:]...)...)
				wantVersions = append(wantVersions[:6:6], append([][]any{{"hstore", "1.8", false, true, true, true, nil, nil, "data type for storing sets of (key, value) pairs"}}, wantVersions[6:]...)...)
				wantAvailable = append(wantAvailable[:10:10], append([][]any{{"ltree", "1.3", nil, "data type for hierarchical tree-like structures"}}, wantAvailable[10:]...)...)
				wantVersions = append(wantVersions[:11:11], append([][]any{{"ltree", "1.3", false, true, true, true, nil, nil, "data type for hierarchical tree-like structures"}}, wantVersions[11:]...)...)
				wantAvailable = append(wantAvailable[:13:13], append([][]any{{"seg", "1.4", nil, "data type for representing line segments or floating-point intervals"}}, wantAvailable[13:]...)...)
				wantVersions = append(wantVersions[:14:14], append([][]any{{"seg", "1.4", false, true, true, true, nil, nil, "data type for representing line segments or floating-point intervals"}}, wantVersions[14:]...)...)
				wantAvailable = append(wantAvailable[:14:14], append([][]any{{"tablefunc", "1.0", nil, "functions that manipulate whole tables, including crosstab"}}, wantAvailable[14:]...)...)
				wantVersions = append(wantVersions[:15:15], append([][]any{{"tablefunc", "1.0", false, true, true, true, nil, nil, "functions that manipulate whole tables, including crosstab"}}, wantVersions[15:]...)...)
				wantAvailable = append(wantAvailable[:11:11], append([][]any{{"pageinspect", "1.13", nil, "inspect the contents of database pages at a low level"}}, wantAvailable[11:]...)...)
				wantVersions = append(wantVersions[:12:12], append([][]any{{"pageinspect", "1.13", false, true, false, true, nil, nil, "inspect the contents of database pages at a low level"}}, wantVersions[12:]...)...)
				wantAvailable = append(wantAvailable, []any{"plpgsql", "1.0", "1.0", "PL/pgSQL procedural language"})
				wantVersions = append(wantVersions, []any{"plpgsql", "1.0", true, true, true, false, "pg_catalog", nil, "PL/pgSQL procedural language"})
				wantAvailable = append(wantAvailable, []any{"tcn", "1.0", nil, "Triggered change notifications"})
				wantVersions = append(wantVersions, []any{"tcn", "1.0", false, true, true, true, nil, nil, "Triggered change notifications"})
				wantAvailable = append(wantAvailable, []any{"refint", "1.0", nil, "functions for implementing referential integrity (obsolete)"})
				wantVersions = append(wantVersions, []any{"refint", "1.0", false, true, false, true, nil, nil, "functions for implementing referential integrity (obsolete)"})
				wantAvailable = append(wantAvailable, []any{"autoinc", "1.0", nil, "functions for autoincrementing fields"})
				wantVersions = append(wantVersions, []any{"autoinc", "1.0", false, true, false, true, nil, nil, "functions for autoincrementing fields"})
				wantAvailable = append(wantAvailable, []any{"insert_username", "1.0", nil, "functions for tracking who changed a table"})
				wantVersions = append(wantVersions, []any{"insert_username", "1.0", false, true, false, true, nil, nil, "functions for tracking who changed a table"})
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions ORDER BY name", false, wantAvailable, pg))
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions ORDER BY name,version", true, wantVersions, pg))
				for _, name := range []string{"file_fdw"} {
					availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions WHERE name=$1", false, [][]any{}, pg, name))
					availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions WHERE name=$1", true, [][]any{}, pg, name))
				}
			} else {
				// Positive real-package control, explicitly not native inventory parity.
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT name FROM pg_available_extensions WHERE name='plpgsql'", []string{"name"}, []string{"NAME"}, [][]any{{"plpgsql"}}))
			}
		}
	})
}
