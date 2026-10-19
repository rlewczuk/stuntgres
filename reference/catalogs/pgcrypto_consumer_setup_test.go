package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

type pgcryptoConsumerProof struct {
	check, view, nested, failure fixture.Step
}

// Every independent proof starts from actual SQL-created consumers and the same
// literal oracle expectations. pgcryptoCase owns the unchanged bounded context,
// physical connection, backend cleanup and shared PostgreSQL execution route.
func pgcryptoConsumerCase(t *testing.T, body func(*testing.T, *fixture.Pair, *sql.Conn, *fixture.Recorder, bool, pgcryptoConsumerProof)) {
	t.Helper()
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) {
			t.Helper()
			defer func() {
				if t.Failed() {
					t.Logf("pgcrypto step prepared=%v SQL=%s args=%#v", s.Prepared, s.Query, s.Args)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
		run(fixture.Exec("CREATE USER pgcrypto_owner SUPERUSER", 0))
		run(fixture.Exec("CREATE SCHEMA pgcrypto_part AUTHORIZATION pgcrypto_owner", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION pgcrypto_owner", 0))
		run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		sha := pgcryptoDigestVectors[3].abc
		run(fixture.Exec("CREATE TABLE pgcrypto_stored(id integer,v text,d bytea DEFAULT pgcrypto_part.digest('abc','sha256'),g bytea GENERATED ALWAYS AS(pgcrypto_part.digest(v,'sha256')) STORED,CONSTRAINT pgcrypto_valid CHECK(v IS NULL OR pgcrypto_part.digest(v,'sha256')='\\x"+sha+"'::bytea))", 0))
		run(fixture.Exec("CREATE INDEX pgcrypto_hash_idx ON pgcrypto_stored ((pgcrypto_part.digest(v,'sha256')))", 0))
		run(fixture.Exec("CREATE VIEW pgcrypto_view AS SELECT id,pgcrypto_part.digest(v,'sha256') AS digest,pgcrypto_part.hmac(v,'Jefe','sha256') AS hmac FROM pgcrypto_stored", 0))
		run(fixture.Exec("CREATE VIEW pgcrypto_nested AS SELECT id,(SELECT pgcrypto_part.digest('abc','sha256')) AS digest FROM pgcrypto_view", 0))
		run(fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(1,'abc'),(2,NULL)", 2))
		check := fixture.Select("SELECT id,d,g FROM pgcrypto_stored ORDER BY id", []string{"id", "d", "g"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, sha), pgcryptoBytes(t, sha)}, {int64(2), pgcryptoBytes(t, sha), nil}})
		view := fixture.Select("SELECT id,digest,hmac FROM pgcrypto_view ORDER BY id", []string{"id", "digest", "hmac"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, sha), pgcryptoBytes(t, "7cf4ec4f741f51cb0d887013c46251d6f4175643c4f422906a1aaec688cc13e8")}, {int64(2), nil, nil}})
		nested := fixture.Select("SELECT id,digest FROM pgcrypto_nested ORDER BY id", []string{"id", "digest"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, sha)}, {int64(2), pgcryptoBytes(t, sha)}})
		// Keep the exact CHECK diagnostic independent of generated/default BYTEA
		// rendering, and require statement atomicity after a valid first tuple.
		run(fixture.Exec("CREATE TABLE pgcrypto_atomic(v text CONSTRAINT pgcrypto_atomic_valid CHECK(pgcrypto_part.digest(v,'sha256')='\\x"+sha+"'::bytea))", 0))
		failure := fixture.ImplicitError("INSERT INTO pgcrypto_atomic VALUES('abc'),('wrong')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "pgcrypto_atomic" violates check constraint "pgcrypto_atomic_valid"`, Detail: "Failing row contains (wrong).", Schema: "public", Table: "pgcrypto_atomic", Constraint: "pgcrypto_atomic_valid"})
		body(t, p, c, r, pg, pgcryptoConsumerProof{check: check, view: view, nested: nested, failure: failure})
	})
}
