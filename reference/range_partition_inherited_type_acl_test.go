package reference_test

import (
	"context"
	"testing"
	"time"
)

func TestReferenceRangePartitionInheritedTypeACL(t *testing.T) {
	roleReferenceCleanup(t, []string{"rp_type_actor"})
	schemaReferenceCleanup(t, "rp_type_schema")
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		conn, err := b.db.Conn(p.ctx)
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
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE USER rp_type_actor", 0))
		run(referenceExec("CREATE SCHEMA rp_type_schema", 0))
		run(referenceExec("CREATE TYPE rp_type_schema.rp_type_state AS ENUM('a','b')", 0))
		run(referenceExec("CREATE DOMAIN rp_type_schema.rp_type_score AS INT", 0))
		run(referenceExec("CREATE TABLE public.rp_type_parent(k INT,e rp_type_schema.rp_type_state DEFAULT 'a'::rp_type_schema.rp_type_state,d rp_type_schema.rp_type_score DEFAULT 1,CONSTRAINT rp_type_check CHECK(e<>'b'::rp_type_schema.rp_type_state)) PARTITION BY RANGE(k)", 0))
		run(referencePrepared(referenceExec("CREATE TABLE public.rp_type_baseline_leaf PARTITION OF public.rp_type_parent FOR VALUES FROM(0) TO(5)", 0)))
		run(referenceExec("INSERT INTO public.rp_type_parent(k) VALUES(1)", 1))
		run(referenceSelect("SELECT k,e::TEXT AS state,d::INT AS score FROM public.rp_type_baseline_leaf", []string{"k", "state", "score"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "a", int64(1)}}))
		// SQL GRANT/REVOKE and owner transfer remain PostgreSQL-only controls.
		// Native units exercise the same manifests through existing SetObjectACL.
		if b.pg {
			run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO rp_type_actor", 0))
			run(referenceExec("GRANT USAGE ON SCHEMA rp_type_schema TO rp_type_actor", 0))
			run(referenceExec("REVOKE ALL ON TYPE rp_type_schema.rp_type_state,rp_type_schema.rp_type_score FROM PUBLIC", 0))
			run(referenceExec("GRANT USAGE ON TYPE rp_type_schema.rp_type_score TO rp_type_actor", 0))
			run(referenceExec("ALTER TABLE public.rp_type_parent OWNER TO rp_type_actor", 0))
			run(referenceExec("SET SESSION AUTHORIZATION rp_type_actor", 0))
			run(referenceExec("SET search_path TO rp_type_schema,public", 0))
			for _, prepared := range []bool{false, true} {
				step := referenceRangeError("CREATE TABLE public.rp_type_usage_denied PARTITION OF public.rp_type_parent FOR VALUES FROM(10) TO(20)", "42501", "permission denied for type rp_type_state", referenceDiagnostic{})
				step.prepared = prepared
				run(step)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("RESET search_path", 0))
			run(referenceExec("GRANT USAGE ON TYPE rp_type_schema.rp_type_state TO rp_type_actor", 0))
			run(referenceExec("REVOKE USAGE ON TYPE rp_type_schema.rp_type_score FROM rp_type_actor", 0))
			run(referenceExec("SET SESSION AUTHORIZATION rp_type_actor", 0))
			run(referenceExec("SET search_path TO rp_type_schema,public", 0))
			for _, prepared := range []bool{false, true} {
				step := referenceRangeError("CREATE TABLE public.rp_domain_usage_denied PARTITION OF public.rp_type_parent FOR VALUES FROM(10) TO(20)", "42501", "permission denied for type rp_type_score", referenceDiagnostic{})
				step.prepared = prepared
				run(step)
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("RESET search_path", 0))
			run(referenceExec("GRANT USAGE ON TYPE rp_type_schema.rp_type_score TO rp_type_actor", 0))
			run(referenceExec("REVOKE USAGE ON SCHEMA rp_type_schema FROM rp_type_actor", 0))
			run(referenceExec("SET SESSION AUTHORIZATION rp_type_actor", 0))
			run(referencePrepared(referenceExec("CREATE TABLE public.rp_type_schema_ok PARTITION OF public.rp_type_parent FOR VALUES FROM(10) TO(20)", 0)))
			run(referencePrepared(referenceExec("INSERT INTO public.rp_type_parent(k) VALUES($1)", 1, 11)))
			run(referencePrepared(referenceSelect("SELECT k,e::TEXT AS state,d::INT AS score FROM public.rp_type_schema_ok", []string{"k", "state", "score"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(11), "a", int64(1)}})))
			for _, c := range []struct {
				name, query, position string
			}{
				{"default_cast", "CREATE TABLE public.rp_type_cast_denied PARTITION OF public.rp_type_parent(e DEFAULT 'a'::rp_type_schema.rp_type_state) FOR VALUES FROM(20) TO(30)", "91"},
				{"fresh_check", "CREATE TABLE public.rp_type_check_denied PARTITION OF public.rp_type_parent(CONSTRAINT rp_fresh_check CHECK(e<>'b'::rp_type_schema.rp_type_state)) FOR VALUES FROM(20) TO(30)", "117"},
				{"nested_key", "CREATE TABLE public.rp_type_key_denied PARTITION OF public.rp_type_parent FOR VALUES FROM(20) TO(30) PARTITION BY RANGE((d::rp_type_schema.rp_type_score))", ""},
			} {
				for _, prepared := range []bool{false, true} {
					t.Run(c.name+"/"+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
						step := referenceRangeError(c.query, "42501", "permission denied for schema rp_type_schema", referenceDiagnostic{Position: c.position})
						step.prepared = prepared
						referenceRunStep(t, p.ctx, conn, b.recorder, step)
					})
				}
			}
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceSelect("SELECT relname FROM pg_class WHERE relname='rp_type_usage_denied' OR relname='rp_domain_usage_denied' OR relname='rp_type_cast_denied' OR relname='rp_type_check_denied' OR relname='rp_type_key_denied'", []string{"relname"}, []string{"NAME"}, nil))
			run(referenceRangeRows("SELECT k FROM public.rp_type_parent ORDER BY k", 1, 11))
		}
	})
}
