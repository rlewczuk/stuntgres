package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangePartitionInheritedTypeACL(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rp_type_actor"})
	fixture.SchemaReferenceCleanup(t, "rp_type_schema")
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec("CREATE USER rp_type_actor", 0))
		run(fixture.Exec("CREATE SCHEMA rp_type_schema", 0))
		run(fixture.Exec("CREATE TYPE rp_type_schema.rp_type_state AS ENUM('a','b')", 0))
		run(fixture.Exec("CREATE DOMAIN rp_type_schema.rp_type_score AS INT", 0))
		run(fixture.Exec("CREATE TABLE public.rp_type_parent(k INT,e rp_type_schema.rp_type_state DEFAULT 'a'::rp_type_schema.rp_type_state,d rp_type_schema.rp_type_score DEFAULT 1,CONSTRAINT rp_type_check CHECK(e<>'b'::rp_type_schema.rp_type_state)) PARTITION BY RANGE(k)", 0))
		run(fixture.Prepared(fixture.Exec("CREATE TABLE public.rp_type_baseline_leaf PARTITION OF public.rp_type_parent FOR VALUES FROM(0) TO(5)", 0)))
		run(fixture.Exec("INSERT INTO public.rp_type_parent(k) VALUES(1)", 1))
		run(fixture.Select("SELECT k,e::TEXT AS state,d::INT AS score FROM public.rp_type_baseline_leaf", []string{"k", "state", "score"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(1)}}))
		// SQL GRANT/REVOKE and owner transfer remain PostgreSQL-only controls.
		// Native units exercise the same manifests through existing SetObjectACL.
		if b.PG {
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO rp_type_actor", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA rp_type_schema TO rp_type_actor", 0))
			run(fixture.Exec("REVOKE ALL ON TYPE rp_type_schema.rp_type_state,rp_type_schema.rp_type_score FROM PUBLIC", 0))
			run(fixture.Exec("GRANT USAGE ON TYPE rp_type_schema.rp_type_score TO rp_type_actor", 0))
			run(fixture.Exec("ALTER TABLE public.rp_type_parent OWNER TO rp_type_actor", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rp_type_actor", 0))
			run(fixture.Exec("SET search_path TO rp_type_schema,public", 0))
			for _, prepared := range []bool{false, true} {
				step := referenceRangeError("CREATE TABLE public.rp_type_usage_denied PARTITION OF public.rp_type_parent FOR VALUES FROM(10) TO(20)", "42501", "permission denied for type rp_type_state", fixture.Diagnostic{})
				step.Prepared = prepared
				run(step)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("RESET search_path", 0))
			run(fixture.Exec("GRANT USAGE ON TYPE rp_type_schema.rp_type_state TO rp_type_actor", 0))
			run(fixture.Exec("REVOKE USAGE ON TYPE rp_type_schema.rp_type_score FROM rp_type_actor", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rp_type_actor", 0))
			run(fixture.Exec("SET search_path TO rp_type_schema,public", 0))
			for _, prepared := range []bool{false, true} {
				step := referenceRangeError("CREATE TABLE public.rp_domain_usage_denied PARTITION OF public.rp_type_parent FOR VALUES FROM(10) TO(20)", "42501", "permission denied for type rp_type_score", fixture.Diagnostic{})
				step.Prepared = prepared
				run(step)
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("RESET search_path", 0))
			run(fixture.Exec("GRANT USAGE ON TYPE rp_type_schema.rp_type_score TO rp_type_actor", 0))
			run(fixture.Exec("REVOKE USAGE ON SCHEMA rp_type_schema FROM rp_type_actor", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rp_type_actor", 0))
			run(fixture.Prepared(fixture.Exec("CREATE TABLE public.rp_type_schema_ok PARTITION OF public.rp_type_parent FOR VALUES FROM(10) TO(20)", 0)))
			run(fixture.Prepared(fixture.Exec("INSERT INTO public.rp_type_parent(k) VALUES($1)", 1, 11)))
			run(fixture.Prepared(fixture.Select("SELECT k,e::TEXT AS state,d::INT AS score FROM public.rp_type_schema_ok", []string{"k", "state", "score"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(11), "a", int64(1)}})))
			for _, c := range []struct {
				name, query, position string
			}{
				{"default_cast", "CREATE TABLE public.rp_type_cast_denied PARTITION OF public.rp_type_parent(e DEFAULT 'a'::rp_type_schema.rp_type_state) FOR VALUES FROM(20) TO(30)", "91"},
				{"fresh_check", "CREATE TABLE public.rp_type_check_denied PARTITION OF public.rp_type_parent(CONSTRAINT rp_fresh_check CHECK(e<>'b'::rp_type_schema.rp_type_state)) FOR VALUES FROM(20) TO(30)", "117"},
				{"nested_key", "CREATE TABLE public.rp_type_key_denied PARTITION OF public.rp_type_parent FOR VALUES FROM(20) TO(30) PARTITION BY RANGE((d::rp_type_schema.rp_type_score))", ""},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(c.name+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
						step := referenceRangeError(c.query, "42501", "permission denied for schema rp_type_schema", fixture.Diagnostic{Position: c.position})
						step.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, step)
					})
				}
			}
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='rp_type_usage_denied' OR relname='rp_domain_usage_denied' OR relname='rp_type_cast_denied' OR relname='rp_type_check_denied' OR relname='rp_type_key_denied'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceRangeRows("SELECT k FROM public.rp_type_parent ORDER BY k", 1, 11))
		}
	})
}
