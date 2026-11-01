package reference_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSSLInfoNestedDefault(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// pgcrypto is an existing installed consumer, not another sslinfo member.
		if b.PG {
			clean := func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := c.ExecContext(ctx, "DROP EXTENSION IF EXISTS pgcrypto CASCADE"); err != nil { t.Error(err) }
			}
			clean()
			defer clean()
		}
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA ssl_part", 0))
		const expression = "ssl_part.digest(CASE WHEN ssl_part.ssl_is_used() THEN 'tls' ELSE 'plain' END,'sha256')"
		// Actual non-TLS fixes the input independently. Standard SHA-256 supplies
		// expected bytes, never SQL/native output or backend parity.
		digest := sha256.Sum256([]byte("plain"))
		want := string(digest[:])
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT "+expression+" AS payload", []string{"payload"}, []string{"BYTEA"}, [][]any{{want}})
			s.Prepared = prepared
			run(s)
		}
		for _, route := range []struct {
			name string
			prepared bool
		}{{"ssl_mixed_direct", false}, {"ssl_mixed_prepared", true}} {
			create := fixture.Exec("CREATE TABLE "+route.name+"(id integer PRIMARY KEY,payload bytea DEFAULT "+expression+")", 0)
			create.Prepared = route.prepared
			run(create)
			insert := fixture.Exec("INSERT INTO "+route.name+"(id) VALUES(1),(2)", 2)
			insert.Prepared = route.prepared
			run(insert)
			run(fixture.Exec("INSERT INTO "+route.name+" VALUES(3,NULL)", 1))
		}
		run(fixture.Exec("CREATE VIEW ssl_mixed_view AS SELECT "+expression+" AS payload", 0))
		check := func(target *sql.Conn, recorder *fixture.Recorder, extra ...int64) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				view := fixture.Select("SELECT payload FROM ssl_mixed_view", []string{"payload"}, []string{"BYTEA"}, [][]any{{want}})
				view.Prepared = prepared
				fixture.RunStep(t, p.Ctx, target, recorder, view)
				for _, table := range []string{"ssl_mixed_direct", "ssl_mixed_prepared"} {
					rows := [][]any{{int64(1), want}, {int64(2), want}, {int64(3), nil}}
					for _, id := range extra { rows = append(rows, []any{id, want}) }
					s := fixture.Select("SELECT id,payload FROM "+table+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "BYTEA"}, rows)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, target, recorder, s)
				}
			}
		}
		check(c, b.Recorder)
		// This held statement demands the default on every execution; it does
		// not merely reread an already stored digest after catalog replacement.
		const query = "INSERT INTO ssl_mixed_direct(id) VALUES($1) RETURNING payload"
		held, err := c.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		defer held.Close()
		h := fixture.HeldStatement{Executor: c, Statement: held, SQL: query}
		heldInsert := func(id int64) {
			t.Helper()
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(query, []string{"payload"}, []string{"BYTEA"}, [][]any{{want}}, id))
		}
		heldInsert(4)
		run(fixture.Prepared(fixture.Exec("INSERT INTO ssl_mixed_prepared(id) VALUES($1)", 1, int64(4))))
		check(c, b.Recorder, 4)
		if b.PG { return }
		// Both providers precede the stored mixed default. Dump serializes its
		// symbolic recipe; only fresh inserts execute it after replacement.
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if strings.Count(dump, "CREATE EXTENSION") < 2 || strings.Index(dump, "CREATE TABLE") <= strings.LastIndex(dump, "CREATE EXTENSION") { t.Fatal("mixed default precedes installed providers", dump) }
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		check(c, b.Recorder, 4)
		heldInsert(5)
		run(fixture.Prepared(fixture.Exec("INSERT INTO ssl_mixed_prepared(id) VALUES($1)", 1, int64(5))))
		check(c, b.Recorder, 4, 5)
		other, target, recorder := pgcryptoControlDestination(t, p, "sslinfo_nested_destination")
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		check(target, recorder, 4)
		for _, route := range []struct {
			name string
			prepared bool
		}{{"ssl_mixed_direct", false}, {"ssl_mixed_prepared", true}} {
			insert := fixture.Exec("INSERT INTO "+route.name+"(id) VALUES($1)", 1, int64(5))
			insert.Prepared = route.prepared
			fixture.RunStep(t, p.Ctx, target, recorder, insert)
		}
		check(target, recorder, 4, 5)
	})
}
