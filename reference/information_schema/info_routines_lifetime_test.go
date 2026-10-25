package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoRoutinesLifetime(t *testing.T) {
	irScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE FUNCTION ir_life(integer) RETURNS integer LANGUAGE SQL AS ' SELECT $1 '", 0))
		id := rsuIdentity(t, p, c, "public", "ir_life", "integer")
		row := irRow(db, "public", "ir_life", id, "FUNCTION", "integer", "pg_catalog", "int4", "SQL", " SELECT $1 ", nil, false, false)
		where := "routine_schema IN ('public','ir_moved') AND routine_name IN ('ir_life','ir_renamed')"
		step := func(rows ...[]any) fixture.Step { return irProjection(b.PG, where, rows) }
		held := rsuHeld(t, p, c, b.Recorder, step().Query)
		held(step(row))
		run(fixture.Exec("CREATE FUNCTION ir_life(text) RETURNS text LANGUAGE SQL AS ' SELECT $1 '", 0))
		other := irRow(db, "public", "ir_life", rsuIdentity(t, p, c, "public", "ir_life", "text"), "FUNCTION", "text", "pg_catalog", "text", "SQL", " SELECT $1 ", nil, false, false)
		held(step(row, other))
		run(fixture.Exec("DROP FUNCTION ir_life(integer)", 0))
		held(step(other))
		run(fixture.Exec("CREATE FUNCTION ir_life(integer) RETURNS integer LANGUAGE SQL AS ' SELECT $1+1 '", 0))
		replacement := rsuIdentity(t, p, c, "public", "ir_life", "integer")
		if replacement == id {
			t.Fatal("drop/recreate retained identity")
		}
		row = irRow(db, "public", "ir_life", replacement, "FUNCTION", "integer", "pg_catalog", "int4", "SQL", " SELECT $1+1 ", nil, false, false)
		held(step(row, other))
		run(fixture.Exec("ALTER FUNCTION ir_life(integer) RENAME TO ir_renamed", 0))
		row[2] = rsuIdentity(t, p, c, "public", "ir_renamed", "integer")
		row[5] = "ir_renamed"
		held(step(row, other))
		run(fixture.Exec("CREATE SCHEMA ir_moved", 0))
		run(fixture.Exec("ALTER FUNCTION ir_renamed(integer) SET SCHEMA ir_moved", 0))
		row[1], row[4] = "ir_moved", "ir_moved"
		held(step(row, other))
		// Actual declarations, before any explicit ACL, are the persistence authority.
		if !b.PG {
			snapshot, err := c.QueryContext(p.Ctx, step().Query)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			types, err := snapshot.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			original := [][]any{append([]any(nil), row...), append([]any(nil), other...)}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if dump.Len() == 0 {
				t.Fatal("empty dump")
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			// Native catalog replacement retires domain-result statement signatures.
			stale := fixture.ErrorDiagnostic(step().Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
			stale.SelectRows, stale.ErrorStage = true, "execute"
			held(stale)
			held = rsuHeld(t, p, c, b.Recorder, step().Query)
			held(step())
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			held(stale)
			held = rsuHeld(t, p, c, b.Recorder, step().Query)
			var retained [][]any
			for snapshot.Next() {
				values := make([]any, 82)
				dest := make([]any, 82)
				for i := range values {
					dest[i] = &values[i]
				}
				if err := snapshot.Scan(dest...); err != nil {
					t.Fatal(err)
				}
				retained = append(retained, values)
			}
			if err := snapshot.Err(); err != nil {
				t.Fatal(err)
			}
			if err := snapshot.Close(); err != nil {
				t.Fatal(err)
			}
			if len(retained) != 2 {
				t.Fatalf("retained=%v", retained)
			}
			for _, expected := range original {
				count := 0
				for _, actual := range retained {
					if reflect.DeepEqual(actual, expected) {
						count++
					}
				}
				if count != 1 {
					t.Fatalf("retained=%v want=%v", retained, original)
				}
			}
			irMetadata(t, types, false)
			row[2] = rsuIdentity(t, p, c, "ir_moved", "ir_renamed", "integer")
			other[2] = rsuIdentity(t, p, c, "public", "ir_life", "text")
			held(step(row, other))
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("ir_fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			freshConn, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer freshConn.Close()
			var freshDB string
			if err := freshConn.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&freshDB); err != nil {
				t.Fatal(err)
			}
			freshRows := [][]any{append([]any(nil), row...), append([]any(nil), other...)}
			for _, v := range freshRows {
				v[0], v[3], v[28] = freshDB, freshDB, freshDB
			}
			freshRows[0][2] = rsuIdentity(t, p, freshConn, "ir_moved", "ir_renamed", "integer")
			freshRows[1][2] = rsuIdentity(t, p, freshConn, "public", "ir_life", "text")
			fixture.RunStep(t, p.Ctx, freshConn, recorder, irProjection(false, where, freshRows))
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(ctx, step().Query)
			if rows != nil || !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatalf("canceled rows=%v error=%v", rows, err)
			}
		}
		run(fixture.Exec("CREATE ROLE ir_owner", 0))
		run(fixture.Exec("CREATE ROLE ir_reader", 0))
		run(fixture.Exec("ALTER FUNCTION ir_moved.ir_renamed(integer) OWNER TO ir_owner", 0))
		held(step(row, other))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ir_moved.ir_renamed(integer),public.ir_life(text) FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE ir_reader", 0))
		held(step())
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ir_moved.ir_renamed(integer) TO ir_reader", 0))
		run(fixture.Exec("SET ROLE ir_reader", 0))
		redacted := append([]any(nil), row...)
		redacted[37] = nil
		held(step(redacted))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION ir_moved.ir_renamed(integer) FROM ir_reader", 0))
		run(fixture.Exec("SET ROLE ir_reader", 0))
		held(step())
		run(fixture.Exec("RESET ROLE", 0))
		held(step(row, other))
	})
}
