package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Called only in the explicit native phase of a shared SQL scenario. Both held
// statements and unread detached cursors span real public catalog replacement.
func pgcryptoArmorNativeRetention(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) string {
	t.Helper()
	queries := []string{"SELECT pgcrypto_part.armor($1)", "SELECT pgcrypto_part.armor($1,$2,$3)", "SELECT pgcrypto_part.dearmor($1)", "SELECT * FROM pgcrypto_part.pgp_armor_headers($1) ORDER BY key,value"}
	steps := []fixture.Step{
		pgcryptoArmorText(queries[0], pgcryptoArmorTest, []byte("test")),
		pgcryptoArmorText(queries[1], fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}"),
		pgcryptoArmorBytes(queries[2], []byte("test"), pgcryptoArmorTest),
		fixture.PgcryptoArmorHeaders(queries[3], [][]any{{"Comment", "a"}, {"Version", "b"}}, pgcryptoArmorBadBody),
	}
	held := make([]*sql.Stmt, len(queries))
	for i, query := range queries {
		var err error
		held[i], err = c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held[i].Close()
	}
	oldBytes, err := held[2].QueryContext(p.Ctx, pgcryptoArmorTest)
	if err != nil {
		t.Fatal(err)
	}
	defer oldBytes.Close()
	oldHeaders, err := held[3].QueryContext(p.Ctx, pgcryptoArmorBadBody)
	if err != nil {
		t.Fatal(err)
	}
	defer oldHeaders.Close()
	byteMetadata, err := oldBytes.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	headerMetadata, err := oldHeaders.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	script := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	for i, query := range queries {
		position := "8"
		if i == 3 {
			position = "15"
		}
		absent := fixture.ImplicitError(query, fixture.Diagnostic{Code: "3F000", Message: `schema "pgcrypto_part" does not exist`, Position: position}, steps[i].Args...)
		absent.SelectRows = true
		absent.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held[i], SQL: query}, r, absent)
		fixture.RunStep(t, p.Ctx, c, r, absent)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	for i, step := range steps {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held[i], SQL: queries[i]}, r, step)
		pgcryptoArmorBoth(t, p, c, r, step)
	}
	if len(byteMetadata) != 1 || byteMetadata[0].Name() != "dearmor" || byteMetadata[0].DatabaseTypeName() != "BYTEA" || len(headerMetadata) != 2 || headerMetadata[0].Name() != "key" || headerMetadata[1].Name() != "value" || headerMetadata[0].DatabaseTypeName() != "TEXT" || headerMetadata[1].DatabaseTypeName() != "TEXT" {
		t.Fatal("retained armor metadata", byteMetadata, headerMetadata)
	}
	if !oldBytes.Next() {
		t.Fatal("unread scalar row lost", oldBytes.Err())
	}
	var value []byte
	if err := oldBytes.Scan(&value); err != nil || !bytes.Equal(value, []byte("test")) {
		t.Fatal("retained BYTEA", value, err)
	}
	if oldBytes.Next() || oldBytes.Err() != nil {
		t.Fatal("retained scalar tail", oldBytes.Err())
	}
	for _, pair := range [][2]string{{"Comment", "a"}, {"Version", "b"}} {
		if !oldHeaders.Next() {
			t.Fatal("unread header row lost", oldHeaders.Err())
		}
		var key, value string
		if err := oldHeaders.Scan(&key, &value); err != nil || key != pair[0] || value != pair[1] {
			t.Fatal("retained headers", key, value, err)
		}
	}
	if oldHeaders.Next() || oldHeaders.Err() != nil {
		t.Fatal("retained header tail", oldHeaders.Err())
	}
	return script
}

func TestReferencePgcryptoArmorControls(t *testing.T) {
	t.Run("reset_restore_retention", func(t *testing.T) {
		pgcryptoArmorConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			ids := pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
			pgcryptoArmorDependencies(t, p, c, ids)
			if pg {
				return
			}
			pgcryptoArmorNativeRetention(t, p, c, r)
			ids = pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
			pgcryptoArmorDependencies(t, p, c, ids)
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO armor_consumers(id) VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT a,h,b FROM armor_consumers WHERE id=2", []string{"a", "h", "b"}, []string{"TEXT", "TEXT", "BYTEA"}, [][]any{{pgcryptoArmorTest, fixture.PgcryptoArmorZooka, "test"}}))
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
		})
	})
	t.Run("independent_destination_with_collisions", func(t *testing.T) {
		pgcryptoArmorConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			if pg {
				return
			}
			script := fixture.ExtensionDump(t, p.Ctx, p.Native)
			original := pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
			baseline, bc, br := pgcryptoControlDestination(t, p, "armor_baseline")
			fixture.RunStep(t, p.Ctx, bc, br, fixture.Exec("CREATE USER pgcrypto_owner NOSUPERUSER", 0))
			if err := stuntgres.Restore(p.Ctx, baseline, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
			candidate := pgcryptoArmorInventory(t, p, bc, br, "pgcrypto_part")
			other, target, record := pgcryptoControlDestination(t, p, "armor_destination")
			fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("CREATE USER pgcrypto_owner NOSUPERUSER", 0))
			var max int64
			for _, id := range candidate {
				if id > max {
					max = id
				}
			}
			collisions := map[int64]string{}
			for i := range 128 {
				name := fmt.Sprintf("armor_collision_%d", i)
				fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 73", 0))
				var id int64
				if err := target.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&id); err != nil {
					t.Fatal(err)
				}
				collisions[id] = name
				if id >= max {
					break
				}
			}
			for signature, id := range candidate {
				if collisions[id] == "" {
					t.Fatal("missing real collision", signature, id)
				}
			}
			if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
			destination := pgcryptoArmorInventory(t, p, target, record, "pgcrypto_part")
			for signature, id := range candidate {
				if destination[signature] == id || destination[signature] == original[signature] {
					t.Fatal("armor declaration not remapped", signature, destination[signature], id)
				}
				fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT oid::bigint AS oid FROM pg_class WHERE relname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{id}}, collisions[id]))
			}
			pgcryptoArmorDependencies(t, p, target, destination)
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, target, record, step)
			}
			pgcryptoArmorBoth(t, p, target, record, pgcryptoArmorText("SELECT pgcrypto_part.armor($1)", pgcryptoArmorTest, []byte("test")))
			pgcryptoArmorBoth(t, p, target, record, pgcryptoArmorText("SELECT pgcrypto_part.armor($1,$2,$3)", fixture.PgcryptoArmorZooka, []byte("zooka"), "{foo}", "{bar}"))
			pgcryptoArmorBoth(t, p, target, record, pgcryptoArmorBytes("SELECT pgcrypto_part.dearmor($1)", []byte("test"), pgcryptoArmorTest))
			pgcryptoArmorBoth(t, p, target, record, fixture.PgcryptoArmorHeaders("SELECT * FROM pgcrypto_part.pgp_armor_headers($1)", [][]any{{"foo", "bar"}}, fixture.PgcryptoArmorZooka))
			fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("INSERT INTO armor_consumers(id) VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, target, record, fixture.Select("SELECT a,h,b FROM armor_consumers WHERE id=2", []string{"a", "h", "b"}, []string{"TEXT", "TEXT", "BYTEA"}, [][]any{{pgcryptoArmorTest, fixture.PgcryptoArmorZooka, "test"}}))
			fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("ROLLBACK", 0))
			if !reflect.DeepEqual(original, pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")) || !reflect.DeepEqual(candidate, pgcryptoArmorInventory(t, p, bc, br, "pgcrypto_part")) {
				t.Fatal("destination replay mutated source identities")
			}
		})
	})
	for _, failure := range pgcryptoReplayFailureNames() {
		t.Run("atomic_"+failure, func(t *testing.T) {
			pgcryptoArmorConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
				for _, step := range pgcryptoArmorConsumerSteps() {
					pgcryptoArmorBoth(t, p, c, r, step)
				}
				if pg {
					return
				}
				script := fixture.ExtensionDump(t, p.Ctx, p.Native)
				old, err := c.QueryContext(p.Ctx, "SELECT key,value FROM armor_source_view ORDER BY key,value")
				if err != nil {
					t.Fatal(err)
				}
				defer old.Close()
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE armor_sentinel(v integer)", 0))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO armor_sentinel VALUES(73)", 1))
				before := fixture.ExtensionDump(t, p.Ctx, p.Native)
				// Use the shared real extension-owner corruption route.
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(pgcryptoReplayFailure(t, script, failure))); err == nil {
					t.Fatal("invalid armor replay accepted", failure)
				}
				if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
					t.Fatal("failed armor replay changed destination", failure)
				}
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT v FROM armor_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(73)}}))
				for _, step := range pgcryptoArmorConsumerSteps() {
					pgcryptoArmorBoth(t, p, c, r, step)
				}
				pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
				if !old.Next() {
					t.Fatal("failed replay lost unread source row", old.Err())
				}
				var key, value string
				if err := old.Scan(&key, &value); err != nil || key != "foo" || value != "bar" {
					t.Fatal(key, value, err)
				}
				if old.Next() || old.Err() != nil {
					t.Fatal("failed replay retained row tail", old.Err())
				}
			})
		})
	}
	t.Run("canceled_and_missing_owner", func(t *testing.T) {
		pgcryptoArmorConsumerCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			if pg {
				return
			}
			script := fixture.ExtensionDump(t, p.Ctx, p.Native)
			old, err := c.QueryContext(p.Ctx, "SELECT key,value FROM armor_source_view ORDER BY key,value")
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			metadata, err := old.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
			cancel()
			if !errors.Is(err, context.Canceled) {
				t.Fatal("canceled armor Restore", err)
			}
			if fixture.ExtensionDump(t, p.Ctx, p.Native) != script {
				t.Fatal("canceled replay changed source")
			}
			for _, step := range pgcryptoArmorConsumerSteps() {
				pgcryptoArmorBoth(t, p, c, r, step)
			}
			pgcryptoArmorInventory(t, p, c, r, "pgcrypto_part")
			if len(metadata) != 2 || metadata[0].Name() != "key" || metadata[1].Name() != "value" || metadata[0].DatabaseTypeName() != "TEXT" || metadata[1].DatabaseTypeName() != "TEXT" {
				t.Fatal("canceled replay retained metadata", metadata)
			}
			if !old.Next() {
				t.Fatal("canceled replay lost unread source row", old.Err())
			}
			var key, value string
			if err := old.Scan(&key, &value); err != nil || key != "foo" || value != "bar" {
				t.Fatal(key, value, err)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal("canceled replay retained row tail", old.Err())
			}
			missing, target, record := pgcryptoControlDestination(t, p, "armor_missing_owner")
			fixture.RunStep(t, p.Ctx, target, record, fixture.Exec("CREATE TABLE armor_sentinel(v integer)", 0))
			before := fixture.ExtensionDump(t, p.Ctx, missing)
			if err := stuntgres.Restore(p.Ctx, missing, strings.NewReader(script)); err == nil {
				t.Fatal("missing armor owner accepted")
			}
			if fixture.ExtensionDump(t, p.Ctx, missing) != before {
				t.Fatal("missing-owner replay changed target")
			}
		})
	})
}
