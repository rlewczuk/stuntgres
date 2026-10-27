package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGProcVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pp_visibility_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			defer b.DB.ExecContext(context.Background(), "DROP SCHEMA IF EXISTS pp_visibility CASCADE")
			pgProcExec(t, p, b, c,
				"CREATE ROLE pp_visibility_reader", "CREATE SCHEMA pp_visibility",
				"CREATE SEQUENCE public.pp_visibility_counter",
				"CREATE FUNCTION pp_visibility.hidden(a bigint DEFAULT nextval('public.pp_visibility_counter')) RETURNS bigint LANGUAGE SQL AS $$SELECT a+nextval('public.pp_visibility_counter')$$",
				"REVOKE ALL ON SCHEMA pp_visibility FROM PUBLIC",
				"REVOKE ALL ON FUNCTION pp_visibility.hidden(bigint) FROM PUBLIC")
			const q = "SELECT p.proname,p.prosrc,p.pronargdefaults,p.proargdefaults IS NOT NULL AS defaults,p.proacl IS NOT NULL AS acl FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pp_visibility' AND p.proname='hidden'"
			check := func() {
				pgProcBoth(t, p, b, c, q, []string{"proname", "prosrc", "pronargdefaults", "defaults", "acl"}, []string{"NAME", "TEXT", "INT2", "BOOL", "BOOL"}, [][]any{{"hidden", "SELECT a+nextval('public.pp_visibility_counter')", int64(1), true, true}})
			}
			check()
			pgProcExec(t, p, b, c, "SET SESSION AUTHORIZATION pp_visibility_reader")
			check() // pg_proc is not information_schema's EXECUTE/USAGE-filtered view.
			pgProcExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			pgProcBoth(t, p, b, c, "SELECT last_value,is_called FROM pp_visibility_counter", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}})
			// Demand the default argument: PostgreSQL may omit evaluation of an
			// unused SQL input. Here default nextval=1 and body nextval=2 sum to3.
			var invoked int64
			if err := c.QueryRowContext(p.Ctx, "SELECT pp_visibility.hidden()").Scan(&invoked); err != nil || invoked != 3 {
				t.Fatalf("real omitted-default/body invocation %d: %v", invoked, err)
			}
			pgProcBoth(t, p, b, c, "SELECT last_value,is_called FROM pp_visibility_counter", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}})
			check()
			pgProcBoth(t, p, b, c, "SELECT last_value FROM pp_visibility_counter", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}})
			pgProcExec(t, p, b, c, "DROP SCHEMA pp_visibility CASCADE")
		})
	}
}
