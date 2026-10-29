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

// Each proof owns a fresh bounded scenario. Only dependent retained consumers
// and replacement targets share a fixture.
func TestReferencePgcryptoConsumersAndControls(t *testing.T) {
	pgcryptoConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, proof pgcryptoConsumerProof) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
		for _, step := range []fixture.Step{proof.check, proof.view, proof.nested} {
			run(step)
			run(fixture.Prepared(step))
		}
		run(proof.failure)
		run(fixture.Select("SELECT count(*) FROM pgcrypto_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		pgcryptoConsumerDependencies(t, p, c, r, pgcryptoProcedureIDs(t, p, c))
		if pg {
			// PostgreSQL's real TEMP lifetime, not a simulated Restore API.
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TEMP TABLE pgcrypto_retained(v integer,b bytea)", 0))
			pgcryptoTempLinks(t, p, c)
		}
	})
}

func TestReferencePgcryptoControlRetention(t *testing.T) {
	pgcryptoConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, proof pgcryptoConsumerProof) {
		if pg {
			return
		}
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		sha := pgcryptoDigestVectors[3].abc
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
		run(proof.check)
		run(proof.view)
		run(proof.nested)
		pgcryptoReplayOverloads(t, p, c, r, pg)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: held, SQL: heldSQL}, r, fixture.Select(heldSQL, []string{"digest"}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, sha)}}, "abc", "sha256"))
		run(fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(3,'abc')", 1))
		run(proof.failure)
	})
}

func TestReferencePgcryptoReplayAtomicity(t *testing.T) {
	for _, name := range pgcryptoReplayFailureNames() {
		t.Run(name, func(t *testing.T) {
			pgcryptoConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, proof pgcryptoConsumerProof) {
				if pg {
					return
				}
				script := fixture.ExtensionDump(t, p.Ctx, p.Native)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(3,'abc')", 1))
				before := fixture.ExtensionDump(t, p.Ctx, p.Native)
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pgcryptoReplayFailure(t, script, name))); err == nil {
					t.Fatal("bad replay accepted")
				}
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
					t.Fatal("bad replay changed target")
				}
				pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
				pgcryptoResult(t, p, c, r, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", pgcryptoDigestVectors[3].abc)
			})
		})
	}
}

func TestReferencePgcryptoReplayLifecycle(t *testing.T) {
	pgcryptoConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, proof pgcryptoConsumerProof) {
		if pg {
			return
		}
		script := fixture.ExtensionDump(t, p.Ctx, p.Native)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(3,'abc')", 1))
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		ctx, cancel := context.WithCancel(p.Ctx)
		if err := stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel}); !errors.Is(err, context.Canceled) {
			t.Fatal("cancelled replay", err)
		}
		cancel()
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("cancelled replay changed target")
		}
		missing := fixture.CatalogControlPool(t, stuntgres.NewDriver(), "pgcrypto_missing_owner")
		if _, err := missing.ExecContext(p.Ctx, "CREATE TABLE sentinel(v integer)"); err != nil {
			t.Fatal(err)
		}
		missingBefore := fixture.ExtensionDump(t, p.Ctx, missing)
		if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(script)); err == nil {
			t.Fatal("missing named owner accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, missing) != missingBefore {
			t.Fatal("missing named owner changed target")
		}
	})
}

func TestReferencePgcryptoControlDestination(t *testing.T) {
	pgcryptoConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, proof pgcryptoConsumerProof) {
		if pg {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		other, target, targetRecord := pgcryptoControlDestination(t, p, "pgcrypto_destination")
		if _, err := other.ExecContext(p.Ctx, "CREATE USER pgcrypto_owner NOSUPERUSER"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		pgcryptoInventory(t, p, target, "pgcrypto_owner", "pgcrypto_owner")
		fixture.RunStep(t, p.Ctx, target, targetRecord, proof.check)
		fixture.RunStep(t, p.Ctx, target, targetRecord, proof.view)
		fixture.RunStep(t, p.Ctx, target, targetRecord, proof.nested)
		pgcryptoReplayOverloads(t, p, target, targetRecord, pg)
		pgcryptoResult(t, p, target, targetRecord, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", pgcryptoDigestVectors[3].abc)
	})
}

func TestReferencePgcryptoForcedRelocation(t *testing.T) {
	for _, refusal := range pgcryptoRelocationRefusals() {
		t.Run(refusal.name, func(t *testing.T) {
			pgcryptoConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, proof pgcryptoConsumerProof) {
				if pg {
					return
				}
				script := fixture.ExtensionDump(t, p.Ctx, p.Native)
				other, target, targetRecord := pgcryptoControlDestination(t, p, "pgcrypto_destination")
				if _, err := other.ExecContext(p.Ctx, "CREATE USER pgcrypto_owner NOSUPERUSER"); err != nil {
					t.Fatal(err)
				}
				// Baseline retains the two original rows; only the source gains row3.
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_stored(id,v) VALUES(3,'abc')", 1))
				if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil {
					t.Fatal(err)
				}
				pgcryptoInventory(t, p, target, "pgcrypto_owner", "pgcrypto_owner")
				for _, step := range []fixture.Step{proof.check, proof.view, proof.nested} {
					fixture.RunStep(t, p.Ctx, target, targetRecord, step)
				}
				pgcryptoReplayOverloads(t, p, target, targetRecord, pg)
				pgcryptoForcedRelocation(t, p, c, target, r, targetRecord, script, proof.check, proof.view, proof.nested, proof.failure, refusal)
				pgcryptoResult(t, p, target, targetRecord, "digest", "SELECT pgcrypto_part.digest('abc','sha256')", pgcryptoDigestVectors[3].abc)
			})
		})
	}
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

func pgcryptoReplayFailureNames() []string {
	return append([]string{"version", "metadata", "provider", "sql"}, pgcryptoInvalidOwnerKinds()...)
}

func pgcryptoReplayFailure(t *testing.T, script, name string) string {
	t.Helper()
	switch name {
	case "version":
		return strings.Replace(script, "VERSION '1.4'", "VERSION 'missing'", 1)
	case "metadata":
		return strings.Replace(script, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1)
	case "provider":
		return script + "\nCREATE EXTENSION nonexistent_provider;"
	case "sql":
		return script + "\nINVALID SQL;"
	}
	for _, kind := range pgcryptoInvalidOwnerKinds() {
		if kind == name {
			return pgcryptoInvalidOwnerDump(t, script, kind)
		}
	}
	t.Fatal("unknown replay failure", name)
	return ""
}

func pgcryptoInvalidOwnerKinds() []string {
	return []string{"missing", "extra", "duplicate", "slot", "schema", "name", "signature", "dictionary", "type", "empty_owner", "missing_owner"}
}

func pgcryptoInvalidOwnerDump(t *testing.T, dump, kind string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	start, end, payload := fixture.CatalogExtensionOwnerDirective(t, dump, "pgcrypto", true)
	line := dump[start:end]
	type member struct {
		Kind        string          `json:"kind"`
		Declaration *int            `json:"declaration,omitempty"`
		Version     string          `json:"version"`
		Extension   string          `json:"extension"`
		ACL         json.RawMessage `json:"acl,omitempty"`
		Schema      string          `json:"schema"`
		Name        string          `json:"name"`
		Signature   string          `json:"signature"`
		Owner       string          `json:"owner"`
	}
	type manifest struct {
		Name    string   `json:"name"`
		Schema  string   `json:"schema"`
		Version string   `json:"version"`
		Owner   string   `json:"owner"`
		Present bool     `json:"present"`
		Members []member `json:"members"`
	}
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
	case "slot":
		record.Members[0].Declaration = new(-1)
	case "schema":
		record.Members[0].Schema = "pgcrypto_absent_schema"
	case "name":
		record.Members[0].Name = "unaccent"
	case "signature":
		record.Members[0].Signature = "17,25,25"
	case "dictionary":
		record.Members[0].Kind = "dictionary"
		record.Members[0].Name = "unaccent"
		record.Members[0].Signature = ""
	case "type":
		record.Members[0].Kind = "type"
		record.Members[0].Name = "query_int"
		record.Members[0].Signature = ""
	case "empty_owner":
		record.Members[0].Owner = ""
	case "missing_owner":
		record.Members[0].Owner = "pgcrypto_absent_owner"
	default:
		t.Fatalf("unknown pgcrypto owner mutation %q", kind)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(dump, line, marker+base64.StdEncoding.EncodeToString(data), 1)
}
