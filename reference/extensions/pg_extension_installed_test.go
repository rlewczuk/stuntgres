package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func pgExtensionInstalled(t *testing.T, p *fixture.Pair, b fixture.Backend) int64 {
	t.Helper()
	var oid int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_extension WHERE extname='plpgsql'").Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 4294967295 {
		t.Fatal("generated extension identity", oid)
	}
	s := fixture.Select(pgExtensionProbe, fixture.PGExtensionColumns, pgExtensionTypes, [][]any{{oid, "plpgsql", int64(10), int64(11), false, "1.0", nil, nil}}, "plpgsql")
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { pgExtensionMetadata(t, rows, b.PG) }
	pgExtensionBoth(t, p, b, s)
	return oid
}

func TestReferencePGExtensionInstalled(t *testing.T) {
	pgExtensionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		oid := pgExtensionInstalled(t, p, b)
		pgExtensionEmpty(t, p, b, pgExtensionProbe, pgExtensionAbsent)
		pgExtensionBoth(t, p, b, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_extension WHERE extname='plpgsql'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		pgExtensionBoth(t, p, b, fixture.Select("SELECT count(*) AS n FROM pg_catalog.pg_extension WHERE extname='"+pgExtensionAbsent+"'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgExtensionBoth(t, p, b, fixture.Select("SELECT e.extname,e.extowner,e.extnamespace,e.extrelocatable,e.extversion,c.relname FROM pg_catalog.pg_extension e JOIN pg_class c ON e.extnamespace=c.relnamespace WHERE c.oid=3079 AND e.extname='plpgsql' ORDER BY e.extname", []string{"extname", "extowner", "extnamespace", "extrelocatable", "extversion", "relname"}, []string{"NAME", "OID", "OID", "BOOL", "TEXT", "NAME"}, [][]any{{"plpgsql", int64(10), int64(11), false, "1.0", "pg_extension"}}))
		// Availability does not imply installation on either backend.
		var candidate string
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT name FROM pg_available_extensions WHERE name IN ('hstore','file_fdw') AND installed_version IS NULL ORDER BY name LIMIT 1").Scan(&candidate); err != nil {
			t.Fatal("real available-but-uninstalled control", err)
		}
		if candidate != "hstore" && candidate != "file_fdw" {
			t.Fatal("unexpected candidate", candidate)
		}
		pgExtensionEmpty(t, p, b, pgExtensionProbe, candidate)
		for _, name := range []string{"hstore", "file_fdw"} {
			pgExtensionEmpty(t, p, b, pgExtensionProbe, name)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pgext_installed_sentinel(id INT)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgext_installed_sentinel VALUES(7)", 1))
		if after := pgExtensionInstalled(t, p, b); after != oid {
			t.Fatal("user DDL changed installed identity", oid, after)
		}
		pgExtensionEmpty(t, p, b, pgExtensionProbe, candidate)
		pgExtensionEmpty(t, p, b, pgExtensionProbe, pgExtensionAbsent)
	})
}
