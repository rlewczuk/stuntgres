package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceInfoDomainUdtUsageNativeControls(t *testing.T) {
	for _, phase := range []string{"same_engine_restore", "independent_target_restore", "canceled_reads"} {
		t.Run(phase, func(t *testing.T) {
			duScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				duSeed(run)
				for _, q := range []string{"CREATE ROLE du_owner", "CREATE ROLE du_domain_owner", "GRANT USAGE,CREATE ON SCHEMA du_scope TO du_owner,du_domain_owner", "ALTER TYPE du_scope.e OWNER TO du_owner", "ALTER DOMAIN du_scope.de OWNER TO du_domain_owner"} {
					run(fixture.Exec(q, 0))
				}
				s := duProjection(b.PG, "domain_schema='du_scope'", duSeedRows(database))
				held := cduHold(t, p, c, s.Query)
				duCheck(t, p, c, r, held, s)
				duIdentity(t, p, b, c, r)
				run(duProjection(b.PG, duBuiltinWhere, duBuiltinRows(database)))
				run(fixture.Exec("SET SESSION AUTHORIZATION du_owner", 0))
				run(duProjection(b.PG, "domain_schema='du_scope'", [][]any{duSeedRows(database)[3]}))
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				if b.PG {
					return
				} // PostgreSQL has no process-local native controls.
				if phase == "canceled_reads" {
					ctx, cancel := context.WithCancel(p.Ctx)
					cancel()
					for _, x := range []fixture.Executor{c, held} {
						rows, err := x.QueryContext(ctx, s.Query)
						if rows != nil {
							rows.Close()
							t.Fatal("canceled metadata published rows")
						}
						if !errors.Is(err, context.Canceled) {
							t.Fatalf("canceled query=%v", err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("ordinary cancellation exposed SQLSTATE", state.SQLState())
						}
					}
					duCheck(t, p, c, r, held, s)
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if strings.Contains(dump.String(), "CREATE VIEW information_schema.domain_udt_usage") || strings.Contains(dump.String(), `CREATE VIEW "information_schema"."domain_udt_usage"`) {
					t.Fatal("builtin source serialized as user view")
				}
				if phase == "same_engine_restore" {
					retained, err := b.DB.QueryContext(p.Ctx, s.Query)
					if err != nil {
						t.Fatal(err)
					}
					defer retained.Close()
					columns, err := retained.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					duMetadata(t, columns, false)
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					run(duProjection(false, "domain_schema='du_scope'", nil))
					run(duProjection(false, duBuiltinWhere, duBuiltinRows(database)))
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
						t.Fatal(err)
					}
					run(s)
					run(fixture.Prepared(s))
					duIdentity(t, p, b, c, r)
					duRetained(t, retained, false, duSeedRows(database))
					duMetadata(t, columns, false)
					return
				}
				target := fixture.NewFixture(t, false, true)
				// Roles are retained shared authority, not remapped owner aliases.
				for _, role := range []string{"du_owner", "du_domain_owner"} {
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE ROLE "+role, 0))
				}
				for _, query := range []string{"CREATE TABLE public.du_spacer(v integer)", "CREATE DOMAIN public.du_spacer_domain AS text"} {
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(query, 0))
				}
				if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var targetDatabase string
				if err := target.Memory.QueryRowContext(target.Ctx, "SELECT current_database()").Scan(&targetDatabase); err != nil {
					t.Fatal(err)
				}
				targetConn, connErr := target.Memory.Conn(target.Ctx)
				if connErr != nil {
					t.Fatal(connErr)
				}
				defer targetConn.Close()
				checkTarget := func() {
					step := duProjection(false, "domain_schema='du_scope'", duSeedRows(targetDatabase))
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, step)
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(step))
					fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, duProjection(false, duBuiltinWhere, duBuiltinRows(targetDatabase)))
					duIdentity(t, target, b, targetConn, target.MemoryRecord)
					fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec("SET SESSION AUTHORIZATION du_owner", 0))
					fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, duProjection(false, "domain_schema='du_scope'", [][]any{duSeedRows(targetDatabase)[3]}))
					fixture.RunStep(t, target.Ctx, targetConn, target.MemoryRecord, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				}
				checkTarget()
				bad := "CREATE SCHEMA du_staged; CREATE DOMAIN du_staged.d AS integer; CREATE TABLE du_staged.t(v integer); INSERT INTO du_staged.t VALUES('bad');"
				err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad))
				fixture.AssertDiagnostic(t, err, func() *fixture.Diagnostic {
					d := fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)
					d.Position = strconv.Itoa(strings.Index(bad, "'bad'") + 1)
					return &d
				}())
				checkTarget()
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT nspname FROM pg_catalog.pg_namespace WHERE nspname='du_staged'", []string{"nspname"}, []string{"NAME"}, nil))
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, duProjection(false, "domain_schema='du_staged'", nil))
				ctx, cancel := context.WithCancel(target.Ctx)
				cancel()
				err = stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes()))
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("canceled Restore=%v", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary Restore cancellation exposed SQLSTATE", state.SQLState())
				}
				checkTarget()
			})
		})
	}
}
