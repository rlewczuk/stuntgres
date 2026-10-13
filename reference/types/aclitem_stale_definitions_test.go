package reference_test

import (
	"database/sql"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceACLItemStaleDefinitions(t *testing.T) {
	aclitemScenario(t, []string{"acl_stale_definition_a", "acl_stale_definition_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name string
			ddl  []string
		}{
			{"default", []string{"CREATE TABLE public.acl_stale_definition (a aclitem DEFAULT 'acl_stale_definition_a=r/acl_stale_definition_b')"}},
			{"view", []string{"CREATE VIEW public.acl_stale_definition_view AS SELECT 'acl_stale_definition_a=r/acl_stale_definition_b'::aclitem AS a"}},
			{"check", []string{"CREATE TABLE public.acl_stale_definition (a aclitem,CHECK (a='acl_stale_definition_a=r/acl_stale_definition_b'::aclitem))"}},
			{"index", []string{"CREATE TABLE public.acl_stale_definition (a aclitem)", "CREATE INDEX acl_stale_definition_index ON public.acl_stale_definition ((CASE WHEN a='acl_stale_definition_a=r/acl_stale_definition_b'::aclitem THEN 1 ELSE 2 END))"}},
			{"partition", []string{"CREATE TABLE public.acl_stale_definition (id INT) PARTITION BY RANGE ((CASE WHEN 'acl_stale_definition_a=r/acl_stale_definition_b'::aclitem='acl_stale_definition_a=r/acl_stale_definition_b'::aclitem THEN id ELSE id END))"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				defer c.ExecContext(p.Ctx, "DROP VIEW IF EXISTS public.acl_stale_definition_view")
				defer c.ExecContext(p.Ctx, "DROP TABLE IF EXISTS public.acl_stale_definition")
				for _, ddl := range tc.ddl {
					run(fixture.Exec(ddl, 0))
				}
				run(fixture.Exec("DROP ROLE acl_stale_definition_a", 0))
				defer c.ExecContext(p.Ctx, "CREATE ROLE acl_stale_definition_a")
				// There are no stored rows: rejecting stale metadata cannot be
				// accidentally satisfied by inspecting table cells alone.
				if !b.PG {
					writer := &aclitemCountingWriter{}
					err := stuntgres.Dump(p.Ctx, p.Native, writer)
					if err == nil || writer.calls != 0 {
						t.Fatalf("stale %s error=%v writer calls=%d", tc.name, err, writer.calls)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatalf("serialization failure is SQL diagnostic: %v", err)
					}
				}
				run(fixture.Select("SELECT 7 AS sentinel", []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(7)}}))
			})
		}
	})
}
