package smoke_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestSequenceTempCreationSmoke(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, initialized := range []bool{false, true} {
			t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared]+"/"+map[bool]string{false: "Uninitialized", true: "Initialized"}[initialized], func(t *testing.T) {
				connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
				if err != nil {
					t.Fatal(err)
				}
				db := sql.OpenDB(connector)
				defer db.Close()
				diagnostic := func(err error, code, message, position string) {
					want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
					var got *stuntgres.Error
					if !errors.As(err, &got) || *got != want {
						t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
					}
				}
				sequenceTempCreationWitness(t, context.Background(), db, prepared, initialized, diagnostic)
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
