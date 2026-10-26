package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateCombineDiagnostics(t *testing.T) {
	const internal = "SFUNC=pg_catalog.string_agg_combine,STYPE=internal"
	for _, tc := range []struct {
		name, args, options, code, message, detail string
	}{
		{"serial_only", "internal", internal + ",SERIALFUNC=pg_catalog.string_agg_serialize", "42P13", "must specify both or neither of serialization and deserialization functions", ""},
		{"deserial_only", "internal", internal + ",DESERIALFUNC=pg_catalog.string_agg_deserialize", "42P13", "must specify both or neither of serialization and deserialization functions", ""},
		{"noninternal_codec", "integer", "SFUNC=pg_catalog.int4pl,STYPE=integer,SERIALFUNC=pg_catalog.string_agg_serialize,DESERIALFUNC=pg_catalog.string_agg_deserialize", "42P13", "serialization functions may be specified only when the aggregate transition data type is internal", ""},
		{"serial_arguments", "internal", internal + ",SERIALFUNC=pg_catalog.numeric_avg_deserialize,DESERIALFUNC=pg_catalog.string_agg_deserialize", "42883", "function pg_catalog.numeric_avg_deserialize(internal) does not exist", ""},
		{"deserial_arguments", "internal", internal + ",SERIALFUNC=pg_catalog.string_agg_serialize,DESERIALFUNC=pg_catalog.string_agg_serialize", "42883", "function pg_catalog.string_agg_serialize(bytea, internal) does not exist", ""},
		{"combine_arguments", "internal", internal + ",COMBINEFUNC=pg_catalog.int4larger", "42883", "function pg_catalog.int4larger(internal, internal) does not exist", ""},
		{"combine_result", "internal", internal + ",COMBINEFUNC=pg_catalog.inet_gist_union", "42804", "return type of combine function pg_catalog.inet_gist_union is not internal", ""},
		{"serial_result", "internal", internal + ",SERIALFUNC=pg_catalog.string_agg_finalfn,DESERIALFUNC=pg_catalog.string_agg_deserialize", "42804", "return type of serialization function pg_catalog.string_agg_finalfn is not bytea", ""},
		{"strict_internal_combine", "internal", internal + ",COMBINEFUNC=pg_catalog.gist_box_picksplit", "42P13", "combine function with transition type internal must not be declared STRICT", ""},
		{"sort_zero", "*", "SFUNC=ac_zero,STYPE=integer,SORTOP = >", "42P13", "sort operator can only be specified for single-argument aggregates", ""},
		{"sort_two", "integer,integer", "SFUNC=ac_pair,STYPE=integer,SORTOP = >", "42P13", "sort operator can only be specified for single-argument aggregates", ""},
		{"sort_signature", "integer", "SFUNC=pg_catalog.int4larger,STYPE=integer,SORTOP = ~~", "42883", "operator does not exist: integer ~~ integer", ""},
		{"parallel_value", "integer", "SFUNC=pg_catalog.int4larger,STYPE=integer,PARALLEL=invalid", "42601", "parameter \"parallel\" must be SAFE, RESTRICTED, or UNSAFE", ""},
		{"unsafe_internal_result", "text", "SFUNC=pg_catalog.string_agg_transfn,STYPE=internal", "42883", "function pg_catalog.string_agg_transfn(internal, text) does not exist", ""},
		{"unsafe_internal_result_valid_support", "text,text", "SFUNC=pg_catalog.string_agg_transfn,STYPE=internal,COMBINEFUNC=pg_catalog.string_agg_combine,SERIALFUNC=pg_catalog.string_agg_serialize,DESERIALFUNC=pg_catalog.string_agg_deserialize", "42P13", "unsafe use of pseudo-type \"internal\"", "A result of type internal requires at least one input of type internal."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				createAggregateRun(t, p, b, c,
					fixture.Exec("CREATE FUNCTION ac_zero(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$", 0),
					fixture.Exec("CREATE FUNCTION ac_pair(integer,integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)+coalesce($3,0)$$", 0))
				q := "/* é */ CREATE AGGREGATE ac_rejected(" + tc.args + ") (" + tc.options + ")"
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Detail: tc.detail})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ac_rejected'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
				// A corrected same-name definition proves failed DDL did not publish.
				createAggregateRun(t, p, b, c, fixture.Exec("CREATE AGGREGATE ac_rejected(integer) (SFUNC=pg_catalog.int4larger,STYPE=integer,COMBINEFUNC=pg_catalog.int4larger,PARALLEL=SAFE)", 0))
				createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_rejected(x) AS v FROM(VALUES(1),(NULL),(4),(2)) s(x)", int64(4)))
			})
		})
	}
	t.Run("non_superuser_internal", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ac_internal_creator"})
		aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE ROLE ac_internal_creator", 0),
				fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO ac_internal_creator", 0),
				fixture.Exec("SET ROLE ac_internal_creator", 0))
			defer c.ExecContext(context.Background(), "RESET ROLE")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(aggregateCombineInternalDDL, fixture.Diagnostic{Code: "42P13", Message: "aggregate transition data type cannot be internal"})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			createAggregateRun(t, p, b, c,
				fixture.Exec("RESET ROLE", 0),
				fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ac_internal'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}),
				fixture.Exec(aggregateCombineInternalDDL, 0))
		})
	})
	t.Run("domain_initial_condition", func(t *testing.T) {
		aggregateCombineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE DOMAIN ac_seed_state AS integer CONSTRAINT nonnegative CHECK(VALUE>=0)", 0),
				fixture.Exec("CREATE FUNCTION ac_seed_step(ac_seed_state,integer) RETURNS ac_seed_state LANGUAGE SQL AS $$SELECT coalesce($1,0)::ac_seed_state$$", 0),
				fixture.Exec("CREATE FUNCTION ac_seed_merge(ac_seed_state,ac_seed_state) RETURNS ac_seed_state LANGUAGE SQL AS $$SELECT coalesce($1,$2)::ac_seed_state$$", 0))
			const bad = "CREATE AGGREGATE ac_seed(integer) (SFUNC=ac_seed_step,STYPE=ac_seed_state,COMBINEFUNC=ac_seed_merge,INITCOND='-1',PARALLEL=SAFE)"
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(bad, fixture.Diagnostic{Code: "23514", Message: "value for domain ac_seed_state violates check constraint \"nonnegative\"", Schema: "public", DataType: "ac_seed_state", Constraint: "nonnegative"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='ac_seed'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			}
			createAggregateRun(t, p, b, c,
				fixture.Exec("CREATE AGGREGATE ac_seed(integer) (SFUNC=ac_seed_step,STYPE=ac_seed_state,COMBINEFUNC=ac_seed_merge,INITCOND='1',PARALLEL=SAFE)", 0),
				fixture.Exec("CREATE TABLE ac_seed_input(x integer)", 0),
				fixture.Exec("INSERT INTO ac_seed_input VALUES(1),(2)", 2),
				fixture.Exec("SET max_parallel_workers_per_gather=2", 0))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_seed(x)::integer AS v FROM ac_seed_input", int64(1)))
			createAggregateBoth(t, p, b, c, createAggregateValue("SELECT ac_seed(x)::integer AS v FROM ac_seed_input WHERE FALSE", int64(1)))
		})
	})
}
