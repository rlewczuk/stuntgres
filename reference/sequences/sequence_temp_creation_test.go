package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// PostgreSQL alone exposes actual namespace names through public catalogs.
// Native actual-identity coverage belongs to the engine authority fixture.
func TestReferenceSequenceTempCreationActualNamespace(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			if p.Postgres == nil {
				return
			}
			c, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			peer, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			exec := func(q string) { fixture.SequenceProbeExec(t, p.Ctx, c, q) }
			exec("CREATE TEMP SEQUENCE actual_seed START 31 CACHE 5")
			exec("CREATE SEQUENCE public.actual_survivor START 71 CACHE 1")
			fixture.SequenceProbeExec(t, p.Ctx, peer, "CREATE TEMP SEQUENCE actual_peer START 81 CACHE 1")
			var own, foreign string
			var pid int
			if err := c.QueryRowContext(p.Ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&own); err != nil {
				t.Fatal(err)
			}
			if err := peer.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&foreign); err != nil {
				t.Fatal(err)
			}
			if own == "" || foreign == "" || own == foreign || own == "pg_temp" || foreign == "pg_temp" {
				t.Fatal("actual namespaces", own, foreign)
			}
			var seedOID int64
			if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_temp.actual_seed'::regclass::oid::bigint").Scan(&seedOID); err != nil || seedOID <= 0 {
				t.Fatal(seedOID, err)
			}
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('actual_seed')", 31)
			seedLog := fixture.GenericRenameState(t, p, c, own+".actual_seed", 35, true, true)
			cases := []struct{ q, code, message string }{
				{"CREATE TEMP SEQUENCE " + foreign + ".invalid", "42P16", "cannot create relations in temporary schemas of other sessions"},
				{"CREATE SEQUENCE " + foreign + ".invalid", "42P16", "cannot create relations in temporary schemas of other sessions"},
				{"CREATE UNLOGGED SEQUENCE " + own + ".invalid", "42P16", "only temporary relations may be created in temporary schemas"},
				{"CREATE UNLOGGED SEQUENCE " + foreign + ".invalid", "42P16", "only temporary relations may be created in temporary schemas"},
				{"CREATE TEMP SEQUENCE IF NOT EXISTS " + foreign + ".actual_peer", "42P16", "cannot create relations in temporary schemas of other sessions"},
				{"CREATE SEQUENCE IF NOT EXISTS " + foreign + ".actual_peer", "42P16", "cannot create relations in temporary schemas of other sessions"},
				{"CREATE UNLOGGED SEQUENCE IF NOT EXISTS " + foreign + ".actual_peer", "42P16", "only temporary relations may be created in temporary schemas"},
				{"CREATE UNLOGGED SEQUENCE IF NOT EXISTS " + own + ".actual_seed", "42P16", "only temporary relations may be created in temporary schemas"},
				{"CREATE TEMP SEQUENCE IF NOT EXISTS public.actual_survivor", "42P16", "cannot create temporary relation in non-temporary schema"},
				{"CREATE TEMP SEQUENCE creation_missing.invalid", "3F000", `schema "creation_missing" does not exist`},
				{"CREATE UNLOGGED SEQUENCE creation_missing.invalid", "3F000", `schema "creation_missing" does not exist`},
				{"CREATE SEQUENCE creation_missing.invalid", "3F000", `schema "creation_missing" does not exist`},
				{"CREATE TEMP SEQUENCE public.invalid", "42P16", "cannot create temporary relation in non-temporary schema"},
			}
			for _, tc := range cases {
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				fixture.AssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, tc.q, prepared), &want)
				var after int64
				if err := c.QueryRowContext(p.Ctx, "SELECT 'pg_temp.actual_seed'::regclass::oid::bigint").Scan(&after); err != nil || after != seedOID {
					t.Fatal("seed identity", seedOID, after, err)
				}
				if after := fixture.GenericRenameState(t, p, c, own+".actual_seed", 35, true, true); after != seedLog {
					t.Fatal("seed state changed", seedLog, after)
				}
				fixture.GenericRenameState(t, p, peer, foreign+".actual_peer", 81, false, true)
				fixture.GenericRenameState(t, p, c, "public.actual_survivor", 71, false, true)
				fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT currval('actual_seed')", 31)
				fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT lastval()", 31)
			}
			for _, namespace := range []string{own, foreign, "public"} {
				var absent bool
				if err := c.QueryRowContext(p.Ctx, "SELECT to_regclass($1) IS NULL", namespace+".invalid").Scan(&absent); err != nil || !absent {
					t.Fatal("failed CREATE published object", namespace, absent, err)
				}
			}
			// Actual own TEMP accepts TEMP and adjusts ordinary logged requests.
			for _, tc := range []struct {
				q, name string
				start   int64
			}{
				{"CREATE TEMP SEQUENCE " + own + ".valid_temp START 51", "valid_temp", 51},
				{"CREATE SEQUENCE " + own + ".valid_logged START 61", "valid_logged", 61},
			} {
				if err := referenceGenericRenameUtility(t, p, c, tc.q, prepared); err != nil {
					t.Fatal(tc.q, err)
				}
				var persistence string
				if err := c.QueryRowContext(p.Ctx, "SELECT relpersistence::text FROM pg_class WHERE oid=$1::regclass", own+"."+tc.name).Scan(&persistence); err != nil || persistence != "t" {
					t.Fatal("adjusted persistence", persistence, err)
				}
				fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('"+own+"."+tc.name+"')", tc.start)
			}
			// Namespace CREATE authorization must precede persistence mismatch.
			exec("CREATE ROLE creation_denied_role")
			defer func() {
				if _, err := peer.ExecContext(p.Ctx, "DROP ROLE creation_denied_role"); err != nil {
					t.Error(err)
				}
			}()
			exec("SET ROLE creation_denied_role")
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema public")
			for _, q := range []string{"CREATE TEMP SEQUENCE public.denied", "CREATE TEMP SEQUENCE IF NOT EXISTS public.actual_survivor"} {
				fixture.AssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, q, prepared), &want)
			}
			exec("RESET ROLE")
			var absent bool
			if err := c.QueryRowContext(p.Ctx, "SELECT to_regclass('public.denied') IS NULL").Scan(&absent); err != nil || !absent {
				t.Fatal("denied CREATE published", absent, err)
			}
			fixture.SequenceProbeValue(t, p.Ctx, c, "SELECT nextval('actual_seed')", 32)
			p.Postgres.SetMaxIdleConns(0)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
			defer cancel()
			for {
				var live bool
				if err := peer.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1)", pid).Scan(&live); err != nil {
					t.Fatal("observing backend retirement", err)
				}
				if !live {
					break
				}
			}
			// PG retains namespace tuples for reuse; only exact owner objects retire.
			for _, name := range []string{"actual_seed", "valid_temp", "valid_logged"} {
				var removed bool
				if err := peer.QueryRowContext(p.Ctx, "SELECT to_regclass($1) IS NULL", own+"."+name).Scan(&removed); err != nil || !removed {
					t.Fatal("owner cleanup", name, removed, err)
				}
			}
			fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('actual_peer')", 81)
			fixture.SequenceProbeValue(t, p.Ctx, peer, "SELECT nextval('public.actual_survivor')", 71)
		})
	}
}
