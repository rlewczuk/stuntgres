package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryLimitInsertPrivateWrites(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+map[bool]string{false: "/rollback", true: "/commit"}[commit], func(t *testing.T) {
				p := referenceLimitFixture(t)
				for _, backend := range p.Backends() {
					tx, err := backend.DB.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					func() {
						defer tx.Rollback()
						run := func(step fixture.Step) {
							t.Helper()
							fixture.RunStep(t, p.Ctx, tx, backend.Recorder, step)
						}
						run(fixture.Exec("INSERT INTO t VALUES(8,8)", 1))
						write := fixture.Exec("INSERT INTO t SELECT id,v FROM t ORDER BY id DESC LIMIT 1", 1)
						write.Prepared = prepared
						run(write)
						run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4, 8, 8)))
						peer := fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, referenceLimitIDs(1, 2, 3, 4))
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, peer)
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(peer))
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
						peer.Rows = want
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, peer)
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(peer))
					}()
				}
			})
		}
	}
}
