package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func batchRetainedControls(t *testing.T) {
	batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
		batchSeed(t, p, b, a)
		r := batchQuery(t, p, a, "SELECT i FROM batch_t; SELECT i FROM batch_t")
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
		} else {
			batchExec(t, p, b, peer, "DELETE FROM batch_t; INSERT INTO batch_t VALUES(8)", 1)
		}
		fixture.BatchSets(t, r, fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false}, fixture.BatchSet{[]string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}, false})
		if !b.PG {
			batchValues(t, p, b, a, "SELECT i FROM batch_t", 7)
		} else {
			batchValues(t, p, b, a, "SELECT i FROM batch_t", 8)
		}
	})
}
