package reference_test

import (
	"bytes"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Exercise AES recipes in real Restore relocation, not only standalone members.
// Candidate OIDs come from a separate ordinary replay, never allocator guesses.
func pgcryptoAESRelocation(t *testing.T, p *fixture.Pair, source *sql.Conn, sourceRecord *fixture.Recorder, script string, stored, view, nested, failure fixture.Step) {
	t.Helper()
	baseline, bc, br := pgcryptoControlDestination(t, p, "pgcrypto_aes_baseline")
	fixture.RunStep(t, p.Ctx, bc, br, fixture.Exec("CREATE USER pgcrypto_owner NOSUPERUSER", 0))
	if err := stuntgres.Restore(p.Ctx, baseline, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	candidate := pgcryptoProcedureIDs(t, p, bc)
	original := pgcryptoProcedureIDs(t, p, source)
	pgcryptoInventory(t, p, bc, "pgcrypto_owner", "pgcrypto_owner")
	pgcryptoAESDependencies(t, p, bc, br)
	pgcryptoReplayOverloads(t, p, bc, br)
	for _, step := range []fixture.Step{stored, view, nested} {
		fixture.RunStep(t, p.Ctx, bc, br, step)
	}
	other, c, r := pgcryptoControlDestination(t, p, "pgcrypto_aes_collision")
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE USER pgcrypto_owner NOSUPERUSER", 0))
	var max int64
	for _, id := range candidate {
		if id > max {
			max = id
		}
	}
	collisions := map[int64]string{}
	for i := range 128 {
		name := fmt.Sprintf("pgcrypto_aes_collision_%d", i)
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 73", 0))
		var id int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		collisions[id] = name
		if id >= max {
			break
		}
	}
	for signature, id := range candidate {
		if collisions[id] == "" {
			t.Fatal("missing actual collision", signature, id)
		}
	}
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil {
		t.Fatal("AES collision Restore", err)
	}
	destination := pgcryptoProcedureIDs(t, p, c)
	for signature, id := range candidate {
		if destination[signature] == id || destination[signature] == original[signature] {
			t.Fatal("AES procedure not relocated", signature, id, destination[signature])
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT oid::bigint AS oid FROM pg_class WHERE relname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{id}}, collisions[id]))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_proc WHERE oid=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, id))
	}
	held := pgcryptoHoldMembers(t, p, c)
	old, err := c.QueryContext(p.Ctx, "SELECT g FROM pgcrypto_aes_stored WHERE id=1")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	metadata, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	// Replacing again requires all already held calls to rebind correctly.
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	post := func() {
		t.Helper()
		pgcryptoInventory(t, p, c, "pgcrypto_owner", "pgcrypto_owner")
		pgcryptoAESDependencies(t, p, c, r)
		pgcryptoReplayOverloads(t, p, c, r)
		for _, call := range held {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: call.statement, SQL: call.step.Query}, r, call.step)
		}
		for _, step := range []fixture.Step{stored, view, nested, failure} {
			fixture.RunStep(t, p.Ctx, c, r, step)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pgcrypto_aes_atomic", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO pgcrypto_aes_stored(id) VALUES(4)", 1))
		pgcryptoResult(t, p, c, r, "g", "SELECT g FROM pgcrypto_aes_stored WHERE id=4", "2c24cb7da91d6d5699801268b0f5adad")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT p,ivp FROM pgcrypto_aes_view WHERE id=4", []string{"p", "ivp"}, []string{"BYTEA", "BYTEA"}, [][]any{{"foo", "foo"}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
	}
	post()
	before := extensionDump(t, p.Ctx, other)
	for _, bad := range append(pgcryptoInvalidOwnerDumps(t, script), script+"\nINVALID SQL;") {
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid AES relocation replay accepted")
		}
		if extensionDump(t, p.Ctx, other) != before {
			t.Fatal("AES collision refusal changed target")
		}
		post()
	}
	if !old.Next() {
		t.Fatal("lost unread relocated AES result", old.Err())
	}
	var got []byte
	if err := old.Scan(&got); err != nil || !bytes.Equal(got, aesBytes(t, "2c24cb7da91d6d5699801268b0f5adad")) {
		t.Fatal(got, err)
	}
	if old.Next() || old.Err() != nil {
		t.Fatal(old.Err())
	}
	old.Close()
	if metadata[0].DatabaseTypeName() != "BYTEA" {
		t.Fatal("relocated saved metadata")
	}
	got[0] ^= 255
	pgcryptoResult(t, p, c, r, "g", "SELECT g FROM pgcrypto_aes_stored WHERE id=1", "2c24cb7da91d6d5699801268b0f5adad")
	if !reflect.DeepEqual(original, pgcryptoProcedureIDs(t, p, source)) || !reflect.DeepEqual(candidate, pgcryptoProcedureIDs(t, p, bc)) {
		t.Fatal("AES destination mutated source identities")
	}
	pgcryptoResult(t, p, source, sourceRecord, "g", "SELECT g FROM pgcrypto_aes_stored WHERE id=3", "2c24cb7da91d6d5699801268b0f5adad")
}
