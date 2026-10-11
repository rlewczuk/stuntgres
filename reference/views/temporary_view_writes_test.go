package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Existing updatable-view operations must consume the same retained TEMP
// manifests as reads, including independent stored subquery occurrences.
func TestReferenceTemporaryViewRetainedWrites(t *testing.T) {
	temporaryViewScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
		t := s.T
		s.Utility("CREATE TEMP TABLE retained_write_base(v INT,payload TEXT)", nil)
		s.Utility("CREATE TEMP TABLE retained_write_guard(v INT)", nil)
		s.Exec(s.C, "INSERT INTO retained_write_guard VALUES(1)", 1)
		s.Exec(s.C, "INSERT INTO retained_write_base VALUES(11,'a')", 1)
		s.Utility("CREATE VIEW retained_write_v AS SELECT payload AS txt,v AS k FROM retained_write_base WHERE v>0 AND EXISTS(SELECT 1 FROM retained_write_guard WHERE v>0)", nil)
		s.Utility("CREATE VIEW retained_write_w AS SELECT k,txt FROM retained_write_v WHERE k<100", nil)
		fixture.RunStep(t, s.P.Ctx, s.Peer, s.Record, fixture.Step{Query: "CREATE TEMP VIEW retained_write_w AS SELECT 22 AS k,'peer'::text AS txt", Prepared: s.Prepared})
		temporaryViewNotices(t, n, "retained_write_v", "retained_write_w")
		rows := func(q string, want [][]any) {
			temporaryViewSelect(s, s.C, q, []string{"k", "txt"}, []string{"INT4", "TEXT"}, want)
		}
		write := func(q string) {
			fixture.RunStep(t, s.P.Ctx, s.C, s.Record, fixture.Step{Query: q, Affected: 1, Prepared: s.Prepared})
		}
		rows("SELECT k,txt FROM retained_write_w ORDER BY k", [][]any{{int64(11), "a"}})
		write("INSERT INTO retained_write_w(k,txt) VALUES(12,'b')")
		write("UPDATE retained_write_w SET txt='changed' WHERE k=11")
		rows("SELECT k,txt FROM retained_write_w ORDER BY k", [][]any{{int64(11), "changed"}, {int64(12), "b"}})
		write("DELETE FROM retained_write_w WHERE k=12")
		rows("SELECT v AS k,payload AS txt FROM retained_write_base ORDER BY v", [][]any{{int64(11), "changed"}})
		if !s.Postgres {
			for _, replace := range []func() error{
				func() error { return stuntgres.Reset(s.P.Ctx, s.P.Native) },
				func() error {
					return stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewBufferString("CREATE TABLE retained_write_marker(v INT); INSERT INTO retained_write_marker VALUES(7);"))
				},
			} {
				if err := replace(); err != nil {
					t.Fatal(err)
				}
				rows("SELECT k,txt FROM retained_write_w ORDER BY k", [][]any{{int64(11), "changed"}})
				write("INSERT INTO retained_write_w(k,txt) VALUES(13,'c')")
				write("UPDATE retained_write_w SET txt='retained' WHERE k=13")
				rows("SELECT k,txt FROM retained_write_w ORDER BY k", [][]any{{int64(11), "changed"}, {int64(13), "retained"}})
				write("DELETE FROM retained_write_w WHERE k=13")
				rows("SELECT v AS k,payload AS txt FROM retained_write_base ORDER BY v", [][]any{{int64(11), "changed"}})
			}
		}
		temporaryViewSelect(s, s.Peer, "SELECT k,txt FROM retained_write_w", []string{"k", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(22), "peer"}})
	})
}
