package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"stuntgres"
	"testing"
)

func sequencePreparedSmoke(t *testing.T) {
	ctx := context.Background()
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name())
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	exec := func(q string) {
		t.Helper()
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	hold := func(q string) *sql.Stmt {
		t.Helper()
		s, err := c.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	value := func(s *sql.Stmt, want int64, args ...any) {
		t.Helper()
		var got int64
		if err := s.QueryRowContext(ctx, args...).Scan(&got); err != nil || got != want {
			t.Fatal(got, want, err)
		}
	}
	failure := func(s *sql.Stmt, want stuntgres.Error) {
		t.Helper()
		var v int64
		err := s.QueryRowContext(ctx).Scan(&v)
		var got *stuntgres.Error
		want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("got=%#v want=%#v err=%v", got, want, err)
		}
	}
	exec("CREATE SEQUENCE s START 14 CACHE 1")
	s := hold("SELECT nextval('s')")
	exec("ALTER SEQUENCE s RENAME TO identity")
	failure(s, stuntgres.Error{Code: "42P01", Message: `relation "s" does not exist`, Position: "16"})
	value(hold("SELECT nextval($1)"), 14, "identity")
	exec("CREATE SEQUENCE s START 90")
	curr := hold("SELECT currval('s')")
	failure(curr, stuntgres.Error{Code: "55000", Message: `currval of sequence "s" is not yet defined in this session`})
	value(s, 90)
	value(curr, 90)
	var identity string
	if err := c.QueryRowContext(ctx, "SELECT 'identity'::regclass").Scan(&identity); err != nil || identity != "identity" {
		t.Fatal(identity, err)
	}
	exec("ALTER SEQUENCE identity RENAME TO old_identity")
	value(hold("SELECT nextval($1)"), 15, "old_identity")
	exec("DROP SEQUENCE old_identity")
	exec("CREATE SEQUENCE identity START 500")
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 10", "CREATE SEQUENCE b.s START 100", "SET search_path TO a,b"} {
		exec(q)
	}
	path := hold("SELECT nextval('s')")
	value(path, 10)
	exec("SET search_path TO b,a")
	value(path, 100)
	exec("CREATE SEQUENCE a.callback START 10 CACHE 1")
	callback := hold("SELECT nextval('callback')")
	value(callback, 10)
	exec("CREATE SEQUENCE b.callback START 100 CACHE 1")
	value(callback, 11)
	exec("CREATE SCHEMA unrelated")
	value(callback, 100)
	var oldLast, newLast int64
	if err := c.QueryRowContext(ctx, "SELECT last_value FROM a.callback").Scan(&oldLast); err != nil || oldLast != 11 {
		t.Fatal("original sequence callback state", oldLast, err)
	}
	if err := c.QueryRowContext(ctx, "SELECT last_value FROM b.callback").Scan(&newLast); err != nil || newLast != 100 {
		t.Fatal("shadow sequence callback state", newLast, err)
	}
	exec("CREATE SEQUENCE a.peer_callback START 10 CACHE 1")
	peerCallback := hold("SELECT nextval('peer_callback')")
	value(peerCallback, 10)
	exec("CREATE SEQUENCE b.peer_callback START 100 CACHE 1")
	value(peerCallback, 11)
	peer, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	if _, err = peer.ExecContext(ctx, "CREATE TEMP SEQUENCE unrelated_temp"); err != nil {
		t.Fatal(err)
	}
	value(peerCallback, 100)
	value(hold("SELECT last_value FROM a.peer_callback"), 11)
	value(hold("SELECT last_value FROM b.peer_callback"), 100)
	exec("CREATE SEQUENCE a.existing_callback START 10 CACHE 1")
	existingCallback := hold("SELECT nextval('existing_callback')")
	value(existingCallback, 10)
	exec("CREATE SEQUENCE b.existing_callback START 100 CACHE 1")
	value(existingCallback, 11)
	if _, err = peer.ExecContext(ctx, "CREATE TEMP SEQUENCE another_temp"); err != nil {
		t.Fatal(err)
	}
	value(existingCallback, 12)
	value(hold("SELECT last_value FROM a.existing_callback"), 12)
	value(hold("SELECT last_value FROM b.existing_callback"), 100)
	exec("CREATE SEQUENCE a.end_callback START 10 CACHE 1")
	endCallback := hold("SELECT nextval('end_callback')")
	value(endCallback, 10)
	exec("CREATE SEQUENCE b.end_callback START 100 CACHE 1")
	value(endCallback, 11)
	db.SetMaxIdleConns(0) // Returning this borrower must physically end its session.
	if err = peer.Close(); err != nil {
		t.Fatal(err)
	}
	value(endCallback, 12)
	value(hold("SELECT last_value FROM a.end_callback"), 12)
	value(hold("SELECT last_value FROM b.end_callback"), 100)
	exec("CREATE TEMP SEQUENCE local_seq CACHE 10")
	temp := hold("SELECT nextval('pg_temp.local_seq')")
	remembered := hold("SELECT currval('pg_temp.local_seq')")
	value(temp, 1)
	persistent := hold("SELECT nextval('public.identity')")
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	value(remembered, 1)
	value(temp, 2)
	var v int64
	if err := persistent.QueryRowContext(ctx).Scan(&v); err == nil {
		t.Fatal("persistent prepared source escaped native replacement epoch fence")
	}
	if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	value(remembered, 2)
	value(temp, 3)
}
