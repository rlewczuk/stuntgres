package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangeTypeCapturedMemberOrdering(t *testing.T) {
	for _, order := range []string{"reverse", "nonmonotone"} {
		for _, form := range []string{"expanded", "variadic"} {
			t.Run(order+"_"+form, func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "ctor_order")
				fixture.SchemaReferenceCleanup(t, "ctor_order_padding")
				rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
					for _, q := range []string{
						"CREATE SCHEMA ctor_order", "CREATE SCHEMA ctor_order_padding",
						"CREATE TABLE ctor_order.u(k integer)", "CREATE TABLE ctor_order.x(k integer)", "CREATE TABLE ctor_order.y(k integer)", "CREATE TABLE ctor_order.z(k integer)", "CREATE TABLE ctor_order.w(k integer)",
						"CREATE TYPE ctor_order_span AS RANGE(SUBTYPE=regclass,SUBTYPE_OPCLASS=pg_catalog.oid_ops,MULTIRANGE_TYPE_NAME=ctor_order_many)",
						"CREATE DOMAIN ctor_order_domain AS ctor_order_many",
						"CREATE TYPE ctor_order_record AS(m ctor_order_many,a ctor_order_domain[])",
					} {
						run(fixture.Exec(q, 0))
					}
					endpoint := func(name string) string { return "'" + name + "'::regclass" }
					span := func(lo, hi string) string {
						return "public.ctor_order_span(" + endpoint(lo) + "," + endpoint(hi) + ",'[]')"
					}
					left, right, third := span("pg_catalog.pg_class", "ctor_order.u"), span("ctor_order.x", "ctor_order.y"), span("ctor_order.z", "ctor_order.w")
					members := []string{right, left}
					count := int64(2)
					want := "{[pg_class,ctor_order.u],[ctor_order.x,ctor_order.y]}"
					if order == "nonmonotone" {
						members = append(members, third)
						count = 3
						want = "{[pg_class,ctor_order.u],[ctor_order.x,ctor_order.y],[ctor_order.z,ctor_order.w]}"
					}
					args := strings.Join(members, ",")
					if form == "variadic" {
						args = "VARIADIC ARRAY[" + args + "]"
					}
					recipe := "public.ctor_order_many(" + args + ")"
					nested := "ARRAY[" + recipe + "::ctor_order_domain]"
					record := "ROW(" + recipe + "," + nested + ")::ctor_order_record"
					run(fixture.Exec("CREATE VIEW ctor_order_view AS SELECT "+recipe+" AS m,"+nested+" AS a,"+record+" AS c,"+recipe+" AS g", 0))
					run(fixture.Exec("CREATE TABLE ctor_order_sink(k integer,m ctor_order_many DEFAULT "+recipe+",a ctor_order_domain[] DEFAULT "+nested+",c ctor_order_record DEFAULT "+record+",g ctor_order_many GENERATED ALWAYS AS ("+recipe+") STORED)", 0))
					// No source row may supply the missing member-gap edge. Only the stored
					// view/default/generated recipes carry these multirange relationships.
					run(fixture.Select("SELECT count(*) AS n FROM ctor_order_sink", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
					var upperID, nextID int64
					if err := c.QueryRowContext(p.Ctx, "SELECT 'ctor_order.u'::regclass::oid::bigint,'ctor_order.x'::regclass::oid::bigint").Scan(&upperID, &nextID); err != nil {
						t.Fatal(err)
					}
					orderSQL := "'pg_catalog.pg_class'::regclass::oid<'ctor_order.u'::regclass::oid AND 'ctor_order.u'::regclass::oid<'ctor_order.x'::regclass::oid AND 'ctor_order.x'::regclass::oid<'ctor_order.y'::regclass::oid"
					if order == "nonmonotone" {
						orderSQL += " AND 'ctor_order.y'::regclass::oid<'ctor_order.z'::regclass::oid AND 'ctor_order.z'::regclass::oid<'ctor_order.w'::regclass::oid"
					}
					ordered := fixture.Select("SELECT "+orderSQL+" AS ordered", []string{"ordered"}, []string{"BOOL"}, [][]any{{true}})
					selectValue := func(table, where string) fixture.Step {
						return fixture.Select("SELECT m::text AS m,a[1]::text AS a,(c).m::text AS cm,((c).a)[1]::text AS ca,g::text AS g FROM "+table+where, []string{"m", "a", "cm", "ca", "g"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{want, want, want, want, want}})
					}
					check := func(backend fixture.Backend, executor fixture.Executor, table, where string) {
						rangeTypeRemedyRoutes(t, p, backend, executor, selectValue(table, where))
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT count(*) AS n FROM "+table+" CROSS JOIN LATERAL unnest(m) AS members(v)"+where, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
						fixture.RunStep(t, p.Ctx, executor, backend.Recorder, ordered)
					}
					check(b, c, "ctor_order_view", "")
					heldStatement, err := c.PrepareContext(p.Ctx, selectValue("ctor_order_view", "").Query)
					if err != nil {
						t.Fatal(err)
					}
					defer heldStatement.Close()
					if !b.PG {
						held, err := heldStatement.QueryContext(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer held.Close()
						var script bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
							t.Fatal(err)
						}
						target := fixture.NewFixture(t, false, true)
						owner, err := target.Memory.Conn(target.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer owner.Close()
						targetRun := func(step fixture.Step) { fixture.RunStep(t, target.Ctx, owner, target.MemoryRecord, step) }
						for _, q := range []string{"CREATE TEMP TABLE ctor_collision(k integer)", "INSERT INTO ctor_collision VALUES(7)", "CREATE TABLE ctor_target_padding(k integer)"} {
							rows := int64(0)
							if strings.HasPrefix(q, "INSERT") {
								rows = 1
							}
							targetRun(fixture.Exec(q, rows))
						}
						// A real TEMP collision hits U. Ordinary persistent padding advances the
						// actual target frontier past X without making X a retained TEMP anchor.
						targetRun(fixture.Select("SELECT 'pg_temp.ctor_collision'::regclass::oid::bigint=$1::bigint AS upper_collision,'ctor_target_padding'::regclass::oid::bigint=$2::bigint AS frontier_past_next", []string{"upper_collision", "frontier_past_next"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, upperID, nextID))
						live := fixture.Select("SELECT k FROM ctor_collision", []string{"k"}, []string{"INT4"}, [][]any{{int64(7)}})
						liveRows, err := owner.QueryContext(target.Ctx, live.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer liveRows.Close()
						if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script.Bytes())); err != nil {
							t.Fatal("definition-only captured member Restore", err)
						}
						dest := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
						check(dest, owner, "ctor_order_view", "")
						targetRun(fixture.Select("SELECT count(*) AS n FROM ctor_order_sink", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
						destinationStatement, err := owner.PrepareContext(target.Ctx, selectValue("ctor_order_view", "").Query)
						if err != nil {
							t.Fatal(err)
						}
						defer destinationStatement.Close()
						targetRun(fixture.Prepared(fixture.Exec("INSERT INTO ctor_order_sink(k) VALUES(1)", 1)))
						check(dest, owner, "ctor_order_sink", " WHERE k=1")
						var redump bytes.Buffer
						if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(redump.Bytes())); err != nil {
							t.Fatal("captured members redump replacement", err)
						}
						check(dest, owner, "ctor_order_view", "")
						check(dest, owner, "ctor_order_sink", " WHERE k=1")
						again, err := destinationStatement.QueryContext(target.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						rangeConsumeHeld(t, again, selectValue("ctor_order_view", ""))
						targetRun(fixture.Exec("INSERT INTO ctor_order_sink(k) VALUES(2)", 1))
						check(dest, owner, "ctor_order_sink", " WHERE k=2")
						targetRun(live)
						rangeConsumeHeld(t, liveRows, live)
						rangeConsumeHeld(t, held, selectValue("ctor_order_view", ""))
						var unchanged bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &unchanged); err != nil || !bytes.Equal(script.Bytes(), unchanged.Bytes()) {
							t.Fatal("definition mapping changed source", err)
						}
						if p.Postgres != nil {
							func() {
								oracle, err := p.Postgres.Conn(p.Ctx)
								if err != nil {
									t.Fatal(err)
								}
								defer oracle.Close()
								clear := "DROP SCHEMA IF EXISTS ctor_order CASCADE;DROP SCHEMA IF EXISTS ctor_order_padding CASCADE;DROP SCHEMA public CASCADE;CREATE SCHEMA public;"
								// Cleanup is installed before replay can change session authorization.
								defer func() {
									cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
									defer cancel()
									if _, err := oracle.ExecContext(cleanup, "SET SESSION AUTHORIZATION DEFAULT;RESET ROLE;"+clear); err != nil {
										t.Error("captured definition oracle cleanup", err)
									}
								}()
								if _, err := oracle.ExecContext(p.Ctx, clear+redump.String()); err != nil {
									t.Fatal("actual captured-definition dump PostgreSQL replay", err)
								}
								real := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
								check(real, oracle, "ctor_order_view", "")
								check(real, oracle, "ctor_order_sink", " WHERE k=1")
							}()
						}
					}
					retained, err := heldStatement.QueryContext(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					rangeConsumeHeld(t, retained, selectValue("ctor_order_view", ""))
					// This shared default evaluation occurs only after the first native import,
					// so its materialized members cannot accidentally certify that import.
					run(fixture.Prepared(fixture.Exec("INSERT INTO ctor_order_sink(k) VALUES(1)", 1)))
					check(b, c, "ctor_order_sink", " WHERE k=1")
				})
			})
		}
	}
}
