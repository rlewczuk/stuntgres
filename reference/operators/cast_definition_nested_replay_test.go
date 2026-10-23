package reference_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Each carrier/cardinality has an isolated restore: a scalar snapshot receiver
// failure must not hide the bounded outer-array renderer's independent failure.
func TestReferenceCastDefinitionNestedConstructorReplay(t *testing.T) {
	for _, carrier := range []string{"outer_array", "scalar_composite"} {
		for _, many := range []bool{false, true} {
			name := carrier + "/one"
			if many {
				name = carrier + "/many"
			}
			t.Run(name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						conn, err := backend.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer conn.Close()
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
						run(fixture.Exec("CREATE TYPE replay_leaf AS (c circle)", 0))
						run(fixture.Exec("CREATE TYPE replay_outer AS (items replay_leaf[])", 0))

						// No circle input or declared builtin cast is needed. The
						// portable constructor retains a genuinely negative radius.
						inner := "pg_catalog.array_fill(ROW(circle(point '(1,2)',-3))::replay_leaf,ARRAY[1],ARRAY[-2])"
						innerLast := "-2"
						innerHi := int64(-2)
						lastCircle, lastRadius := "<(1,2),-3>", float64(-3)
						if many {
							// NULL composite and non-NULL composite containing NULL
							// are separate identities, even though both satisfy IS NULL.
							inner = "pg_catalog.array_cat(" + inner + ",ARRAY[NULL::replay_leaf,ROW(NULL::circle)::replay_leaf,ROW(circle(point '(4,5)',-7))::replay_leaf])"
							innerLast, innerHi = "1", int64(1)
							lastCircle, lastRadius = "<(4,5),-7>", float64(-7)
						}
						value := "ROW(" + inner + ")::replay_outer"
						columnType := "replay_outer"
						outerHi := int64(0)
						if carrier == "outer_array" {
							columnType = "replay_outer[]"
							n := "1"
							if many {
								n, outerHi = "2", int64(1)
							}
							value = "pg_catalog.array_fill(" + value + ",ARRAY[" + n + "],ARRAY[0])"
						}
						run(fixture.Exec("CREATE TABLE replay_nested(id integer,v "+columnType+")", 0))
						run(fixture.Exec("INSERT INTO replay_nested VALUES (1,"+value+")", 1))
						run(fixture.Exec("CREATE MATERIALIZED VIEW replay_nested_snapshot AS SELECT id,v FROM replay_nested", 1))
						// Keep an observably stale captured snapshot. Recomputing it
						// during native Restore would incorrectly change id from 1 to 2.
						run(fixture.Exec("UPDATE replay_nested SET id=2", 1))

						checks := func(b fixture.Backend, relation string, id int64) []fixture.Step {
							first, last := "(v).items", "(v).items"
							steps := []fixture.Step{}
							if carrier == "outer_array" {
								first = "(v[0]).items"
								lastIndex := "0"
								if many {
									lastIndex = "1"
								}
								last = "(v[" + lastIndex + "]).items"
								steps = append(steps, fixture.Select("SELECT id,array_lower(v,1) AS lo,array_upper(v,1) AS hi FROM "+relation,
									[]string{"id", "lo", "hi"}, []string{"INT4", "INT4", "INT4"}, [][]any{{id, int64(0), outerHi}}))
							}
							steps = append(steps, castNegativeCircleStep(b,
								"SELECT id,array_lower("+first+",1) AS first_lo,array_upper("+first+",1) AS first_hi,array_lower("+last+",1) AS last_lo,array_upper("+last+",1) AS last_hi,(("+first+")[-2]).c AS first_circle,radius((("+first+")[-2]).c) AS first_radius,(("+last+")["+innerLast+"]).c AS last_circle,radius((("+last+")["+innerLast+"]).c) AS last_radius FROM "+relation,
								[]string{"id", "first_lo", "first_hi", "last_lo", "last_hi", "first_circle", "first_radius", "last_circle", "last_radius"},
								[]string{"INT4", "INT4", "INT4", "INT4", "INT4", "CIRCLE", "FLOAT8", "CIRCLE", "FLOAT8"},
								[][]any{{id, int64(-2), innerHi, int64(-2), innerHi, "<(1,2),-3>", float64(-3), lastCircle, lastRadius}}))
							if many {
								steps = append(steps, fixture.Select(
									"SELECT ("+first+")[-1] IS NOT DISTINCT FROM NULL::replay_leaf AS null_composite,("+first+")[0] IS DISTINCT FROM NULL::replay_leaf AS present_composite,(("+first+")[0]).c IS NULL AS null_field,("+last+")[-1] IS NOT DISTINCT FROM NULL::replay_leaf AS last_null_composite,("+last+")[0] IS DISTINCT FROM NULL::replay_leaf AS last_present_composite FROM "+relation,
									[]string{"null_composite", "present_composite", "null_field", "last_null_composite", "last_present_composite"},
									[]string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true}}))
							}
							return steps
						}
						verify := func(b fixture.Backend, run func(fixture.Step), snapshotID int64) {
							for _, relation := range []string{"replay_nested", "replay_nested_snapshot"} {
								id := int64(2)
								if relation == "replay_nested_snapshot" {
									id = snapshotID
								}
								for _, s := range checks(b, relation, id) {
									run(s)
									run(fixture.Prepared(s))
								}
							}
						}
						verify(backend, run, 1)
						for _, s := range checks(backend, "replay_nested_snapshot", 1) {
							stmt, err := conn.PrepareContext(p.Ctx, s.Query)
							if err != nil {
								t.Fatal(err)
							}
							defer stmt.Close()
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: s.Query}, backend.Recorder, s)
							if err := stmt.Close(); err != nil {
								t.Fatal(err)
							}
						}
						// All held statements, rows and the borrowed handle are closed
						// before public native controls replace catalog ownership.
						if err := conn.Close(); err != nil {
							t.Fatal(err)
						}
						if backend.PG {
							return
						}
						var dump bytes.Buffer
						if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatalf("nested constructor Restore: %v\n%s", err, dump.String())
						}
						verify(backend, func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }, 1)
						target := fixture.NewFixture(t, false, true)
						if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatalf("fresh-owner nested constructor Restore: %v", err)
						}
						fresh := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
						verify(fresh, func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }, 1)
						// Neither native verification above performs REFRESH: the stale
						// captured id and nested values must survive both Restore owners.
						if p.Postgres != nil {
							defer func() {
								ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
								defer cancel()
								if _, err := p.Postgres.ExecContext(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
									t.Error("nested replay PostgreSQL cleanup:", err)
								}
							}()
							if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
								t.Fatal(err)
							}
							// Execute the actual unmodified bytes, not a separately
							// generated PostgreSQL setup or a dump-format assertion.
							if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
								t.Fatalf("actual nested constructor SQL PostgreSQL replay: %v\n%s", err, dump.String())
							}
							pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
							pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
							for _, s := range checks(pg, "replay_nested", 2) {
								pgRun(s)
								pgRun(fixture.Prepared(s))
							}
							// PostgreSQL has no native captured-snapshot API. Its
							// definition starts unpopulated; REFRESH must use the real
							// preserved table payload, not private annotation authority.
							pgRun(fixture.MaterializedViewUnpopulated("replay_nested_snapshot"))
							pgRun(fixture.Exec("REFRESH MATERIALIZED VIEW replay_nested_snapshot", 0))
							verify(pg, pgRun, 2)
						}
					})
				}
			})
		}
	}
}
