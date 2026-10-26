package reference_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoSchemataNativeControls(t *testing.T) {
	for _, phase := range []string{"same_engine_restore", "independent_target_restore", "canceled_reads"} {
		t.Run(phase, func(t *testing.T) {
			iscScenario(t, func(c *iscCase) {
				c.exec("CREATE ROLE isc_owner", "CREATE SCHEMA isc_owned AUTHORIZATION isc_owner")
				q := iscQuery + " WHERE schema_name='isc_owned'"
				s := c.projection(q, c.row("isc_owned", "isc_owner"))
				c.routes(s)
				c.identity()
				if c.b.PG {
					return
				} // PostgreSQL has no native Reset/Dump/Restore API.
				if phase == "canceled_reads" {
					held := c.hold(q)
					ctx, cancel := context.WithCancel(c.p.Ctx)
					cancel()
					for _, x := range []fixture.Executor{c.conn, held} {
						rows, err := x.QueryContext(ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled query published rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("canceled query=%v", err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
						}
					}
					c.routes(s)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(c.p.Ctx, c.p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if phase == "same_engine_restore" {
					retained, err := c.conn.QueryContext(c.p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					columns, err := retained.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					iscMetadata(t, columns, false)
					c.exec("DROP SCHEMA isc_owned")
					c.routes(c.projection(q, nil))
					if err := stuntgres.Reset(c.p.Ctx, c.p.Native); err != nil {
						t.Fatal(err)
					}
					c.routes(c.projection(q, nil))
					c.identity()
					if err := stuntgres.Restore(c.p.Ctx, c.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					c.routes(s)
					c.identity()
					c.consume(retained, c.row("isc_owned", "isc_owner"))
					iscMetadata(t, columns, false)
					return
				}
				target := fixture.NewFixture(t, false, true)
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE isc_owner", 0))
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var database string
				if err := target.Memory.QueryRowContext(target.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					t.Fatal(err)
				}
				conn, err := target.Memory.Conn(target.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				tc := &iscCase{t: t, p: target, b: fixture.Backend{Recorder: target.MemoryRecord}, conn: conn, database: database}
				check := func() { tc.routes(tc.projection(q, tc.row("isc_owned", "isc_owner"))); tc.identity(); c.routes(s) }
				check()
				err = stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE SCHEMA isc_staged; CREATE TABLE isc_staged.t(v integer); INSERT INTO isc_staged.t VALUES('bad');"))
				want := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
				want.Position = "97"
				fixture.AssertDiagnostic(t, err, &want)
				tc.routes(tc.projection(iscQuery+" WHERE schema_name='isc_staged'", nil))
				check()
				ctx, cancel := context.WithCancel(target.Ctx)
				cancel()
				if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled Restore=%v", err)
				}
				check()
			})
		})
	}
}
