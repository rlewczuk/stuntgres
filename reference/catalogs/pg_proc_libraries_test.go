package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func pgProcLibraries(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	clean := func() {
		for _, q := range []string{`DROP EXTENSION IF EXISTS "uuid-ossp" CASCADE`, "DROP SCHEMA IF EXISTS pp_library CASCADE"} {
			if _, err := c.ExecContext(context.Background(), q); err != nil {
				t.Error(q, err)
			}
		}
	}
	defer clean()
	pgProcExec(t, p, b, c, `CREATE EXTENSION "uuid-ossp"`, "CREATE SCHEMA pp_library")
	var id int64
	if err := c.QueryRowContext(p.Ctx, "SELECT 'uuid_generate_v4()'::regprocedure::oid").Scan(&id); err != nil || id <= 0 {
		t.Fatal(id, err)
	}
	// REL_18_6/contrib/uuid-ossp/uuid-ossp--1.1.sql: true C symbol
	// and MODULE_PATHNAME replacement survive public-identity mutations.
	probe := func(name, schema string) {
		t.Helper()
		pgProcBoth(t, p, b, c, "SELECT p.proname,n.nspname,l.lanname,p.prosrc,p.probin,p.prosqlbody,p.protrftypes FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE p.oid=$1::oid", []string{"proname", "nspname", "lanname", "prosrc", "probin", "prosqlbody", "protrftypes"}, []string{"NAME", "NAME", "NAME", "TEXT", "TEXT", "PG_NODE_TREE", "_OID"}, [][]any{{name, schema, "c", "uuid_generate_v4", "$libdir/uuid-ossp", nil, nil}}, id)
	}
	probe("uuid_generate_v4", "public")
	pgProcExec(t, p, b, c, "ALTER FUNCTION uuid_generate_v4() RENAME TO pp_library_v4", "ALTER FUNCTION pp_library_v4() SET SCHEMA pp_library")
	probe("pp_library_v4", "pp_library")
	var text string
	if err := c.QueryRowContext(p.Ctx, "SELECT pp_library.pp_library_v4()::text").Scan(&text); err != nil {
		t.Fatal(err)
	}
	if len(text) != 36 || text[14] != '4' {
		t.Fatalf("real renamed/moved UUID v4 invocation %q", text)
	}
}
