package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func ipRetainRoleRows(t *testing.T, p *fixture.Pair, b fixture.Backend, oid int64) func() {
	t.Helper()
	if b.PG {
		return func() {}
	}
	rows, err := b.DB.QueryContext(p.Ctx, "SELECT * FROM pg_init_privs WHERE classoid=1259 AND objoid=$1 AND objsubid=0", oid)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rows.Close() })
	metadata, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		if !rows.Next() {
			t.Fatal("captured initial privilege row lost", rows.Err())
		}
		var object, class, sub int64
		var kind, acl string
		if err := rows.Scan(&object, &class, &sub, &kind, &acl); err != nil {
			t.Fatal(err)
		}
		if object != oid || class != 1259 || sub != 0 || kind != "e" || !strings.Contains(acl, "ip_reader=r/") || strings.Contains(acl, "ip_renamed") || strings.Contains(acl, "ip_target") {
			t.Fatal("role rename/name reuse mutated captured ACL", object, class, sub, kind, acl)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("captured EOF", rows.Err())
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		ipMetadata(t, metadata, false)
	}
}
