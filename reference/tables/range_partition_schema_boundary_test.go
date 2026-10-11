package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionCreateSchemaBoundary(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, prepared := range []bool{false, true} {
			suffix := "direct"
			if prepared {
				suffix = "prepared"
			}
			schema := "rp_schema_" + suffix
			sequence := "rp_schema_sequence_" + suffix
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step) }
			run(fixture.Exec("CREATE SEQUENCE "+sequence, 0))
			query := fmt.Sprintf("CREATE SCHEMA %s CREATE TABLE ordinary(k INT) CREATE TABLE virtual(k INT) PARTITION BY RANGE(k) CREATE TABLE leaf PARTITION OF virtual FOR VALUES FROM(nextval('public.%s')) TO(20)", schema, sequence)
			step := fixture.Exec(query, 0)
			if !b.PG {
				step = referenceRangeError(query, "0A000", "partition definitions in CREATE SCHEMA are unsupported", fixture.Diagnostic{Position: referenceRangePosition(query, "PARTITION")})
			}
			if prepared {
				step = fixture.Prepared(step)
			}
			run(step)
			relations := [][]any{}
			if b.PG {
				relations = [][]any{{"leaf", "r"}, {"ordinary", "r"}, {"virtual", "p"}}
			}
			run(fixture.Select("SELECT t.tablename AS relname,c.relkind FROM pg_tables t JOIN pg_class c ON c.relname=t.tablename WHERE t.schemaname='"+schema+"' ORDER BY t.tablename", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, relations))
			if !b.PG {
				// Successful recreation of the exact namespace proves the failed
				// whole statement did not publish even its schema descriptor.
				run(fixture.Exec("CREATE SCHEMA "+schema, 0))
				run(fixture.Exec("DROP SCHEMA "+schema, 0))
			}
			next := int64(1)
			if b.PG {
				next = 2
			}
			run(fixture.Select("SELECT nextval('"+sequence+"') AS next", []string{"next"}, []string{"INT8"}, [][]any{{next}}))
			if b.PG {
				run(fixture.Exec("DROP SCHEMA "+schema+" CASCADE", 0))
			}
			run(fixture.Exec("DROP SEQUENCE "+sequence, 0))
		}
	})
}
