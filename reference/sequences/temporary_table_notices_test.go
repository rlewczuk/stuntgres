package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTemporaryTableNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					db, c, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					s := &fixture.TempReferenceSession{T: t, P: p, DB: db, C: c, Record: recorder, Prepared: prepared, Postgres: backend.PG}
					s.Utility("CREATE TEMP TABLE notice_table(v INT)", nil)
					s.Exec(c, "INSERT INTO notice_table VALUES(11)", 1)
					notices = nil
					s.Utility("CREATE TEMPORARY TABLE IF NOT EXISTS notice_table(other TEXT) ON COMMIT DROP", nil)
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_table" already exists, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("notice got%#v want%#v", notices, want)
					}
					fixture.TemporaryTableRows(s, c, "notice_table", 11)
					s.Utility(`CREATE TEMP TABLE "q""u\z"(v INT)`, nil)
					s.Utility(`CREATE TEMP TABLE "q""u\z"(v INT)`, fixture.TempReferenceDiagnostic("42P07", `relation "q"u\z" already exists`, "", ""))
				})
			}
		})
	}
}
