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

func TestReferenceInfoParametersLifetime(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, db string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		where := "specific_schema IN ('public','ip_moved')"
		step := func(rows [][]any) fixture.Step { return ipProjection(b.PG, where, rows) }
		held := rsuHeld(t, p, c, b.Recorder, step(nil).Query)
		held(step(nil))
		id := ipCreateMix(t, p, b, c)
		want := ipMix(db, "public", id, "9")
		held(step(want))
		run(fixture.Exec("CREATE OR REPLACE FUNCTION ip_mix(IN a integer,OUT b text,INOUT c integer DEFAULT 11) LANGUAGE SQL AS 'SELECT a::text,c'", 0))
		want = ipMix(db, "public", id, "11")
		held(step(want))
		run(fixture.Exec("ALTER FUNCTION ip_mix(integer,integer) RENAME TO ip_renamed", 0))
		renamed := rsuIdentity(t, p, c, "public", "ip_renamed", "integer, integer")
		want = ipMix(db, "public", renamed, "11")
		held(step(want))
		run(fixture.Exec("CREATE SCHEMA ip_moved", 0))
		run(fixture.Exec("ALTER FUNCTION ip_renamed(integer,integer) SET SCHEMA ip_moved", 0))
		want = ipMix(db, "ip_moved", renamed, "11")
		held(step(want))
		if !b.PG {
			snapshot, err := c.QueryContext(p.Ctx, step(nil).Query)
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			types, err := snapshot.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
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
			stale := fixture.ErrorDiagnostic(step(nil).Query, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
			stale.SelectRows, stale.ErrorStage = true, "execute"
			held(stale)
			irCheck(t, p, b, c, step(nil))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			held(stale)
			var retained [][]any
			for snapshot.Next() {
				values := make([]any, 32)
				dest := make([]any, 32)
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
			snapshot.Close()
			ipMetadata(t, types, false)
			if !reflect.DeepEqual(retained, want) {
				t.Fatalf("retained=%v want=%v", retained, want)
			}
			renamed = rsuIdentity(t, p, c, "ip_moved", "ip_renamed", "integer, integer")
			want = ipMix(db, "ip_moved", renamed, "11")
			held = rsuHeld(t, p, c, b.Recorder, step(nil).Query)
			held(step(want))
			driver := stuntgres.NewDriver()
			connector, err := driver.OpenConnector("ip_fresh")
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.RecordedNativePool(t, connector)
			fc, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fc.Close()
			var freshDB string
			if err := fc.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&freshDB); err != nil {
				t.Fatal(err)
			}
			freshID := rsuIdentity(t, p, fc, "ip_moved", "ip_renamed", "integer, integer")
			fixture.RunStep(t, p.Ctx, fc, recorder, ipProjection(false, where, ipMix(freshDB, "ip_moved", freshID, "11")))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := c.QueryContext(canceled, step(nil).Query)
			if rows != nil || !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatalf("canceled rows=%v error=%v", rows, err)
			}
		}
		run(fixture.Exec("CREATE ROLE ip_reader", 0))
		run(fixture.Exec("REVOKE ALL ON FUNCTION ip_moved.ip_renamed(integer,integer) FROM PUBLIC", 0))
		run(fixture.Exec("SET ROLE ip_reader", 0))
		held(step(nil))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION ip_moved.ip_renamed(integer,integer) TO ip_reader", 0))
		run(fixture.Exec("SET ROLE ip_reader", 0))
		held(step(ipMix(db, "ip_moved", renamed, nil)))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("DROP FUNCTION ip_moved.ip_renamed(integer,integer)", 0))
		held(step(nil))
	})
}
