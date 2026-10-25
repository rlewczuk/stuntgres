package reference_test

import (
	"context"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAggregateMutationPreflight(t *testing.T) {
	const role = "pga_preflight_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
			for _, q := range []string{
				"CREATE ROLE " + role,
				"CREATE TABLE public.pga_authority_sentinel(v INT)",
				"INSERT INTO public.pga_authority_sentinel VALUES(7)",
				"GRANT SELECT ON public.pga_authority_sentinel TO " + role,
				"ALTER DEFAULT PRIVILEGES GRANT USAGE,SELECT ON SEQUENCES TO " + role,
				"CREATE SEQUENCE public.pga_authority_effect",
				"ALTER DEFAULT PRIVILEGES REVOKE USAGE,SELECT ON SEQUENCES FROM " + role,
			} {
				n := int64(0)
				if q == "INSERT INTO public.pga_authority_sentinel VALUES(7)" {
					n = 1
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, n))
			}
			if b.PG {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO "+role, 0))
			} else {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SCHEMA public OWNER TO "+role, 0))
			}
			for _, family := range []struct {
				name, sql string
				rows      bool
			}{
				{"insert", "INSERT INTO pg_catalog.pg_aggregate(aggtransspace) VALUES(%s)", false},
				{"update", "UPDATE pg_catalog.pg_aggregate SET aggtransspace=%s WHERE FALSE", false},
				{"delete", "DELETE FROM pg_catalog.pg_aggregate WHERE aggtransspace=%s", false},
				{"merge", "MERGE INTO pg_catalog.pg_aggregate a USING (SELECT 2803 AS id) s ON a.aggfnoid::oid=s.id WHEN MATCHED THEN UPDATE SET aggtransspace=%s", false},
				{"cte_update", "WITH denied AS (UPDATE pg_catalog.pg_aggregate SET aggtransspace=%s WHERE FALSE RETURNING aggfnoid) SELECT * FROM denied", true},
				{"cte_delete", "WITH denied AS (DELETE FROM pg_catalog.pg_aggregate WHERE aggtransspace=%s RETURNING aggfnoid) SELECT * FROM denied", true},
			} {
				for _, conversion := range []struct {
					name, expression         string
					parameter, portalContext bool
				}{
					{"constant", "CAST('oops'::text AS int4)", false, false},
					// A good imported frame must reach ACL admission without
					// evaluating the volatile sibling; a bad TEXT frame must
					// first fail its nested, effect-free integer conversion.
					{"text_frame", "CAST($1::text AS int4)+nextval('public.pga_authority_effect')::int4", true, false},
					{"integer_frame", "$1::int4+nextval('public.pga_authority_effect')::int4", true, true},
				} {
					t.Run(family.name+"/"+conversion.name, func(t *testing.T) {
						q := fmt.Sprintf(family.sql, conversion.expression)
						// Retain a command prepared under the superuser, then
						// exercise it under the current ordinary role.
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal("planning conversion must not fail Prepare", err)
						}
						defer stmt.Close()
						held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
						fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE "+role, 0))
						defer c.ExecContext(context.Background(), "RESET ROLE")
						for _, route := range []struct {
							name     string
							executor fixture.Executor
							prepared bool
						}{
							{"direct", c, false}, {"fresh", c, true}, {"held", held, false},
						} {
							t.Run(route.name, func(t *testing.T) {
								frames := []any{"oops"}
								if conversion.parameter {
									frames = []any{"oops", "42", nil, "oops"}
								}
								for _, frame := range frames {
									want := fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "oops"`}
									if conversion.parameter && frame != "oops" {
										want = fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_aggregate"}
									} else if conversion.portalContext {
										want.Context = "unnamed portal parameter $1 = '...'"
									}
									s := fixture.ImplicitError(q, want)
									s.SelectRows, s.Prepared, s.ErrorStage = family.rows, route.prepared, "execute"
									if conversion.parameter {
										s.Args = []any{frame}
									}
									fixture.RunStep(t, p.Ctx, route.executor, b.Recorder, s)
									pgAggregateSentinel(t, p, b, c)
								}
							})
						}
					})
				}
			}
		})
	}
}
