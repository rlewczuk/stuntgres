package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Preserve raw ACLs, not merely equivalent PUBLIC rights. Oracle fixture state
// outlives a scenario, and native Restore owns the existing isolated repair path.
func checkpointerRestoreACLs(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	var snapshot bytes.Buffer
	if !b.PG {
		if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil {
			t.Fatal("capture original ACLs", err)
		}
		return func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil {
				t.Error("restore original native ACLs", err)
			}
		}
	}
	var relation sql.NullString
	if err := c.QueryRowContext(p.Ctx, "SELECT relacl::text FROM pg_catalog.pg_class WHERE oid='pg_catalog.pg_stat_checkpointer'::regclass").Scan(&relation); err != nil {
		t.Fatal(err)
	}
	var routines [12]sql.NullString
	ids := [...]int{2769, 2770, 6377, 6327, 6328, 6329, 3160, 3161, 2771, 6366, 6314, 3775}
	for i, oid := range ids {
		if err := c.QueryRowContext(p.Ctx, "SELECT proacl::text FROM pg_catalog.pg_proc WHERE oid=$1", oid).Scan(&routines[i]); err != nil {
			t.Fatal(err)
		}
	}
	var columns [11]sql.NullString
	for i := range columns {
		if err := c.QueryRowContext(p.Ctx, "SELECT attacl::text FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_stat_checkpointer'::regclass AND attnum=$1", i+1).Scan(&columns[i]); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
		defer cancel()
		if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_class SET relacl=$1::aclitem[] WHERE oid='pg_catalog.pg_stat_checkpointer'::regclass", relation); err != nil {
			t.Error("restore original view ACL", err)
		}
		for i, oid := range ids {
			if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=$1::aclitem[] WHERE oid=$2", routines[i], oid); err != nil {
				t.Error("restore original getter/reset ACL", oid, err)
			}
		}
		for i, raw := range columns {
			if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_attribute SET attacl=$1::aclitem[] WHERE attrelid='pg_catalog.pg_stat_checkpointer'::regclass AND attnum=$2", raw, i+1); err != nil {
				t.Error("restore original column ACL", i+1, err)
			}
		}
	}
}
