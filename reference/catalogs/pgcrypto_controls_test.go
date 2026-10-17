package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgcryptoConsumersAndControls(t *testing.T) {
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
		pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
		sha := pgcryptoDigestVectors[3].abc
		run(fixture.Exec("CREATE TABLE pgcrypto_stored(id integer,v text,d bytea DEFAULT pgcrypto_part.digest('abc','sha256'),g bytea GENERATED ALWAYS AS(pgcrypto_part.digest(v,'sha256')) STORED,CONSTRAINT pgcrypto_valid CHECK(v IS NULL OR pgcrypto_part.digest(v,'sha256')='\\x"+sha+"'::bytea))", 0))
		run(fixture.Exec("CREATE INDEX pgcrypto_hash_idx ON pgcrypto_stored ((pgcrypto_part.digest(v,'sha256')))", 0))
		run(fixture.Exec("CREATE VIEW pgcrypto_view AS SELECT id,pgcrypto_part.digest(v,'sha256') AS digest,pgcrypto_part.hmac(v,'Jefe','sha256') AS hmac FROM pgcrypto_stored", 0))
		run(fixture.Exec("CREATE VIEW pgcrypto_nested AS SELECT id,(SELECT pgcrypto_part.digest('abc','sha256')) AS digest FROM pgcrypto_view", 0))
		run(fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(1,'abc'),(2,NULL)", 2))
		check := fixture.Select("SELECT id,d,g FROM pgcrypto_stored ORDER BY id", []string{"id", "d", "g"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, sha), pgcryptoBytes(t, sha)}, {int64(2), pgcryptoBytes(t, sha), nil}})
		run(check)
		run(fixture.Prepared(check))
		view := fixture.Select("SELECT id,digest,hmac FROM pgcrypto_view ORDER BY id", []string{"id", "digest", "hmac"}, []string{"INT4", "BYTEA", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, sha), pgcryptoBytes(t, "7cf4ec4f741f51cb0d887013c46251d6f4175643c4f422906a1aaec688cc13e8")}, {int64(2), nil, nil}})
		run(view)
		run(fixture.Prepared(view))
		nested := fixture.Select("SELECT id,digest FROM pgcrypto_nested ORDER BY id", []string{"id", "digest"}, []string{"INT4", "BYTEA"}, [][]any{{int64(1), pgcryptoBytes(t, sha)}, {int64(2), pgcryptoBytes(t, sha)}})
		run(nested)
		run(fixture.Prepared(nested))
		// CHECK failure is statement-atomic, even after a valid first tuple.
		// Generated/default BYTEA values make the release's failing-row Detail long;
		// use a separate CHECK table to keep every diagnostic field independently exact.
		run(fixture.Exec("CREATE TABLE pgcrypto_atomic(v text CONSTRAINT pgcrypto_atomic_valid CHECK(pgcrypto_part.digest(v,'sha256')='\\x"+sha+"'::bytea))", 0))
		failure := fixture.ImplicitError("INSERT INTO pgcrypto_atomic VALUES('abc'),('wrong')", fixture.Diagnostic{Code: "23514", Message: `new row for relation "pgcrypto_atomic" violates check constraint "pgcrypto_atomic_valid"`, Detail: "Failing row contains (wrong).", Schema: "public", Table: "pgcrypto_atomic", Constraint: "pgcrypto_atomic_valid"})
		run(failure)
		run(fixture.Select("SELECT count(*) FROM pgcrypto_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgcryptoConsumerDependencies(t, p, c, r, pgcryptoProcedureIDs(t, p, c))
		if pg {
			// This phase uses PostgreSQL's real TEMP lifetime, not native Restore.
			run(fixture.Exec("CREATE TEMP TABLE pgcrypto_retained(v integer,b bytea)", 0))
			pgcryptoTempLinks(t, p, c)
			return
		}
		heldSQL := "SELECT pgcrypto_part.digest($1,$2)"
		held, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		old, err := c.QueryContext(p.Ctx, "SELECT pgcrypto_part.digest('abc','sha256')")
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
		run(fixture.ImplicitError("SELECT pgcrypto_part.digest('abc','sha256')", absent))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: heldSQL}, r, fixture.ImplicitError(heldSQL, absent, "abc", "sha256"))
		if !old.Next() {
			t.Fatal("lost retained row", old.Err())
		}
		var retained []byte
		if err := old.Scan(&retained); err != nil || !bytes.Equal(retained, []byte(pgcryptoBytes(t, sha))) {
			t.Fatal(retained, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained row tail", old.Err())
		}
		old.Close()
		if metadata[0].DatabaseTypeName() != "BYTEA" {
			t.Fatal("retained metadata")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
		run(check)
		run(view)
		run(nested)
		pgcryptoReplayOverloads(t, p, c, r, pg)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: heldSQL}, r, fixture.Select(heldSQL, []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, sha)}}, "abc", "sha256"))
		run(fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(3,'abc')", 1))
		run(failure)
		// Invalid provider/version/metadata/late SQL must not publish a partial replay.
		before := extensionDump(t, p.Ctx, p.Native)
		badReplays := []string{strings.Replace(dump.String(), "VERSION '1.4'", "VERSION 'missing'", 1), strings.Replace(dump.String(), "stuntgres-extension-owners-v1", "stuntgres-extension-owners-v2", 1), dump.String() + "\nCREATE EXTENSION nonexistent_provider;", dump.String() + "\nINVALID SQL;"}
		badReplays = append(badReplays, pgcryptoInvalidOwnerDumps(t, dump.String())...)
		for _, script := range badReplays {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err == nil {
				t.Fatal("bad replay accepted")
			}
			if extensionDump(t, p.Ctx, p.Native) != before {
				t.Fatal("bad replay changed target")
			}
			pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
			pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", sha)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled replay", err)
		}
		cancel()
		if extensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("cancelled replay changed target")
		}
		other, target, targetRecord := pgcryptoControlDestination(t, p, "pgcrypto_destination")
		if _, err := other.ExecContext(p.Ctx, "CREATE USER pgcrypto_owner NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		pgcryptoInventory(t, p, target, "pgcrypto_owner", "pgcrypto_owner")
		fixture.RunStep(t, p.Ctx, target, targetRecord, check)
		fixture.RunStep(t, p.Ctx, target, targetRecord, view)
		fixture.RunStep(t, p.Ctx, target, targetRecord, nested)
		pgcryptoReplayOverloads(t, p, target, targetRecord, pg)
		pgcryptoForcedRelocation(t, p, c, target, r, targetRecord, dump.String(), check, view, nested, failure)
		pgcryptoResult(t, p, target, targetRecord, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", sha)
		missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "pgcrypto_missing_owner")
		if _, err := missing.ExecContext(p.Ctx, "CREATE TABLE sentinel(v integer)"); err != nil {
			t.Fatal(err)
		}
		missingBefore := extensionDump(t, p.Ctx, missing)
		if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(dump.String())); err == nil {
			t.Fatal("missing named owner accepted")
		}
		if extensionDump(t, p.Ctx, missing) != missingBefore {
			t.Fatal("missing named owner changed target")
		}
	})
}

func TestReferencePgcryptoFeatureSmoke(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE SCHEMA pgcrypto_part", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA pgcrypto_part", 0))
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		pgcryptoInventory(t, p, c, owner, owner)
		pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", pgcryptoDigestVectors[3].abc)
		pgcryptoResult(t, p, c, r, "hmac", "SELECT pgcrypto_part.hmac($1,$2,$3)", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843", "what do ya want for nothing?", "Jefe", "sha256")
		pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest($1::bytea,$2)", pgcryptoDigestVectors[3].abc, []byte("abc"), "sha256")
		pgcryptoResult(t, p, c, r, "hmac", "SELECT pgcrypto_part.hmac($1::bytea,$2::bytea,$3)", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843", []byte("what do ya want for nothing?"), []byte("Jefe"), "sha256")
		fixture.RunStep(t, p.Ctx, c, r, pgcryptoUnsupported("SELECT pgcrypto_part.digest('abc','NO_SUCH_HASH')", "no_such_hash"))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT pgcrypto_part.hmac(NULL,'key','NO_SUCH_HASH')", []string{"hmac"}, []string{"BYTEA"}, [][]any{{nil}}))
	})
}

// Corrupt the actual emitted manifest rather than inventing a separate replay
// format. Every candidate is exercised through public Restore against live data.
func pgcryptoInvalidOwnerDumps(t *testing.T, dump string) []string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v1 "
	var line string
	for _, candidate := range strings.Split(dump, "\n") {
		if strings.HasPrefix(candidate, marker) {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatal("missing extension owner manifest")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, marker))
	if err != nil {
		t.Fatal(err)
	}
	type member struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Signature string `json:"signature"`
		Owner     string `json:"owner"`
	}
	type manifest struct {
		Name    string   `json:"name"`
		Schema  string   `json:"schema"`
		Version string   `json:"version"`
		Owner   string   `json:"owner"`
		Members []member `json:"members"`
	}
	var scripts []string
	for _, kind := range []string{"missing", "extra", "duplicate", "name", "signature", "dictionary", "type", "empty_owner", "missing_owner"} {
		var record manifest
		if err := json.Unmarshal(payload, &record); err != nil {
			t.Fatal(err)
		}
		if record.Name != "pgcrypto" || record.Version != "1.4" || len(record.Members) != 14 {
			t.Fatal("unexpected pgcrypto manifest", record)
		}
		switch kind {
		case "missing":
			record.Members = record.Members[1:]
		case "extra":
			record.Members = append(record.Members, record.Members[0])
		case "duplicate":
			record.Members[1] = record.Members[0]
		case "name":
			record.Members[0].Name = "unaccent"
		case "signature":
			record.Members[0].Signature = "17,25,25"
		case "dictionary":
			record.Members[0] = member{Kind: "dictionary", Name: "unaccent", Owner: "pgcrypto_owner"}
		case "type":
			record.Members[0] = member{Kind: "type", Name: "query_int", Owner: "pgcrypto_owner"}
		case "empty_owner":
			record.Members[0].Owner = ""
		case "missing_owner":
			record.Members[0].Owner = "pgcrypto_absent_owner"
		}
		data, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		scripts = append(scripts, strings.Replace(dump, line, marker+base64.StdEncoding.EncodeToString(data), 1))
	}
	return scripts
}
