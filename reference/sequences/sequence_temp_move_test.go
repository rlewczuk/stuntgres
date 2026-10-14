package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Read actual TEMP namespace identities through pg_class/pg_namespace, retaining
// the full creation/move precedence contract on every available backend.
func TestReferenceSequenceTempMoveNamespaceAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"temp_move_oracle_role"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					c, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					peer, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					exec := func(q string) {
						fixture.SequenceProbeExec(t, p.Ctx, c, q)
					}
					exec("CREATE TEMP SEQUENCE actual_temp START 31 CACHE 5")
					exec("CREATE SEQUENCE public.actual_persistent START 51 CACHE 5")
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('actual_temp')", 31)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('public.actual_persistent')", 51)
					var namespace string
					var oid int64
					var pid int
					if backend.PG {
						if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
							t.Fatal(err)
						}
					}
					if err := c.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='pg_temp.actual_temp'::regclass::oid").Scan(&namespace); err != nil {
						t.Fatal(err)
					}
					if namespace == "" || namespace == "pg_temp" {
						t.Fatal("expected actual generated namespace", namespace)
					}
					if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_temp.actual_temp'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
						t.Fatal(oid, err)
					}
					for _, q := range []string{
						"ALTER SEQUENCE public.actual_persistent SET SCHEMA " + namespace,
						"ALTER SEQUENCE " + namespace + ".actual_temp SET SCHEMA public",
					} {
						want := fixture.ControlsReferenceDiagnostic("0A000", "cannot move objects into or out of temporary schemas")
						fixture.AssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, q, prepared), &want)
					}
					// A peer's actual namespace is independently temporary as well.
					fixture.SequenceProbeExec(t, p.Ctx, peer, "CREATE TEMP SEQUENCE actual_peer START 81")
					var peerNamespace string
					if err := peer.QueryRowContext(p.Ctx, "SELECT n.nspname FROM pg_namespace n JOIN pg_class c ON c.relnamespace=n.oid WHERE c.oid='pg_temp.actual_peer'::regclass::oid").Scan(&peerNamespace); err != nil || peerNamespace == namespace {
						t.Fatal(namespace, peerNamespace, err)
					}
					want := fixture.ControlsReferenceDiagnostic("42P16", "cannot create relations in temporary schemas of other sessions")
					fixture.AssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "ALTER SEQUENCE public.actual_persistent SET SCHEMA "+peerNamespace, prepared), &want)
					// CREATE and SET SCHEMA consume the same release creation authority.
					// A foreign temporary namespace must never acquire the new object.
					fixture.AssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "CREATE SEQUENCE "+peerNamespace+".forbidden_new", prepared), &want)
					var absent bool
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*)=0 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='forbidden_new'", peerNamespace).Scan(&absent); err != nil || !absent {
						t.Fatal("foreign temporary CREATE published an object", absent, err)
					}
					var after int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_temp.actual_temp'::regclass::oid::bigint").Scan(&after); err != nil || after != oid {
						t.Fatal("identity changed", oid, after, err)
					}
					fixture.GenericRenameState(t, p, c, namespace+".actual_temp", 35, true, backend.PG)
					fixture.GenericRenameState(t, p, c, "public.actual_persistent", 55, true, backend.PG)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('actual_temp')", 32)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('public.actual_persistent')", 52)
					// The release creation authority reports missing/denied destination
					// before considering a temporary source endpoint.
					exec("CREATE ROLE temp_move_oracle_role")
					defer func() {
						backend.DB.SetMaxIdleConns(0)
						_ = c.Close()
						if _, err := peer.ExecContext(p.Ctx, "DROP ROLE temp_move_oracle_role"); err != nil {
							t.Error(err)
						}
					}()
					exec("SET ROLE temp_move_oracle_role")
					exec("CREATE TEMP SEQUENCE denied_temp START 91 CACHE 1")
					denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema public")
					fixture.AssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, "ALTER SEQUENCE denied_temp SET SCHEMA public", prepared), &denied)
					fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('denied_temp')", 91)
					exec("RESET ROLE")
					exec("DROP SEQUENCE pg_temp.denied_temp")
					backend.DB.SetMaxIdleConns(0)
					if err := c.Close(); err != nil {
						t.Fatal(err)
					}
					// Close releases the client before backend teardown necessarily finishes.
					// Observe the actual server exit, as in the existing TEMP lifecycle fixture.
					if backend.PG {
						ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
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
					}
					// Owner objects disappear while peer objects and persistent visibility survive;
					// both backends retain actual empty namespace identities.
					var removed bool
					if err := peer.QueryRowContext(p.Ctx, "SELECT count(*)=0 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname='actual_temp'", namespace).Scan(&removed); err != nil || !removed {
						t.Fatal("physical owner cleanup", removed, err)
					}
					fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('actual_peer')", 81)
					fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('public.actual_persistent')", 56)
				})
			}
		})
	}
}
