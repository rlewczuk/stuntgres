package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func pgcryptoSHAConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
	run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
	run(fixture.Exec("CREATE TABLE pgcrypto_sha_stored(id integer,p text DEFAULT 'Hello world!',s text DEFAULT pgcrypto_part.gen_salt('sha256crypt',1000),h text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,s)) STORED,d text DEFAULT pgcrypto_part.crypt('Hello world!','$5$saltstring'),x text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,'$6$saltstring')) STORED,CHECK(pgcrypto_part.crypt(p,'$5$saltstring')='"+pgcryptoSHA256Hello+"'))", 0))
	run(fixture.Exec("CREATE INDEX pgcrypto_sha_idx ON pgcrypto_sha_stored((pgcrypto_part.crypt(p,'$6$saltstring')))", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_sha_view AS SELECT id,d,x,h=pgcrypto_part.crypt(p,s) AS valid,pgcrypto_part.crypt('wrong',h)=h AS wrong,pgcrypto_part.gen_salt('sha512crypt',1000) AS fresh FROM pgcrypto_sha_stored", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_sha_nested AS SELECT id,d,x,valid,wrong FROM pgcrypto_sha_view", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_sha_stored(id) VALUES(1),(2)", 2))
	check := fixture.Select("SELECT id,d,x,valid,wrong FROM pgcrypto_sha_nested ORDER BY id", []string{"id", "d", "x", "valid", "wrong"}, []string{"INT4", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), pgcryptoSHA256Hello, pgcryptoSHA512Hello, true, false}, {int64(2), pgcryptoSHA256Hello, pgcryptoSHA512Hello, true, false}})
	run(check)
	run(fixture.Prepared(check))
	var salt, hash, first, second string
	if err := c.QueryRowContext(p.Ctx, "SELECT s,h FROM pgcrypto_sha_stored WHERE id=1").Scan(&salt, &hash); err != nil {
		t.Fatal(err)
	}
	pgcryptoSHASaltShape(t, salt, "5", 1000)
	for _, dest := range []*string{&first, &second} {
		if err := c.QueryRowContext(p.Ctx, "SELECT fresh FROM pgcrypto_sha_view WHERE id=1").Scan(dest); err != nil {
			t.Fatal(err)
		}
		pgcryptoSHASaltShape(t, *dest, "6", 1000)
	}
	if first == second {
		t.Fatal("volatile SHA salt reused")
	}
	run(fixture.ImplicitError("CREATE TABLE pgcrypto_sha_volatile(v text GENERATED ALWAYS AS(pgcrypto_part.gen_salt('sha256crypt')) STORED)", fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
	run(fixture.ImplicitError("CREATE INDEX pgcrypto_sha_volatile_idx ON pgcrypto_sha_stored((pgcrypto_part.gen_salt('sha512crypt')))", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}))
	if pg {
		return
	}
	q := "SELECT pgcrypto_part.crypt($1,$2) AS value"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	old, err := c.QueryContext(p.Ctx, "SELECT pgcrypto_part.crypt('Hello world!','$6$saltstring') AS value")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if !old.Next() {
		t.Fatal("old SHA row lost", old.Err())
	}
	var value string
	if err := old.Scan(&value); err != nil || value != pgcryptoSHA512Hello {
		t.Fatal(value, err)
	}
	if old.Next() || old.Err() != nil || metadata[0].DatabaseTypeName() != "TEXT" {
		t.Fatal("old SHA metadata/tail", old.Err())
	}
	old.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	post := func(conn *sql.Conn, recorder *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, conn, recorder, check)
		fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Select("SELECT s,h FROM pgcrypto_sha_stored WHERE id=1", []string{"s", "h"}, []string{"TEXT", "TEXT"}, [][]any{{salt, hash}}))
		pgcryptoPasswordText(t, p, conn, recorder, q, pgcryptoSHA512Hello, "Hello world!", "$6$saltstring")
	}
	post(c, r)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{pgcryptoSHA256Hello}}, "Hello world!", "$5$saltstring"))
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"\nINVALID SQL;")); err == nil {
		t.Fatal("invalid SHA replay accepted")
	}
	if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("failed SHA replay mutated state")
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("cancelled SHA replay", err)
	}
	other, target, record := pgcryptoControlDestination(t, p, "pgcrypto_sha_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	post(target, record)
	for _, dest := range []struct {
		c *sql.Conn
		r *fixture.Recorder
	}{{c, r}, {target, record}} {
		fixture.RunStep(t, p.Ctx, dest.c, dest.r, fixture.Exec("INSERT INTO pgcrypto_sha_stored(id) VALUES(3)", 1))
		fixture.RunStep(t, p.Ctx, dest.c, dest.r, fixture.Select("SELECT d,x,h=pgcrypto_part.crypt(p,s) AS valid FROM pgcrypto_sha_stored WHERE id=3", []string{"d", "x", "valid"}, []string{"TEXT", "TEXT", "BOOL"}, [][]any{{pgcryptoSHA256Hello, pgcryptoSHA512Hello, true}}))
	}
}

func TestReferencePgcryptoPasswordSHAConsumersAndControls(t *testing.T) {
	pgcryptoCase(t, false, pgcryptoSHAConsumers)
}

func TestReferencePgcryptoPasswordSHAFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoSHAConsumers(t, p, c, r, pg)
		for _, v := range []struct{ setting, hash string }{{"$5$saltstring", pgcryptoSHA256Hello}, {"$6$saltstring", pgcryptoSHA512Hello}} {
			pgcryptoPasswordText(t, p, c, r, "SELECT pgcrypto_part.crypt($1,$2) AS value", v.hash, "Hello world!", v.setting)
		}
		pgcryptoPasswordText(t, p, c, r, "SELECT pgcrypto_part.crypt(NULL::text,'$5$rounds=oops$!') AS value", nil)
		for _, name := range []string{"sha256crypt", "sha512crypt"} {
			var salt string
			if err := c.QueryRowContext(p.Ctx, "SELECT pgcrypto_part.gen_salt($1,999999999)", name).Scan(&salt); err != nil {
				t.Fatal(err)
			}
			id := "5"
			if name == "sha512crypt" {
				id = "6"
			}
			pgcryptoSHASaltShape(t, salt, id, 999999999)
			for _, count := range []int{0, 1000} {
				if err := c.QueryRowContext(p.Ctx, "SELECT pgcrypto_part.gen_salt($1,$2)", name, count).Scan(&salt); err != nil {
					t.Fatal(err)
				}
				rounds := count
				if rounds == 0 {
					rounds = 5000
				}
				pgcryptoSHASaltShape(t, salt, id, rounds)
				s := fixture.Select("SELECT pgcrypto_part.crypt('Hello world!',pgcrypto_part.crypt('Hello world!',$1))=pgcrypto_part.crypt('Hello world!',$1) AS correct,pgcrypto_part.crypt('wrong',pgcrypto_part.crypt('Hello world!',$1))=pgcrypto_part.crypt('Hello world!',$1) AS wrong", []string{"correct", "wrong"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}, salt)
				fixture.RunStep(t, p.Ctx, c, r, s)
			}
		}
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, pg, &notices)
		pgcryptoPasswordText(t, p, nc, nr, "SELECT pgcrypto_part.crypt($1,$2) AS value", "$5$rounds=1000$roundstoolow$yfvwcWrQ8l/K0DAWyuPMDNHpIVlTQebY9l/gL972bIC", "the minimum number is still observed", "$5$rounds=10$roundstoolow")
		if len(notices) != 2 {
			t.Fatalf("direct/prepared clamp notices=%#v", notices)
		}
		for _, notice := range notices {
			if notice != pgcryptoSHAClamp("10", false) {
				t.Errorf("clamp notice=%#v", notice)
			}
		}
	})
}
