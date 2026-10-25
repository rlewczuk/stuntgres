package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgcryptoRandomRelocation(t *testing.T) {
	pgcryptoCase(t, false, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		pgcryptoRandomInstall(t, p, c, r)
		pgcryptoRandomConsumerSetup(t, p, c, r)
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		source := pgcryptoRandomInventory(t, p, c, pg, owner)
		stored := pgcryptoRandomStored(t, p, c, pg)
		pgcryptoRandomFresh(t, p, c, r, pg)
		pgcryptoRandomDependencies(t, p, c, r, source)
		if pg {
			return
		}
		script := extensionDump(t, p.Ctx, p.Native)
		baseline, base, baseRecord := pgcryptoControlDestination(t, p, "pgcrypto_random_relocation_baseline")
		if err := stuntgres.Restore(p.Ctx, baseline, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		candidate := pgcryptoRandomInventory(t, p, base, false, owner)
		target, conn, record := pgcryptoControlDestination(t, p, "pgcrypto_random_relocation_collision")
		maxID := int64(0)
		for _, id := range candidate {
			if id > maxID {
				maxID = id
			}
		}
		collisions := map[int64]string{}
		for i := range 128 {
			name := fmt.Sprintf("pgcrypto_random_collision_%d", i)
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TEMP SEQUENCE "+name+" START 73", 0))
			var id int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname=$1", name).Scan(&id); err != nil {
				t.Fatal(err)
			}
			collisions[id] = name
			if id >= maxID {
				break
			}
		}
		for signature, id := range candidate {
			if collisions[id] == "" {
				t.Fatal("missing real collision", signature, id)
			}
		}
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TEMP TABLE pgcrypto_random_collision_data(v integer)", 0))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("INSERT INTO pgcrypto_random_collision_data VALUES(81)", 1))
		var sequence string
		for _, name := range collisions {
			sequence = name
			break
		}
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT nextval('"+sequence+"') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(73)}}))
		// Native eager Rows own their result, unlike an old SQL HOLD portal,
		// whose FETCH/MOVE remains fenced after catalog replacement.
		retained, err := conn.QueryContext(p.Ctx, "SELECT v FROM pgcrypto_random_collision_data")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		retainedMetadata := func() pgcryptoTempColumn {
			t.Helper()
			columns, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(columns, []string{"v"}) {
				t.Fatal("retained columns", columns, err)
			}
			metadata, err := retained.ColumnTypes()
			if err != nil || len(metadata) != 1 {
				t.Fatal("retained metadata", metadata, err)
			}
			nullable, known := metadata[0].Nullable()
			got := pgcryptoTempColumn{metadata[0].Name(), metadata[0].DatabaseTypeName(), metadata[0].ScanType(), nullable, known}
			want := pgcryptoTempColumn{"v", "INT4", reflect.TypeOf(int64(0)), true, true}
			if got != want {
				t.Fatal("retained INT4 metadata", got, want)
			}
			return got
		}
		savedMetadata := retainedMetadata()
		if err := stuntgres.Restore(p.Ctx, target, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		if retainedMetadata() != savedMetadata {
			t.Fatal("replacement changed unread metadata")
		}
		destination := pgcryptoRandomInventory(t, p, conn, false, owner)
		for signature, id := range candidate {
			if destination[signature] == id || destination[signature] == source[signature] {
				t.Fatal("member not relocated", signature, id, destination[signature])
			}
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT oid::bigint AS oid FROM pg_class WHERE relname=$1", []string{"oid"}, []string{"INT8"}, [][]any{{id}}, collisions[id]))
		}
		type heldCall struct {
			stmt *sql.Stmt
			kind string
		}
		var held []heldCall
		for _, v := range []struct{ call, kind string }{{"gen_random_bytes(32)", "bytes"}, {"gen_random_uuid()", "uuid"}, {"fips_mode()", "fips"}} {
			stmt, err := conn.PrepareContext(p.Ctx, "SELECT pgcrypto_part."+v.call+" AS value")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held = append(held, heldCall{stmt, v.kind})
		}
		// A successful second replacement forces these real held statements to
		// reanalyze installed identities rather than only surviving failed replay.
		if err := stuntgres.Restore(p.Ctx, target, strings.NewReader(script)); err != nil {
			t.Fatal(err)
		}
		destination = pgcryptoRandomInventory(t, p, conn, false, owner)
		post := func() {
			if retainedMetadata() != savedMetadata {
				t.Fatal("replay changed unread metadata")
			}
			fixture.RunStep(t, p.Ctx, conn, record, stored)
			pgcryptoRandomDependencies(t, p, conn, record, destination)
			pgcryptoRandomFresh(t, p, conn, record, false)
			if !reflect.DeepEqual(destination, pgcryptoRandomInventory(t, p, conn, false, owner)) {
				t.Fatal("destination identity changed")
			}
			for _, call := range held {
				rows, err := call.stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				switch call.kind {
				case "bytes":
					pgcryptoRandomByteRows(t, rows, 32)
				case "uuid":
					pgcryptoRandomUUIDRows(t, rows, false)
				case "fips":
					meta, err := rows.ColumnTypes()
					if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "BOOL" {
						t.Fatal(meta, err)
					}
					if !rows.Next() {
						t.Fatal(rows.Err())
					}
					var raw any
					if err := rows.Scan(&raw); err != nil || raw != pgcryptoRandomFIPSExpected(false) {
						t.Fatal(raw, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal(rows.Err())
					}
					rows.Close()
				}
			}
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT v FROM pgcrypto_random_collision_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(81)}}))
			fixture.RunStep(t, p.Ctx, conn, record, fixture.Select("SELECT currval('"+sequence+"') AS value", []string{"value"}, []string{"INT8"}, [][]any{{int64(73)}}))
		}
		post()
		pgcryptoRandomRejectReplay(t, p, target, script, post)
		if !reflect.DeepEqual(source, pgcryptoRandomInventory(t, p, c, false, owner)) || !reflect.DeepEqual(candidate, pgcryptoRandomInventory(t, p, base, false, owner)) {
			t.Fatal("source/baseline identities changed")
		}
		fixture.RunStep(t, p.Ctx, c, r, stored)
		fixture.RunStep(t, p.Ctx, base, baseRecord, stored)
		if !retained.Next() {
			t.Fatal("lost unread retained row", retained.Err())
		}
		var raw any
		if err := retained.Scan(&raw); err != nil || raw != int64(81) {
			t.Fatalf("retained INT4 raw %T %v: %v", raw, raw, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained row tail", retained.Err())
		}
	})
}
