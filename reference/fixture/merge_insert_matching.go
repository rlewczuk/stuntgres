package fixture

import (
	"testing"
)

func MergeInsertRun(t *testing.T, p *Pair, b Backend, prepared bool) func(Step) {
	return func(s Step) {
		if len(s.Query) >= 5 && s.Query[:5] == "MERGE" {
			s.Prepared = prepared || s.Prepared
			// lib/pq does not decode PostgreSQL's MERGE command tag. Persisted
			// rows independently prove effects; native must report their count.
			if b.PG {
				s.Affected = 0
			}
		}
		InsertSelectRun(t, p.Ctx, b.DB, b.Recorder, s)
	}
}
