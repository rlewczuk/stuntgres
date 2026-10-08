package reference_test

import (
	"testing"
)

func TestReferenceQueryLimitInsertPrivateWrites(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				p := referenceLimitFixture(t)
				for _, backend := range p.backends() {
					tx, err := backend.db.BeginTx(p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer tx.Rollback()
						run := func(step referenceStep) {
							t.Helper()
							referenceRunStep(t, p.ctx, tx, backend.recorder, step)
						}
						run(referenceExec("INSERT INTO t VALUES(8,8)", 1))
						write := referenceExec("INSERT INTO t SELECT id,v FROM t ORDER BY id DESC LIMIT 1", 1)
						write.prepared = prepared
						run(write)
						run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4, 8, 8)))
						peer := referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4))
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, peer)
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(peer))
						want := referenceLimitIDs(1, 2, 3, 4)
						if commit {
							err = tx.Commit()
							want = referenceLimitIDs(1, 2, 3, 4, 8, 8)
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						peer.rows = want
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, peer)
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(peer))
					}()
				}
			})
		}
	}
}
