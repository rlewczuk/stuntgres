package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceRangePartitionCreateSchemaBoundary(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		for _, prepared := range []bool{false, true} {
			suffix := "direct"
			if prepared {
				suffix = "prepared"
			}
			schema := "rp_schema_" + suffix
			sequence := "rp_schema_sequence_" + suffix
			run := func(step referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, step) }
			run(referenceExec("CREATE SEQUENCE "+sequence, 0))
			query := fmt.Sprintf("CREATE SCHEMA %s CREATE TABLE ordinary(k INT) CREATE TABLE virtual(k INT) PARTITION BY RANGE(k) CREATE TABLE leaf PARTITION OF virtual FOR VALUES FROM(nextval('public.%s')) TO(20)", schema, sequence)
			step := referenceExec(query, 0)
			if !b.pg {
				step = referenceRangeError(query, "0A000", "partition definitions in CREATE SCHEMA are unsupported", referenceDiagnostic{Position: referenceRangePosition(query, "PARTITION")})
			}
			if prepared {
				step = referencePrepared(step)
			}
			run(step)
			relations := [][]any{}
			if b.pg {
				relations = [][]any{{"leaf", "r"}, {"ordinary", "r"}, {"virtual", "p"}}
			}
			run(referenceSelect("SELECT t.tablename AS relname,c.relkind FROM pg_tables t JOIN pg_class c ON c.relname=t.tablename WHERE t.schemaname='"+schema+"' ORDER BY t.tablename", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, relations))
			if !b.pg {
				// Successful recreation of the exact namespace proves the failed
				// whole statement did not publish even its schema descriptor.
				run(referenceExec("CREATE SCHEMA "+schema, 0))
				run(referenceExec("DROP SCHEMA "+schema, 0))
			}
			next := int64(1)
			if b.pg {
				next = 2
			}
			run(referenceSelect("SELECT nextval('"+sequence+"') AS next", []string{"next"}, []string{"INT8"}, [][]any{{next}}))
			if b.pg {
				run(referenceExec("DROP SCHEMA "+schema+" CASCADE", 0))
			}
			run(referenceExec("DROP SEQUENCE "+sequence, 0))
		}
	})
}
