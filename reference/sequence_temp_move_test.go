package reference_test

import (
	"context"
	"testing"
	"time"
)

// Only PostgreSQL exposes pg_namespace here. Do not fabricate a native catalog
// query or equate the pg_temp alias with either backend's generated schema name.
func TestReferenceSequenceTempMoveNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			if p.postgres == nil {
				return
			}
			c, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := p.postgres.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			exec := func(q string) {
				sequenceProbeExec(t, p.ctx, c, q)
			}
			exec("CREATE TEMP SEQUENCE actual_temp START 31 CACHE 5")
			exec("CREATE SEQUENCE public.actual_persistent START 51 CACHE 5")
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('actual_temp')", 31)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('public.actual_persistent')", 51)
			var namespace string
			var oid int64
			var pid int
			if err := c.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&namespace); err != nil {
				t.Fatal(err)
			}
			if namespace == "" || namespace == "pg_temp" {
				t.Fatal("expected actual generated namespace", namespace)
			}
			if err := c.QueryRowContext(p.ctx, "SELECT 'pg_temp.actual_temp'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal(oid, err)
			}
			for _, q := range []string{
				"ALTER SEQUENCE public.actual_persistent SET SCHEMA " + namespace,
				"ALTER SEQUENCE " + namespace + ".actual_temp SET SCHEMA public",
			} {
				want := controlsReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas")
				referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, q, prepared), &want)
			}
			// A peer's actual namespace is independently temporary as well.
			sequenceProbeExec(t, p.ctx, peer, "CREATE TEMP SEQUENCE actual_peer START 81")
			var peerNamespace string
			if err := peer.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&peerNamespace); err != nil || peerNamespace == namespace {
				t.Fatal(namespace, peerNamespace, err)
			}
			want := controlsReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions")
			referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "ALTER SEQUENCE public.actual_persistent SET SCHEMA "+peerNamespace, prepared), &want)
			// CREATE and SET SCHEMA consume the same release creation authority.
			// A foreign temporary namespace must never acquire the new object.
			referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "CREATE SEQUENCE "+peerNamespace+".forbidden_new", prepared), &want)
			var absent bool
			if err := peer.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", peerNamespace+".forbidden_new").Scan(&absent); err != nil || !absent {
				t.Fatal("foreign temporary CREATE published an object", absent, err)
			}
			var after int64
			if err := c.QueryRowContext(p.ctx, "SELECT 'pg_temp.actual_temp'::regclass::oid::bigint").Scan(&after); err != nil || after != oid {
				t.Fatal("identity changed", oid, after, err)
			}
			referenceGenericRenameState(t, p, c, namespace+".actual_temp", 35, true, true)
			referenceGenericRenameState(t, p, c, "public.actual_persistent", 55, true, true)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('actual_temp')", 32)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('public.actual_persistent')", 52)
			// The release creation authority reports missing/denied destination
			// before considering a temporary source endpoint.
			exec("CREATE ROLE temp_move_oracle_role")
			defer func() {
				p.postgres.SetMaxIdleConns(0)
				_ = c.Close()
				if _, err := peer.ExecContext(p.ctx, "DROP ROLE temp_move_oracle_role"); err != nil {
					t.Error(err)
				}
			}()
			exec("SET ROLE temp_move_oracle_role")
			exec("CREATE TEMP SEQUENCE denied_temp START 91 CACHE 1")
			denied := controlsReferenceDiagnostic("42501", "permission denied for schema public")
			referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "ALTER SEQUENCE denied_temp SET SCHEMA public", prepared), &denied)
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('denied_temp')", 91)
			exec("RESET ROLE")
			exec("DROP SEQUENCE pg_temp.denied_temp")
			p.postgres.SetMaxIdleConns(0)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			// Close releases the client before backend teardown necessarily finishes.
			// Observe the actual server exit, as in the existing TEMP lifecycle fixture.
			ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
			defer cancel()
			for {
				var live bool
				if err := peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
					t.Fatal("observing physical backend exit", err)
				}
				if !live {
					break
				}
			}
			// Exact owner identity has disappeared, while the peer's object and
			// persistent visibility survive. Namespace tuple retirement is not
			// asserted: PostgreSQL retains that tuple for backend-slot reuse.
			var removed bool
			if err := peer.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", namespace+".actual_temp").Scan(&removed); err != nil || !removed {
				t.Fatal("physical owner cleanup", removed, err)
			}
			sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('actual_peer')", 81)
			sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('public.actual_persistent')", 56)
		})
	}
}
