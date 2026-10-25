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
			if !pg {
				// Native availability is its real embedded registry, not PostgreSQL's
				// larger system package installation or bootstrap plpgsql descriptor.
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
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extensions ORDER BY name", false, wantAvailable, pg))
				availableExtensionsBoth(t, p, c, r, availableExtensionsStep("SELECT * FROM pg_available_extension_versions ORDER BY name,version", true, wantVersions, pg))
				for _, name := range []string{"hstore", "file_fdw", "plpgsql"} {
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
