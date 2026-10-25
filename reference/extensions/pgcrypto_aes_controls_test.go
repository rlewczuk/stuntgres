package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func pgcryptoAESConsumerSetup(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) (stored, view, nested, failure fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE USER pgcrypto_owner SUPERUSER", 0))
	run(fixture.Exec("CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", 0))
	run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
	run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
	run(fixture.Exec("CREATE TABLE pgcrypto_aes_stored(id integer,p bytea DEFAULT 'foo'::bytea,c bytea DEFAULT pgcrypto_part.encrypt('foo','0123456789','aes'),g bytea GENERATED ALWAYS AS(pgcrypto_part.encrypt_iv(p,'0123456','abcd','aes')) STORED)", 0))
	run(fixture.Exec("CREATE INDEX pgcrypto_aes_idx ON pgcrypto_aes_stored ((pgcrypto_part.encrypt_iv(p,'0123456','abcd','aes')))", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_aes_view AS SELECT id,pgcrypto_part.decrypt(c,'0123456789','aes') AS p,pgcrypto_part.decrypt_iv(g,'0123456','abcd','aes') AS ivp FROM pgcrypto_aes_stored", 0))
	run(fixture.Exec("CREATE VIEW pgcrypto_aes_nested AS SELECT id,(SELECT pgcrypto_part.decrypt_iv('\\x2c24cb7da91d6d5699801268b0f5adad'::bytea,'0123456','abcd','aes')) AS p FROM pgcrypto_aes_view", 0))
	run(fixture.Exec("INSERT INTO pgcrypto_aes_stored(id,p) VALUES(1,'foo'),(2,NULL)", 2))
	stored = fixture.Select("SELECT id,c,g FROM pgcrypto_aes_stored ORDER BY id", []string{"id", "c", "g"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, "f397f03d2819b7172b68d0706fda4693"), pgcryptoBytes(t, "2c24cb7da91d6d5699801268b0f5adad")}, {int64(2), pgcryptoBytes(t, "f397f03d2819b7172b68d0706fda4693"), nil}})
	view = fixture.Select("SELECT id,p,ivp FROM pgcrypto_aes_view ORDER BY id", []string{"id", "p", "ivp"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), "foo", "foo"}, {int64(2), "foo", nil}})
	nested = fixture.Select("SELECT id,p FROM pgcrypto_aes_nested ORDER BY id", []string{"id", "p"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), "foo"}, {int64(2), "foo"}})
	for _, s := range []fixture.Step{stored, view, nested} {
		run(s)
		run(fixture.Prepared(s))
	}
	run(fixture.Exec("CREATE TABLE pgcrypto_aes_atomic(p bytea CONSTRAINT pgcrypto_aes_valid CHECK(pgcrypto_part.encrypt(p,'0123456789','aes')='\\xf397f03d2819b7172b68d0706fda4693'::bytea))", 0))
	failure = fixture.ImplicitError("INSERT INTO pgcrypto_aes_atomic VALUES('foo'),('wrong')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "pgcrypto_aes_atomic" violates check constraint "pgcrypto_aes_valid"`, Detail: `Failing row contains (\x77726f6e67).`, Schema: "public", Table: "pgcrypto_aes_atomic", Constraint: "pgcrypto_aes_valid"})
	run(failure)
	run(fixture.Select("SELECT count(*) FROM pgcrypto_aes_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	pgcryptoAESDependencies(t, p, c, r)
	return
}

func TestReferencePgcryptoAESConsumersAndControls(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoAESConsumerSetup(t, p, c, r)
	})
}

func TestReferencePgcryptoAESRetainedControls(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		stored, view, nested, failure := pgcryptoAESConsumerSetup(t, p, c, r)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		if pg {
			return
		}
		allHeld := pgcryptoHoldMembers(t, p, c)
		q := "SELECT pgcrypto_part.encrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		old, err := c.QueryContext(p.Ctx, "SELECT c FROM pgcrypto_aes_stored WHERE id=1")
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
		absent := fixture.Diagnostic{Code: "3F000", Message: `schema "pgcrypto_part" does not exist`, Position: "8"}
		run(fixture.ImplicitError("SELECT pgcrypto_part.encrypt('foo','key','aes')", absent))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: q}, r, fixture.ImplicitError(q, absent, []byte("foo"), []byte("0123456"), []byte("abcd"), "aes"))
		if !old.Next() {
			t.Fatal("retained AES row missing", old.Err())
		}
		var exported []byte
		if err := old.Scan(&exported); err != nil || !bytes.Equal(exported, aesBytes(t, "f397f03d2819b7172b68d0706fda4693")) {
			t.Fatal(exported, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained tail", old.Err())
		}
		old.Close()
		if metadata[0].DatabaseTypeName() != "BYTEA" {
			t.Fatal("retained metadata")
		}
		exported[0] ^= 255
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
		pgcryptoAESDependencies(t, p, c, r)
		pgcryptoReplayOverloads(t, p, c, r, pg)
		for _, call := range allHeld {
			pgcryptoRunHeldCall(t, p, c, r, call)
		}
		for _, s := range []fixture.Step{stored, view, nested} {
			run(s)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: q}, r, fixture.Select(q, []string{"encrypt_iv"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, "2c24cb7da91d6d5699801268b0f5adad")}}, []byte("foo"), []byte("0123456"), []byte("abcd"), "aes"))
		run(fixture.Exec("INSERT INTO pgcrypto_aes_stored(id) VALUES(3)", 1))
		pgcryptoResult(t, p, c, r, "g", "SELECT g FROM pgcrypto_aes_stored WHERE id=3", "2c24cb7da91d6d5699801268b0f5adad")
		run(failure)
	})
}

func TestReferencePgcryptoAESReplayAtomicity(t *testing.T) {
	for _, name := range append([]string{"sql", "version"}, pgcryptoInvalidOwnerKinds()...) {
		t.Run(name, func(t *testing.T) {
			pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				pgcryptoAESConsumerSetup(t, p, c, r)
				if pg {
					return // Shared SQL above; native Restore has no PostgreSQL API.
				}
				script := extensionDump(t, p.Ctx, p.Native)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_aes_stored(id) VALUES(3)", 1))
				before := extensionDump(t, p.Ctx, p.Native)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pgcryptoReplayFailure(t, script, name))); err == nil {
					t.Fatal("invalid replay accepted")
				}
				if extensionDump(t, p.Ctx, p.Native) != before {
					t.Fatal("failed replay published changes")
				}
				pgcryptoResult(t, p, c, r, "encrypt", "SELECT pgcrypto_part.encrypt('foo','0123456789','aes')", "f397f03d2819b7172b68d0706fda4693")
			})
		})
	}
}

func TestReferencePgcryptoAESReplayLifecycle(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoAESConsumerSetup(t, p, c, r)
		if pg {
			return
		}
		script := extensionDump(t, p.Ctx, p.Native)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_aes_stored(id) VALUES(3)", 1))
		before := extensionDump(t, p.Ctx, p.Native)
		cancelCtx, cancel := context.WithCancel(p.Ctx)
		err := stuntgres.Restore(cancelCtx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		cancel()
		if !errors.Is(err, context.Canceled) || extensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("canceled replay changed AES target", err)
		}
		missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "pgcrypto_aes_missing_owner")
		if _, err := missing.ExecContext(p.Ctx, "CREATE TABLE sentinel(v integer)"); err != nil {
			t.Fatal(err)
		}
		missingBefore := extensionDump(t, p.Ctx, missing)
		if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(script)); err == nil {
			t.Fatal("missing owner accepted")
		}
		if extensionDump(t, p.Ctx, missing) != missingBefore {
			t.Fatal("missing owner changed sentinel")
		}
	})
}

func TestReferencePgcryptoAESRelocation(t *testing.T) {
	for _, name := range append(pgcryptoInvalidOwnerKinds(), "sql") {
		t.Run(name, func(t *testing.T) {
			pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				stored, view, nested, failure := pgcryptoAESConsumerSetup(t, p, c, r)
				if pg {
					return
				}
				script := extensionDump(t, p.Ctx, p.Native)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_aes_stored(id) VALUES(3)", 1))
				pgcryptoAESRelocation(t, p, c, r, script, stored, view, nested, failure, name)
			})
		})
	}
}

func TestReferencePgcryptoAESFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoResult(t, p, c, r, "encrypt", "SELECT encrypt('foo','0123456789','aes')", "f397f03d2819b7172b68d0706fda4693")
		pgcryptoResult(t, p, c, r, "decrypt", "SELECT decrypt('\\xf397f03d2819b7172b68d0706fda4693'::bytea,'0123456789','aes')", "666f6f")
		pgcryptoResult(t, p, c, r, "encrypt_iv", "SELECT encrypt_iv('foo','0123456','abcd','aes')", "2c24cb7da91d6d5699801268b0f5adad")
		pgcryptoResult(t, p, c, r, "decrypt_iv", "SELECT decrypt_iv('\\x2c24cb7da91d6d5699801268b0f5adad'::bytea,'0123456','abcd','aes')", "666f6f")
	})
}
