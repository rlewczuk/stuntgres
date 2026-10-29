package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeparsePartitionLifecycle(t *testing.T) {
	t.Run("range_default_and_frozen_bounds", func(t *testing.T) {
		fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE SEQUENCE dp_once START 10",
				"CREATE TABLE dp_live(k BIGINT) PARTITION BY RANGE(k)",
				"CREATE TABLE dp_live_default PARTITION OF dp_live DEFAULT",
				"CREATE TABLE dp_live_a PARTITION OF dp_live FOR VALUES FROM(0) TO(nextval('dp_once'))",
			} {
				run(fixture.Exec(q, 0))
			}
			q := "SELECT pg_get_partition_constraintdef($1) AS definition"
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			id := fixture.DeparseDefinitionID(t, c, "SELECT 'dp_live_default'::regclass::oid")
			check := func(want string) {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, fixture.DeparseRelationsTextStep(t, b, q, want, id))
			}
			check("(NOT ((k IS NOT NULL) AND ((k >= '0'::bigint) AND (k < '10'::bigint))))")
			run(fixture.Exec("CREATE TABLE dp_live_b PARTITION OF dp_live FOR VALUES FROM(20) TO(30)", 0))
			check("(NOT ((k IS NOT NULL) AND (((k >= '0'::bigint) AND (k < '10'::bigint)) OR ((k >= '20'::bigint) AND (k < '30'::bigint)))))")
			run(fixture.Exec("DROP TABLE dp_live_b", 0))
			check("(NOT ((k IS NOT NULL) AND ((k >= '0'::bigint) AND (k < '10'::bigint))))")
			run(fixture.Select("SELECT last_value FROM dp_once", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
			// Full explicit coverage leaves only NULL for the DEFAULT partition.
			run(fixture.Exec("DROP TABLE dp_live_a", 0))
			run(fixture.Exec("CREATE TABLE dp_live_all PARTITION OF dp_live FOR VALUES FROM(MINVALUE) TO(MAXVALUE)", 0))
			check("(NOT ((k IS NOT NULL) AND (k IS NOT NULL)))")
		})
	})
	t.Run("list_full_coverage", func(t *testing.T) {
		fixture.DeparseRelationsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{
				"CREATE TABLE dp_boolean(k BOOL) PARTITION BY LIST(k)",
				"CREATE TABLE dp_boolean_default PARTITION OF dp_boolean DEFAULT",
				"CREATE TABLE dp_boolean_values PARTITION OF dp_boolean FOR VALUES IN(true,false,NULL)",
			} {
				run(fixture.Exec(q, 0))
			}
			s := fixture.DeparseRelationsTextStep(t, b, "SELECT pg_get_partition_constraintdef('dp_boolean_default'::regclass) AS definition", "(NOT ((k IS NULL) OR (k = ANY (ARRAY[false, true]))))")
			run(s)
			run(fixture.Prepared(s))
		})
	})
	t.Run("pending_canonical_membership", func(t *testing.T) {
		detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
			t.Run(b.Name, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				detachConcurrentSetup(run)
				query := "SELECT pg_get_partition_constraintdef('dc_leave'::regclass) AS definition"
				s := fixture.DeparseRelationsTextStep(t, b, query, "((k IS NOT NULL) AND (k >= 2) AND (k < 4))")
				run(s)
				detachConcurrentPending(t, p, b, true)
				run(s)
				run(fixture.Prepared(s))
				run(fixture.Exec("ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", 0))
				run(fixture.DeparseRelationsTextStep(t, b, query, nil))
			})
		})
	})
}
