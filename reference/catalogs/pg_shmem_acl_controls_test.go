package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func shmemACLControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for i, name := range shmemNames {
		function := "pg_get_shmem_allocations"
		if i == 1 { function = "pg_get_shmem_allocations_numa" }
		pgSequenceExec(t, p, b, b.DB, "GRANT SELECT ON pg_catalog."+name+" TO pg_signal_backend")
		pgSequenceExec(t, p, b, b.DB, "GRANT EXECUTE ON FUNCTION pg_catalog."+function+"() TO pg_signal_backend")
	}
	pgSequenceExec(t, p, b, b.DB, "REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_shmem_allocations() FROM pg_read_all_stats")
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	driver := stuntgres.NewDriver()
	connector, err := driver.OpenConnector("shmem-acl-controls")
	if err != nil { t.Fatal(err) }
	destination := sql.OpenDB(connector)
	defer destination.Close()
	if err := stuntgres.Restore(p.Ctx, destination, strings.NewReader(dump.String())); err != nil { t.Fatal(err) }
	for _, tc := range []struct{ query, name, acl string }{
		{"SELECT relacl::text FROM pg_class WHERE relname=$1", "pg_shmem_allocations", "{postgres=arwdDxtm/postgres,pg_read_all_stats=r/postgres,pg_signal_backend=r/postgres}"},
		{"SELECT relacl::text FROM pg_class WHERE relname=$1", "pg_shmem_allocations_numa", "{postgres=arwdDxtm/postgres,pg_read_all_stats=r/postgres,pg_signal_backend=r/postgres}"},
		{"SELECT proacl::text FROM pg_proc WHERE proname=$1", "pg_get_shmem_allocations", "{postgres=X/postgres,pg_signal_backend=X/postgres}"},
		{"SELECT proacl::text FROM pg_proc WHERE proname=$1", "pg_get_shmem_allocations_numa", "{postgres=X/postgres,pg_read_all_stats=X/postgres,pg_signal_backend=X/postgres}"},
	} {
		for _, pool := range []*sql.DB{p.Native, destination} {
			var acl string
			if err := pool.QueryRowContext(p.Ctx, tc.query, tc.name).Scan(&acl); err != nil || acl != tc.acl { t.Fatal("exact changed ACL replay", tc.name, acl, tc.acl, err) }
		}
	}
	conn, err := destination.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer conn.Close()
	if _, err := conn.ExecContext(p.Ctx, "SET ROLE pg_signal_backend"); err != nil { t.Fatal(err) }
	for i := range shmemNames { shmemRead(t, p, b, conn, i, true, false) }
	if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil { t.Fatal(err) }
	if _, err := conn.ExecContext(p.Ctx, "SET ROLE pg_read_all_stats"); err != nil { t.Fatal(err) }
	rows, err := conn.QueryContext(p.Ctx, "SELECT * FROM pg_catalog.pg_shmem_allocations")
	if rows != nil { rows.Close(); t.Fatal("revoked backing routine returned rows") }
	want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for function pg_get_shmem_allocations")
	fixture.AssertDiagnostic(t, err, &want)
	if _, err := conn.ExecContext(p.Ctx, "RESET ROLE"); err != nil { t.Fatal(err) }
	if err := stuntgres.Reset(p.Ctx, destination); err != nil { t.Fatal(err) }
	if _, err := conn.ExecContext(p.Ctx, "SET ROLE pg_read_all_stats"); err != nil { t.Fatal(err) }
	for i := range shmemNames { shmemRead(t, p, b, conn, i, false, false) }
}
