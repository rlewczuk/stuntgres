package stuntgres_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func TestSequenceTempMove(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			diagnostic := func(err error, code, message, detail, position string) {
				want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail, Position: position}
				var got *stuntgres.Error
				if !errors.As(err, &got) || *got != want {
					t.Fatalf("diagnostic got %#v (%v), want %#v", got, err, want)
				}
			}
			sequenceTempMoveWitness(t, context.Background(), db, prepared, diagnostic)
		})
	}
}

// Rejected relocation must preserve both the temporary object's session state
// and unrelated retained source bindings; no sleep/deadline is used as proof.
func sequenceTempMoveWitness(t *testing.T, ctx context.Context, db *sql.DB, prepared bool, diagnostic func(error, string, string, string, string)) {
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
		var result sql.Result
		var err error
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("utility Prepare", q, e)
			}
			defer s.Close()
			result, err = s.ExecContext(ctx)
		} else {
			result, err = c.ExecContext(ctx, q)
		}
		if err == nil {
			if n, e := result.RowsAffected(); e != nil || n != 0 {
				t.Fatal(q, n, e)
			}
		}
		return err
	}
	for _, q := range []string{
		"CREATE SCHEMA temp_move_a", "CREATE SCHEMA temp_move_b", "CREATE SCHEMA temp_move_dst",
		"CREATE SEQUENCE public.movable START 1000 CACHE 1",
		"CREATE TEMP SEQUENCE movable START 31 CACHE 5",
		"CREATE TEMP SEQUENCE owner_only START 71 CACHE 1",
		"CREATE SEQUENCE temp_move_b.s START 10 CACHE 1",
		"CREATE SEQUENCE public.persistent_move START 51 CACHE 5",
		"CREATE TABLE public.link_table(id INT)",
		"CREATE SEQUENCE public.linked OWNED BY public.link_table.id",
		"SET search_path TO temp_move_a,temp_move_b,public",
	} {
		exec(c, q)
	}
	defer func() {
		for _, schema := range []string{"temp_move_a", "temp_move_b", "temp_move_dst"} {
			if _, err := peer.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
				t.Error(err)
			}
		}
	}()
	// Peer owns a different temporary namespace, independent of owner cleanup.
	exec(peer, "CREATE TEMP SEQUENCE peer_temp START 81")
	held, err := c.PrepareContext(ctx, "SELECT nextval('movable'),nextval('s')")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldValue := func(wantTemp, wantPersistent int64) {
		var a, b int64
		if err := held.QueryRowContext(ctx).Scan(&a, &b); err != nil || a != wantTemp || b != wantPersistent {
			t.Fatal("held group", a, b, err)
		}
	}
	heldValue(31, 10)
	exec(c, "CREATE SEQUENCE temp_move_a.s START 100 CACHE 1")
	// This new shadow must not be adopted by the held group after an error.
	state := func(name string, last int64, called bool) int64 {
		var got, log int64
		var isCalled bool
		if err := c.QueryRowContext(ctx, "SELECT last_value,log_cnt,is_called FROM "+name).Scan(&got, &log, &isCalled); err != nil || got != last || isCalled != called || log < 0 {
			t.Fatal(name, got, log, isCalled, err)
		}
		return log
	}
	log := state("pg_temp.movable", 35, true)
	// A retained row is an immutable snapshot, not a new lookup after relocation.
	rows, err := c.QueryContext(ctx, "SELECT last_value,is_called FROM pg_temp.movable")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, q := range []string{
		"ALTER SEQUENCE pg_temp.movable SET SCHEMA public",
		"ALTER SEQUENCE public.persistent_move SET SCHEMA pg_temp",
		"ALTER SEQUENCE pg_temp.movable SET SCHEMA pg_temp",
	} {
		diagnostic(utility(q), "0A000", "cannot move objects into or out of temporary schemas", "", "")
		if after := state("pg_temp.movable", 35, true); after != log {
			t.Fatal("rejection changed log_cnt", log, after)
		}
		state("public.persistent_move", 51, false)
		value(c, "SELECT currval('pg_temp.movable')", 31)
		value(c, "SELECT lastval()", 10)
		value(peer, "SELECT last_value FROM public.movable", 1000)
	}
	// Linked restriction precedes destination lookup and temporary endpoint checks.
	for _, destination := range []string{"temp_move_missing", "pg_temp"} {
		diagnostic(utility("ALTER SEQUENCE public.linked SET SCHEMA "+destination), "0A000", "cannot move an owned sequence into another schema", `Sequence "linked" is linked to table "link_table".`, "")
	}
	diagnostic(utility("ALTER SEQUENCE pg_temp.movable SET SCHEMA temp_move_missing"), "3F000", `schema "temp_move_missing" does not exist`, "", "")
	heldValue(32, 11) // original temporary cache tail and original persistent source
	state("pg_temp.movable", 35, true)
	state("temp_move_a.s", 100, false)
	state("temp_move_b.s", 11, true)
	value(c, "SELECT nextval('s')", 100) // fresh binding sees the shadow
	if !rows.Next() {
		t.Fatal("missing retained row", rows.Err())
	}
	var frozen int64
	var called bool
	if err := rows.Scan(&frozen, &called); err != nil || frozen != 35 || !called {
		t.Fatal(frozen, called, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained row count", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	// Ordinary persistent relocation remains legal and preserves its reservation.
	value(c, "SELECT nextval('public.persistent_move')", 51)
	if err := utility("ALTER SEQUENCE public.persistent_move SET SCHEMA temp_move_dst"); err != nil {
		t.Fatal(err)
	}
	state("temp_move_dst.persistent_move", 55, true)
	value(c, "SELECT currval('temp_move_dst.persistent_move')", 51)
	value(c, "SELECT nextval('temp_move_dst.persistent_move')", 52)
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	// Close the actual owner driver connection, not just a pool borrower.
	db.SetMaxIdleConns(0)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	value(peer, "SELECT nextval('public.movable')", 1000)
	value(peer, "SELECT nextval('peer_temp')", 81)
	var missing int64
	err = peer.QueryRowContext(ctx, "SELECT last_value FROM public.owner_only").Scan(&missing)
	// Exact relation-analysis Position belongs to this SELECT, not the utility.
	diagnostic(err, "42P01", `relation "public.owner_only" does not exist`, "", "24")
}
