package fixture

import "testing"

func TestReferenceFixtureLegacyNoticeOwnership(t *testing.T) {
	p := NewFixture(t, *Mode, false)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices, peerNotices []Diagnostic
			ownerPool, owner, record := NoticeReferencePool(t, p, backend.PG, &notices)
			peerPool, peer, peerRecord := NoticeReferencePool(t, p, backend.PG, &peerNotices)
			RunStep(t, p.Ctx, owner, record, Exec("CREATE TABLE fixture_notice_owned(v INT)", 0))
			RunStep(t, p.Ctx, owner, record, Exec("INSERT INTO fixture_notice_owned VALUES(7)", 1))
			for _, prepared := range []bool{false, true} {
				step := Select("SELECT v FROM fixture_notice_owned", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				step.Prepared = prepared
				RunStep(t, p.Ctx, peer, peerRecord, step)
				notices = nil
				step = Exec("CREATE TABLE IF NOT EXISTS fixture_notice_owned(v TEXT)", 0)
				step.Prepared = prepared
				RunStep(t, p.Ctx, owner, record, step)
				want := Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "fixture_notice_owned" already exists, skipping`}
				if len(notices) != 1 || notices[0] != want || len(peerNotices) != 0 {
					t.Fatalf("notice connector ownership: owner=%#v peer=%#v", notices, peerNotices)
				}
			}
			if err := owner.Close(); err != nil {
				t.Fatal(err)
			}
			if err := peer.Close(); err != nil {
				t.Fatal(err)
			}
			if err := ownerPool.Close(); err != nil {
				t.Fatal(err)
			}
			if err := peerPool.Close(); err != nil {
				t.Fatal(err)
			}
			_, peer, peerRecord = NoticeReferencePool(t, p, backend.PG, &peerNotices)
			RunStep(t, p.Ctx, peer, peerRecord, Select("SELECT v FROM fixture_notice_owned", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			if backend.PG {
				return
			}

			// Legacy child names select distinct engines within the owning Driver.
			// A fresh-provenance scenario instead deliberately uses its parent
			// connector; do not erase the legacy namespace contract here.
			missing := ImplicitError("SELECT v FROM fixture_notice_owned", Diagnostic{Code: "42P01", Message: `relation "fixture_notice_owned" does not exist`, Position: "15"})
			t.Run("sibling_namespace", func(t *testing.T) {
				var siblingNotices []Diagnostic
				_, sibling, siblingRecord := NoticeReferencePool(t, p, false, &siblingNotices)
				RunStep(t, p.Ctx, sibling, siblingRecord, missing)
				RunStep(t, p.Ctx, sibling, siblingRecord, Exec("CREATE TABLE fixture_notice_owned(v INT)", 0))
				RunStep(t, p.Ctx, sibling, siblingRecord, Select("SELECT v FROM fixture_notice_owned", []string{"v"}, []string{"INT4"}, nil))
			})
			// Same pair, parent DSN: the legacy child must not publish here.
			RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, missing)
			RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, Exec("CREATE TABLE fixture_notice_owned(v INT)", 0))
			RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, Select("SELECT v FROM fixture_notice_owned", []string{"v"}, []string{"INT4"}, nil))

			// Same child DSN, separate pair: no process-global fixture engine.
			independent := NewFixture(t, false, false)
			independent.Step(t, missing)
			independent.Step(t, Exec("CREATE TABLE fixture_notice_owned(v INT)", 0))
			independent.Step(t, Select("SELECT v FROM fixture_notice_owned", []string{"v"}, []string{"INT4"}, nil))
			RunStep(t, p.Ctx, peer, peerRecord, Select("SELECT v FROM fixture_notice_owned", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
