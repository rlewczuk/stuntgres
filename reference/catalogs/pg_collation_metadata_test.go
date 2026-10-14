package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fmtCollationOID(oid int64) string { return strconv.FormatInt(oid, 10) }

func TestReferencePGCollationSeeds(t *testing.T) {
	pgCollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// REL_18_0 pg_collation.dat supplies fixed identities, not a promise
		// that the oracle's imported host inventory contains exactly seven rows.
		var unicodeVersion any
		if b.PG {
			// Observed in the pinned PostgreSQL18.0 fixture during oracle discovery.
			// Native provider information remains unavailable, not copied from ICU.
			unicodeVersion = "153.120"
		}
		want := [][]any{
			pgCollationRow(100, "default", 11, 10, "d", -1, nil, nil, nil, nil),
			pgCollationRow(811, "pg_c_utf8", 11, 10, "b", 6, nil, nil, "C.UTF-8", "1"),
			pgCollationRow(950, "C", 11, 10, "c", -1, "C", "C", nil, nil),
			pgCollationRow(951, "POSIX", 11, 10, "c", -1, "POSIX", "POSIX", nil, nil),
			pgCollationRow(962, "ucs_basic", 11, 10, "b", 6, nil, nil, "C", "1"),
			pgCollationRow(963, "unicode", 11, 10, "i", -1, nil, nil, "und", unicodeVersion),
			pgCollationRow(6411, "pg_unicode_fast", 11, 10, "b", 6, nil, nil, "PG_UNICODE_FAST", "1"),
		}
		s := fixture.Select("SELECT * FROM pg_catalog.pg_collation WHERE oid IN (100,811,950,951,962,963,6411) ORDER BY oid", pgCollationColumns, pgCollationTypes, want)
		for _, plain := range []bool{false, true} {
			s.NonContext = plain
			pgCollationStep(t, p, b, b.DB, s)
			pgCollationStep(t, p, b, b.DB, fixture.Prepared(s))
		}
		s.NonContext = false
		pgCollationStep(t, p, b, fixture.ViewHold(t, p, b, s.Query), s)
	})
}

func pgCollationFresh(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "pgc_fresh_schema")
	fixture.CollationExec(t, p, b, "CREATE USER pgc_fresh_owner", "CREATE SCHEMA pgc_fresh_schema AUTHORIZATION pgc_fresh_owner")
	q := "SELECT * FROM pg_catalog.pg_collation WHERE oid=$1"
	held := fixture.ViewHold(t, p, b, q)
	check := func(oid int64, row []any) {
		t.Helper()
		var want [][]any
		if row != nil {
			want = [][]any{row}
		}
		pgCollationStep(t, p, b, held, fixture.Select(q, pgCollationColumns, pgCollationTypes, want, oid))
	}
	check(4294967295, nil)
	fixture.CollationExec(t, p, b, `CREATE COLLATION pgc_fresh_copy FROM pg_catalog."C"`)
	oid := fixture.CollationOID(t, b.DB, "pgc_fresh_copy")
	var namespace, owner, targetNamespace int64
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'public'::regnamespace::oid").Scan(&namespace); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT collowner FROM pg_catalog.pg_collation WHERE oid=950").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pgc_fresh_schema'::regnamespace::oid").Scan(&targetNamespace); err != nil {
		t.Fatal(err)
	}
	if namespace <= 0 || targetNamespace <= 0 || namespace == targetNamespace || oid == 950 {
		t.Fatal("independent identities", namespace, targetNamespace, oid)
	}
	old := pgCollationRow(oid, "pgc_fresh_copy", namespace, owner, "c", -1, "C", "C", nil, nil)
	check(oid, old)
	var captured *sql.Rows
	if !b.PG {
		var err error
		captured, err = held.Statement.QueryContext(p.Ctx, oid)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		pgCollationMetadata(t, captured, false)
	}
	fixture.CollationExec(t, p, b, "ALTER COLLATION pgc_fresh_copy RENAME TO pgc_fresh_renamed")
	check(oid, pgCollationRow(oid, "pgc_fresh_renamed", namespace, owner, "c", -1, "C", "C", nil, nil))
	fixture.CollationExec(t, p, b, "ALTER COLLATION pgc_fresh_renamed SET SCHEMA pgc_fresh_schema")
	check(oid, pgCollationRow(oid, "pgc_fresh_renamed", targetNamespace, owner, "c", -1, "C", "C", nil, nil))
	// Discover the actual role identity relationally through a collation made
	// by the authenticated owner; no generated OID parity is asserted.
	if b.PG {
		fixture.CollationExec(t, p, b, "ALTER ROLE pgc_fresh_owner PASSWORD 'role-core-fixture'")
	}
	actor := fixture.RoleScenarioLoginPool(t, p.NativeConnector.Driver().(*stuntgres.Driver), b.PG, "reference/"+strings.TrimSuffix(t.Name(), "/"+b.Name), "pgc_fresh_owner")
	if _, err := actor.ExecContext(p.Ctx, `CREATE COLLATION pgc_fresh_schema.pgc_owner_probe FROM pg_catalog."C"`); err != nil {
		t.Fatal(err)
	}
	targetOwner := fixture.CollationOwnerOID(t, b.DB, "pgc_owner_probe")
	if targetOwner == owner {
		t.Fatal("ordinary owner adopted bootstrap identity")
	}
	fixture.CollationExec(t, p, b, "ALTER COLLATION pgc_fresh_schema.pgc_fresh_renamed OWNER TO pgc_fresh_owner")
	check(oid, pgCollationRow(oid, "pgc_fresh_renamed", targetNamespace, targetOwner, "c", -1, "C", "C", nil, nil))
	fixture.CollationExec(t, p, b, "DROP COLLATION pgc_fresh_schema.pgc_fresh_renamed")
	check(oid, nil)
	if captured != nil {
		if !captured.Next() {
			t.Fatal("missing retained row", captured.Err())
		}
		got := make([]any, 12)
		dest := make([]any, 12)
		for i := range got {
			dest[i] = &got[i]
		}
		if err := captured.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, v := range got {
			fixture.AssertRawValue(t, false, pgCollationTypes[i], v)
		}
		if !reflect.DeepEqual(got, old) || captured.Next() || captured.Err() != nil {
			t.Fatal("immutable retained row", got, captured.Err())
		}
	}
	fixture.CollationExec(t, p, b, "DROP COLLATION pgc_fresh_schema.pgc_owner_probe", "DROP SCHEMA pgc_fresh_schema")
}

func TestReferencePGCollationFreshMetadata(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgc_fresh_owner"})
	pgCollationScenario(t, pgCollationFresh)
}

func TestReferencePGCollationPublicRead(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgc_public_owner", "pgc_public_reader"})
	pgCollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.SchemaReferenceCleanup(t, "pgc_public_hidden")
		fixture.CollationExec(t, p, b, "CREATE USER pgc_public_owner", "CREATE USER pgc_public_reader", "CREATE SCHEMA pgc_public_hidden AUTHORIZATION pgc_public_owner")
		if b.PG {
			fixture.CollationExec(t, p, b, "ALTER ROLE pgc_public_owner PASSWORD 'role-core-fixture'", "ALTER ROLE pgc_public_reader PASSWORD 'role-core-fixture'")
		}
		d := p.NativeConnector.Driver().(*stuntgres.Driver)
		dsn := "reference/" + strings.TrimSuffix(t.Name(), "/"+b.Name)
		ownerDB := fixture.RoleScenarioLoginPool(t, d, b.PG, dsn, "pgc_public_owner")
		readerDB := fixture.RoleScenarioLoginPool(t, d, b.PG, dsn, "pgc_public_reader")
		if _, err := ownerDB.ExecContext(p.Ctx, `CREATE COLLATION pgc_public_hidden.pgc_public_copy FROM pg_catalog."C"`); err != nil {
			t.Fatal(err)
		}
		oid := fixture.CollationOID(t, b.DB, "pgc_public_copy")
		owner := fixture.CollationOwnerOID(t, b.DB, "pgc_public_copy")
		var namespace int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT 'pgc_public_hidden'::regnamespace::oid").Scan(&namespace); err != nil {
			t.Fatal(err)
		}
		want := [][]any{pgCollationRow(oid, "pgc_public_copy", namespace, owner, "c", -1, "C", "C", nil, nil)}
		q := "SELECT * FROM pg_catalog.pg_collation WHERE oid=$1"
		for _, db := range []*sql.DB{b.DB, ownerDB, readerDB} {
			if _, err := db.ExecContext(p.Ctx, "SET search_path=pg_catalog,public"); err != nil {
				t.Fatal(err)
			}
			pgCollationStep(t, p, b, db, fixture.Select(q, pgCollationColumns, pgCollationTypes, want, oid))
			pgCollationStep(t, p, b, db, fixture.Prepared(fixture.Select(q, pgCollationColumns, pgCollationTypes, want, oid)))
			fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Select("SELECT pg_collation_is_visible($1)", []string{"pg_collation_is_visible"}, []string{"BOOL"}, [][]any{{false}}, oid))
		}
		fixture.CollationExec(t, p, b, "DROP COLLATION pgc_public_hidden.pgc_public_copy", "DROP SCHEMA pgc_public_hidden")
	})
}

func TestReferencePGCollationFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgc_fresh_owner"})
	pgCollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgCollationStep(t, p, b, b.DB, fixture.Select("SELECT * FROM pg_catalog.pg_collation WHERE oid=811", pgCollationColumns, pgCollationTypes, [][]any{pgCollationRow(811, "pg_c_utf8", 11, 10, "b", 6, nil, nil, "C.UTF-8", "1")}))
		pgCollationFresh(t, p, b)
		pgCollationAttributeProbe(t, p, b)
		var unicodeVersion any
		if b.PG {
			unicodeVersion = "153.120"
		}
		pgCollationStep(t, p, b, b.DB, fixture.Select("SELECT * FROM pg_catalog.pg_collation WHERE oid=963", pgCollationColumns, pgCollationTypes, [][]any{pgCollationRow(963, "unicode", 11, 10, "i", -1, nil, nil, "und", unicodeVersion)}))
	})
}
