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

func pgcryptoPasswordConsumers(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
	run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
	runNew := func(conn *sql.Conn, record *fixture.Recorder, s fixture.Step) {
		fixture.RunStep(t, p.Ctx, conn, record, s)
	}
	run(fixture.Exec("CREATE TABLE pgcrypto_password_new(id integer,p text DEFAULT 'foox',s text DEFAULT pgcrypto_part.gen_salt('md5'),h text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,s)) STORED,d text DEFAULT pgcrypto_part.crypt('foox','NB'),x text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,'_J9..j2zz')) STORED,m text GENERATED ALWAYS AS(pgcrypto_part.crypt(p,'$1$Szzz0yzz')) STORED,CHECK(pgcrypto_part.crypt(p,'NB')='NB53EGGqrrb5E'))", 0))
	run(fixture.Exec("CREATE INDEX pgcrypto_password_new_idx ON pgcrypto_password_new((pgcrypto_part.crypt(p,'_J9..j2zz')))", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_password_new_view AS SELECT id,d,x,m,h=pgcrypto_part.crypt(p,s) AS valid,pgcrypto_part.crypt('wrong',h)=h AS wrong FROM pgcrypto_password_new", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_password_new_nested AS SELECT id,d,x,m,valid,wrong FROM pgcrypto_password_new_view", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_password_new(id) VALUES(1),(2)", 2))
	newCheck := fixture.Select("SELECT id,d,x,m,valid,wrong FROM pgcrypto_password_new_nested ORDER BY id", []string{"id", "d", "x", "m", "valid", "wrong"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{
		{int64(1), "NB53EGGqrrb5E", "_J9..j2zzAYKMvO2BYRY", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1", true, false},
		{int64(2), "NB53EGGqrrb5E", "_J9..j2zzAYKMvO2BYRY", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1", true, false},
	})
	run(newCheck)
	run(fixture.Prepared(newCheck))
	var newSalt, newHash string
	if err := c.QueryRowContext(p.Ctx, "SELECT s,h FROM pgcrypto_password_new WHERE id=1").Scan(&newSalt, &newHash); err != nil {
		t.Fatal(err)
	}
	pgcryptoPasswordAlgorithmSaltShape(t, newSalt, "md5", 1000)
	newStored := fixture.Select("SELECT s,h FROM pgcrypto_password_new WHERE id=1", []string{"s", "h"}, []string{"TEXT", "TEXT"}, [][]any{{newSalt, newHash}})
	run(fixture.Exec("CREATE TABLE pgcrypto_password_stored(id integer,p text DEFAULT 'foox',s text DEFAULT pgcrypto_part.gen_salt('bf',4),h text GENERATED ALWAYS AS (pgcrypto_part.crypt(p,s)) STORED,d text DEFAULT pgcrypto_part.crypt('foox','"+pgcryptoPasswordSalt+"'),CONSTRAINT pgcrypto_password_valid CHECK(p IS NULL OR pgcrypto_part.crypt(p,'"+pgcryptoPasswordSalt+"')='"+pgcryptoPasswordFoox+"'))", 0))
	run(fixture.Exec("CREATE INDEX pgcrypto_password_idx ON pgcrypto_password_stored ((pgcrypto_part.crypt(p,'"+pgcryptoPasswordSalt+"')))", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_password_view AS SELECT id,h,pgcrypto_part.crypt(p,s) AS verified,pgcrypto_part.gen_salt('bf',4) AS fresh FROM pgcrypto_password_stored", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_password_nested AS SELECT id,(SELECT pgcrypto_part.crypt('foox','"+pgcryptoPasswordSalt+"')) AS value FROM pgcrypto_password_view", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_password_stored(id) VALUES(1),(2)", 2))
	check := fixture.Select("SELECT id,d,h=pgcrypto_part.crypt(p,s) AS valid FROM pgcrypto_password_stored ORDER BY id", []string{"id", "d", "valid"}, []string{"INT4", "TEXT", "BOOL"}, [][]any{{int64(1), pgcryptoPasswordFoox, true}, {int64(2), pgcryptoPasswordFoox, true}})
	run(check)
	run(fixture.Prepared(check))
	run(fixture.Select("SELECT id,value FROM pgcrypto_password_nested ORDER BY id", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), pgcryptoPasswordFoox}, {int64(2), pgcryptoPasswordFoox}}))
	var salt, hash, first, second string
	if err := c.QueryRowContext(p.Ctx, "SELECT s,h FROM pgcrypto_password_stored WHERE id=1").Scan(&salt, &hash); err != nil {
		t.Fatal(err)
	}
	pgcryptoPasswordSaltShape(t, salt, 4)
	for _, dst := range []*string{&first, &second} {
		if err := c.QueryRowContext(p.Ctx, "SELECT fresh FROM pgcrypto_password_view WHERE id=1").Scan(dst); err != nil {
			t.Fatal(err)
		}
		pgcryptoPasswordSaltShape(t, *dst, 4)
	}
	if first == second {
		t.Fatal("view cached volatile salt")
	}
	run(fixture.ImplicitError("CREATE TABLE pgcrypto_password_volatile(v text GENERATED ALWAYS AS (pgcrypto_part.gen_salt('bf',4)) STORED)", fixture.Diagnostic{Code: "42P17", Message: "generation expression is not immutable"}))
	run(fixture.ImplicitError("CREATE INDEX pgcrypto_password_volatile_idx ON pgcrypto_password_stored ((pgcrypto_part.gen_salt('bf',4)))", fixture.Diagnostic{Code: "42P17", Message: "functions in index expression must be marked IMMUTABLE"}))
	if pg {
		return
	}
	q := "SELECT pgcrypto_part.crypt($1,$2) AS value"
	held, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	old, err := c.QueryContext(p.Ctx, "SELECT pgcrypto_part.crypt('foox','"+pgcryptoPasswordSalt+"') AS value")
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
		t.Fatal("lost old TEXT row", old.Err())
	}
	var value string
	if err := old.Scan(&value); err != nil || value != pgcryptoPasswordFoox {
		t.Fatal(value, err)
	}
	if old.Next() || old.Err() != nil || metadata[0].DatabaseTypeName() != "TEXT" {
		t.Fatal("old TEXT metadata/tail", old.Err())
	}
	old.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	post := func(c *sql.Conn, r *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, c, r, check)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT s,h FROM pgcrypto_password_stored WHERE id=1", []string{"s", "h"}, []string{"TEXT", "TEXT"}, [][]any{{salt, hash}}))
		runNew(c, r, newCheck)
		runNew(c, r, newStored)
		pgcryptoReplayOverloads(t, p, c, r, pg)
	}
	post(c, r)
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{pgcryptoPasswordFoox}}, "foox", pgcryptoPasswordSalt))
	for _, v := range []struct{ salt, hash string }{{"NB", "NB53EGGqrrb5E"}, {"_J9..j2zz", "_J9..j2zzAYKMvO2BYRY"}, {"$1$Szzz0yzz", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1"}} {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{v.hash}}, "foox", v.salt))
	}
	before := extensionDump(t, p.Ctx, p.Native)
	for _, bad := range []string{dump.String() + "\nINVALID SQL;", strings.Replace(dump.String(), "VERSION '1.4'", "VERSION 'missing'", 1)} {
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid password replay accepted")
		}
		if extensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed replay changed stored salt/hash")
		}
		post(c, r)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || extensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("cancelled password replay", err)
	}
	other, target, record := pgcryptoControlDestination(t, p, "pgcrypto_password_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump.String())); err != nil {
		t.Fatal(err)
	}
	post(target, record)
	run(fixture.Exec("INSERT INTO pgcrypto_password_stored(id) VALUES(3)", 1))
	run(fixture.Select("SELECT d,h=pgcrypto_part.crypt(p,s) AS valid FROM pgcrypto_password_stored WHERE id=3", []string{"d", "valid"}, []string{"TEXT", "BOOL"}, [][]any{{pgcryptoPasswordFoox, true}}))
	run(fixture.Exec("INSERT INTO pgcrypto_password_new(id) VALUES(3)", 1))
	run(fixture.Select("SELECT d,x,m,h=pgcrypto_part.crypt(p,s) AS valid FROM pgcrypto_password_new WHERE id=3", []string{"d", "x", "m", "valid"}, []string{"TEXT", "TEXT", "TEXT", "BOOL"}, [][]any{{"NB53EGGqrrb5E", "_J9..j2zzAYKMvO2BYRY", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1", true}}))
}

func TestReferencePgcryptoPasswordConsumersAndControls(t *testing.T) {
	pgcryptoCase(t, false, pgcryptoPasswordConsumers)
}

func TestReferencePgcryptoPasswordFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoPasswordConsumers(t, p, c, r, pg)
		pgcryptoPasswordText(t, p, c, r, "SELECT pgcrypto_part.crypt('foox',$1) AS value", pgcryptoPasswordFoox, pgcryptoPasswordSalt)
		for _, v := range []struct{ password, salt, hash string }{
			{"foox", "NB", "NB53EGGqrrb5E"},
			{"longlongpassword", "_J9..j2zz", "_J9..j2zz4BeseiQNwUg"},
			{"foox", "$1$Szzz0yzzEXTRA", "$1$Szzz0yzz$IYL49cd3t9bllsA7Jmz1M1"},
		} {
			pgcryptoPasswordText(t, p, c, r, "SELECT pgcrypto_part.crypt($1,$2) AS value", v.hash, v.password, v.salt)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT pgcrypto_part.crypt('wrong',$1)=$1 AS wrong", []string{"wrong"}, []string{"BOOL"}, [][]any{{false}}, v.hash))
		}
		pgcryptoPasswordNewSalts(t, p, c, r, "pgcrypto_part.")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT pgcrypto_part.crypt('abcdefgh','NB')=pgcrypto_part.crypt('abcdefghZ','NB') AS truncated", []string{"truncated"}, []string{"BOOL"}, [][]any{{true}}))
		q := "SELECT pgcrypto_part.crypt($1,$2) AS value"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, v := range []struct {
			password any
			hash     any
		}{{"", pgcryptoPasswordEmpty}, {nil, nil}} {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, r, fixture.Select(q, []string{"value"}, []string{"TEXT"}, [][]any{{v.hash}}, v.password, pgcryptoPasswordSalt))
		}
		pgcryptoPasswordGenerated(t, p, c, r, "SELECT pgcrypto_part.gen_salt('bf',0)", "pgcrypto_part.crypt", 6)
		fixture.RunStep(t, p.Ctx, c, r, fixture.ImplicitError("SELECT pgcrypto_part.crypt('p','$2a$03$......................')", fixture.Diagnostic{Code: "22023", Message: "invalid salt"}))
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		pgcryptoInventory(t, p, c, owner, owner)
	})
}
