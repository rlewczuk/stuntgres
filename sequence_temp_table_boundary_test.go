package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"testing"
)

func TestSequenceTemporaryTableBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
			if err != nil {
				t.Fatal(err)
			}
			db := sql.OpenDB(connector)
			defer db.Close()
			sequenceTempTableBoundaryWitness(t, context.Background(), db, prepared)
		})
	}
}

func sequenceTempTableBoundaryWitness(t *testing.T, ctx context.Context, db *sql.DB, prepared bool) {
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
	exec := func(conn *sql.Conn, q string, want int64) {
		r, e := conn.ExecContext(ctx, q)
		if e != nil {
			t.Fatal(q, e)
		}
		if n, e := r.RowsAffected(); e != nil || n != want {
			t.Fatal(q, n, e)
		}
	}
	value := func(conn *sql.Conn, q string, want int64) {
		var n int64
		if e := conn.QueryRowContext(ctx, q).Scan(&n); e != nil || n != want {
			t.Fatal(q, n, e)
		}
	}
	diagnostic := func(err error, code, message, position string) {
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got %#v (%v), want %#v", got, err, want)
		}
	}
	exec(c, "CREATE TABLE public.persistent(v INT)", 0)
	exec(c, "INSERT INTO public.persistent VALUES(7)", 1)
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	exec(c, "CREATE TEMP SEQUENCE seed START 31 CACHE 5", 0)
	exec(peer, "CREATE TEMP SEQUENCE peer_seed START 81 CACHE 5", 0)
	exec(c, "SET search_path TO pg_temp", 0)
	exec(peer, "SET search_path TO pg_temp", 0)
	actual := func(conn *sql.Conn) string {
		var name string
		if e := conn.QueryRowContext(ctx, "SELECT current_schema").Scan(&name); e != nil || !strings.HasPrefix(name, "pg_temp_") {
			t.Fatal(name, e)
		}
		return name
	}
	own, foreign := actual(c), actual(peer)
	if own == foreign {
		t.Fatal("shared temporary namespace")
	}
	held, err := c.PrepareContext(ctx, "SELECT nextval('seed'),currval('seed'),lastval()")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	heldValue := func(want int64) {
		rows, e := held.QueryContext(ctx)
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		types, e := rows.ColumnTypes()
		if e != nil || len(types) != 3 {
			t.Fatal(types, e)
		}
		for _, typ := range types {
			if typ.DatabaseTypeName() != "INT8" {
				t.Fatal(typ)
			}
		}
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		var a, b, c int64
		if e = rows.Scan(&a, &b, &c); e != nil || a != want || b != want || c != want {
			t.Fatal(a, b, c, e)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal("extra rows", rows.Err())
		}
	}
	heldValue(31)
	value(peer, "SELECT nextval('peer_seed')", 81)
	state := func(conn *sql.Conn, name string, last int64) {
		var got, log int64
		var called bool
		if e := conn.QueryRowContext(ctx, "SELECT last_value,log_cnt,is_called FROM "+name).Scan(&got, &log, &called); e != nil || got != last || log != 0 || !called {
			t.Fatal(name, got, log, called, e)
		}
	}
	for _, q := range []string{"CREATE TABLE forbidden(v INT)", "CREATE TABLE " + own + ".forbidden(v INT)", "CREATE TABLE " + foreign + ".forbidden(v INT)", "CREATE TABLE IF NOT EXISTS seed(v INT)", "CREATE TABLE IF NOT EXISTS " + own + ".seed(v INT)", "CREATE TABLE IF NOT EXISTS " + foreign + ".peer_seed(v INT)", "CREATE TABLE pg_temp.forbidden(v INT)"} {
		var err error
		position := ""
		if q == "CREATE TABLE pg_temp.forbidden(v INT)" {
			position = "14"
		}
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal("utility Prepare", q, e)
			}
			for range 2 {
				_, err = s.ExecContext(ctx)
				diagnostic(err, "0A000", "temporary schema creation is unsupported", position)
			}
			s.Close()
		} else {
			_, err = c.ExecContext(ctx, q)
			diagnostic(err, "0A000", "temporary schema creation is unsupported", position)
		}
		state(c, "seed", 35)
		state(peer, "peer_seed", 85)
		value(c, "SELECT currval('seed')", 31)
		value(c, "SELECT lastval()", 31)
	}
	// Each live TEMP namespace must be inspected by its actual physical owner.
	for _, probe := range []struct {
		conn *sql.Conn
		name string
	}{{c, own + ".forbidden"}, {peer, foreign + ".forbidden"}} {
		var n int64
		err := probe.conn.QueryRowContext(ctx, "SELECT v FROM "+probe.name).Scan(&n)
		diagnostic(err, "42P01", `relation "`+probe.name+`" does not exist`, "15")
	}
	exec(c, "CREATE TABLE public.allowed(v INT)", 0)
	exec(c, "INSERT INTO public.allowed VALUES(7)", 1)
	value(c, "SELECT v FROM public.allowed", 7)
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	heldValue(32)
	value(c, "SELECT currval('seed')", 32)
	value(c, "SELECT lastval()", 32)
	state(c, "seed", 35)
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	heldValue(33)
	state(c, "seed", 35)
	value(c, "SELECT v FROM public.persistent", 7)
	value(peer, "SELECT nextval('peer_seed')", 82)
	held.Close()
	db.SetMaxIdleConns(0)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	var n int64
	err = peer.QueryRowContext(ctx, "SELECT last_value FROM "+own+".seed").Scan(&n)
	diagnostic(err, "42P01", `relation "`+own+`.seed" does not exist`, "24")
	value(peer, "SELECT nextval('peer_seed')", 83)
	value(peer, "SELECT v FROM public.persistent", 7)
}
