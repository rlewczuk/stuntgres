//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestReferenceSequenceTempCreation(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, initialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+map[bool]string{false: "Uninitialized", true: "Initialized"}[initialized], func(t *testing.T) {
				p := newReferencePair(t)
				for _, backend := range []struct {
					name string
					db   *sql.DB
				}{{"native", p.memory}, {"postgres", p.postgres}} {
					t.Run(backend.name, func(t *testing.T) {
						diagnostic := func(err error, code, message, position string) {
							want := controlsReferenceDiagnostic(code, message)
							want.Position = position
							referenceAssertDiagnostic(t, err, &want)
						}
						sequenceTempCreationWitness(t, p.ctx, backend.db, prepared, initialized, diagnostic)
					})
				}
			})
		}
	}
}

// Failed CREATE must not publish a temporary namespace or invalidate a held
// literal group. Actual native namespace identity is checked by engine tests.
func sequenceTempCreationWitness(t *testing.T, ctx context.Context, db *sql.DB, prepared, initialized bool, diagnostic func(error, string, string, string)) {
	t.Helper()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	exec := func(conn *sql.Conn, q string) {
		r, err := conn.ExecContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		if n, err := r.RowsAffected(); err != nil || n != 0 {
			t.Fatal(q, n, err)
		}
	}
	value := func(conn *sql.Conn, q string, want int64) {
		var got int64
		if err := conn.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatal(q, got, want, err)
		}
	}
	utility := func(q string) error {
		var r sql.Result
		var err error
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("utility Prepare must succeed before Execute", q, e)
			}
			defer s.Close()
			r, err = s.ExecContext(ctx)
		} else {
			r, err = c.ExecContext(ctx, q)
		}
		if err == nil {
			if n, e := r.RowsAffected(); e != nil || n != 0 {
				t.Fatal(q, n, e)
			}
		}
		return err
	}
	for _, q := range []string{
		"CREATE SCHEMA creation_a", "CREATE SCHEMA creation_b",
		"CREATE SEQUENCE creation_b.s START 10 CACHE 1",
		"CREATE SEQUENCE public.remembered START 31 CACHE 5",
		"CREATE SEQUENCE public.survivor START 71 CACHE 1",
	} {
		exec(c, q)
	}
	defer func() {
		for _, schema := range []string{"creation_a", "creation_b"} {
			if _, err := peer.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	}()
	if initialized {
		exec(c, "CREATE TEMP SEQUENCE seed START 41 CACHE 5")
	}
	exec(peer, "CREATE TEMP SEQUENCE peer_seed START 81")
	exec(peer, "SET search_path TO creation_a,creation_b,public")
	held, err := peer.PrepareContext(ctx, "SELECT nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldValue := func(want int64) {
		var got int64
		if err := held.QueryRowContext(ctx).Scan(&got); err != nil || got != want {
			t.Fatal("held source", got, want, err)
		}
	}
	heldValue(10)
	exec(c, "CREATE SEQUENCE creation_a.s START 100 CACHE 1")
	value(c, "SELECT nextval('public.remembered')", 31)
	state := func(name string, want int64, called bool) int64 {
		var last, log int64
		var actual bool
		if err := c.QueryRowContext(ctx, "SELECT last_value,log_cnt,is_called FROM "+name).Scan(&last, &log, &actual); err != nil || last != want || actual != called || log < 0 {
			t.Fatal(name, last, log, actual, err)
		}
		return log
	}
	rememberedLog := state("public.remembered", 35, true)
	var seedLog int64
	if initialized {
		value(c, "SELECT nextval('seed')", 41)
		seedLog = state("pg_temp.seed", 45, true)
	}
	cases := []struct{ q, code, message string }{
		{"CREATE UNLOGGED SEQUENCE pg_temp.invalid", "42P16", "only temporary relations may be created in temporary schemas"},
		{"CREATE TEMP SEQUENCE creation_missing.invalid", "3F000", `schema "creation_missing" does not exist`},
		{"CREATE TEMP SEQUENCE public.invalid", "42P16", "cannot create temporary relation in non-temporary schema"},
		{"CREATE TEMP SEQUENCE IF NOT EXISTS public.survivor", "42P16", "cannot create temporary relation in non-temporary schema"},
	}
	if initialized {
		cases = append(cases, struct{ q, code, message string }{"CREATE UNLOGGED SEQUENCE IF NOT EXISTS pg_temp.seed", "42P16", "only temporary relations may be created in temporary schemas"})
	}
	for i, tc := range cases {
		diagnostic(utility(tc.q), tc.code, tc.message, "")
		if after := state("public.remembered", 35, true); after != rememberedLog {
			t.Fatal("failed CREATE changed generator", rememberedLog, after)
		}
		value(c, "SELECT currval('public.remembered')", 31)
		value(c, "SELECT lastval()", map[bool]int64{false: 31, true: 41}[initialized])
		if initialized {
			if after := state("pg_temp.seed", 45, true); after != seedLog {
				t.Fatal("failed CREATE changed seed", seedLog, after)
			}
			value(c, "SELECT currval('seed')", 41)
		}
		heldValue(11 + int64(i))
		state("creation_a.s", 100, false)
		state("public.survivor", 71, false)
	}
	// Ordinary logged creation adjusts to TEMP; explicit TEMP alias also works.
	for _, q := range []string{"CREATE SEQUENCE pg_temp.valid_logged START 51", "CREATE TEMP SEQUENCE pg_temp.valid_temp START 61"} {
		if err := utility(q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, name := range []string{"pg_temp.invalid", "public.invalid"} {
		var ignored int64
		err := c.QueryRowContext(ctx, "SELECT last_value FROM "+name).Scan(&ignored)
		diagnostic(err, "42P01", `relation "`+name+`" does not exist`, "24")
	}
	value(c, "SELECT nextval('public.remembered')", 32)
	if initialized {
		value(c, "SELECT nextval('seed')", 42)
	}
	value(c, "SELECT nextval('pg_temp.valid_logged')", 51)
	value(c, "SELECT nextval('pg_temp.valid_temp')", 61)
	// Returning a borrower is not enough: force the owner physical connection end.
	db.SetMaxIdleConns(0)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	value(peer, "SELECT nextval('peer_seed')", 81)
	value(peer, "SELECT nextval('public.survivor')", 71)
	value(peer, "SELECT nextval('public.remembered')", 36)
	for _, name := range []string{"valid_logged", "valid_temp"} {
		var ignored int64
		err := peer.QueryRowContext(ctx, "SELECT last_value FROM "+name).Scan(&ignored)
		diagnostic(err, "42P01", `relation "`+name+`" does not exist`, "24")
	}
}

// PostgreSQL alone exposes actual namespace names through public catalogs.
// Native actual-identity coverage belongs to the engine authority fixture.
func TestReferenceSequenceTempCreationActualNamespace(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferencePair(t)
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
			exec := func(q string) { sequenceProbeExec(t, p.ctx, c, q) }
			exec("CREATE TEMP SEQUENCE actual_seed START 31 CACHE 5")
			exec("CREATE SEQUENCE public.actual_survivor START 71 CACHE 1")
			sequenceProbeExec(t, p.ctx, peer, "CREATE TEMP SEQUENCE actual_peer START 81 CACHE 1")
			var own, foreign string
			var pid int
			if err := c.QueryRowContext(p.ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
				t.Fatal(err)
			}
			if err := c.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&own); err != nil {
				t.Fatal(err)
			}
			if err := peer.QueryRowContext(p.ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&foreign); err != nil {
				t.Fatal(err)
			}
			if own == "" || foreign == "" || own == foreign || own == "pg_temp" || foreign == "pg_temp" {
				t.Fatal("actual namespaces", own, foreign)
			}
			var seedOID int64
			if err := c.QueryRowContext(p.ctx, "SELECT 'pg_temp.actual_seed'::regclass::oid::bigint").Scan(&seedOID); err != nil || seedOID <= 0 {
				t.Fatal(seedOID, err)
			}
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('actual_seed')", 31)
			seedLog := referenceGenericRenameState(t, p, c, own+".actual_seed", 35, true)
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
				want := controlsReferenceDiagnostic(tc.code, tc.message)
				referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, tc.q, prepared), &want)
				var after int64
				if err := c.QueryRowContext(p.ctx, "SELECT 'pg_temp.actual_seed'::regclass::oid::bigint").Scan(&after); err != nil || after != seedOID {
					t.Fatal("seed identity", seedOID, after, err)
				}
				if after := referenceGenericRenameState(t, p, c, own+".actual_seed", 35, true); after != seedLog {
					t.Fatal("seed state changed", seedLog, after)
				}
				referenceGenericRenameState(t, p, peer, foreign+".actual_peer", 81, false)
				referenceGenericRenameState(t, p, c, "public.actual_survivor", 71, false)
				sequenceProbeValue(t, p.ctx, c, "SELECT currval('actual_seed')", 31)
				sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 31)
			}
			for _, namespace := range []string{own, foreign, "public"} {
				var absent bool
				if err := c.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", namespace+".invalid").Scan(&absent); err != nil || !absent {
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
				if err := c.QueryRowContext(p.ctx, "SELECT relpersistence::text FROM pg_class WHERE oid=$1::regclass", own+"."+tc.name).Scan(&persistence); err != nil || persistence != "t" {
					t.Fatal("adjusted persistence", persistence, err)
				}
				sequenceProbeValue(t, p.ctx, c, "SELECT nextval('"+own+"."+tc.name+"')", tc.start)
			}
			// Namespace CREATE authorization must precede persistence mismatch.
			exec("CREATE ROLE creation_denied_role")
			defer func() {
				if _, err := peer.ExecContext(p.ctx, "DROP ROLE creation_denied_role"); err != nil {
					t.Error(err)
				}
			}()
			exec("SET ROLE creation_denied_role")
			want := controlsReferenceDiagnostic("42501", "permission denied for schema public")
			for _, q := range []string{"CREATE TEMP SEQUENCE public.denied", "CREATE TEMP SEQUENCE IF NOT EXISTS public.actual_survivor"} {
				referenceAssertDiagnostic(t, referenceGenericRenameUtility(t, p, c, q, prepared), &want)
			}
			exec("RESET ROLE")
			var absent bool
			if err := c.QueryRowContext(p.ctx, "SELECT to_regclass('public.denied') IS NULL").Scan(&absent); err != nil || !absent {
				t.Fatal("denied CREATE published", absent, err)
			}
			sequenceProbeValue(t, p.ctx, c, "SELECT nextval('actual_seed')", 32)
			p.postgres.SetMaxIdleConns(0)
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(p.ctx, 2*time.Second)
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
				if err := peer.QueryRowContext(p.ctx, "SELECT to_regclass($1) IS NULL", own+"."+name).Scan(&removed); err != nil || !removed {
					t.Fatal("owner cleanup", name, removed, err)
				}
			}
			sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('actual_peer')", 81)
			sequenceProbeValue(t, p.ctx, peer, "SELECT nextval('public.actual_survivor')", 71)
		})
	}
}
