package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceTemporaryTableNotices(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "Direct", true: "Prepared"}[prepared], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					var notices []referenceDiagnostic
					db, c, recorder := noticeReferencePool(t, p, backend.pg, &notices)
					s := &tempReferenceSession{t: t, p: p, db: db, c: c, record: recorder, prepared: prepared, postgres: backend.pg}
					s.utility("CREATE TEMP TABLE notice_table(v INT)", nil)
					s.exec(c, "INSERT INTO notice_table VALUES(11)", 1)
					notices = nil
					s.utility("CREATE TEMPORARY TABLE IF NOT EXISTS notice_table(other TEXT) ON COMMIT DROP", nil)
					want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_table" already exists, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatalf("notice got%#v want%#v", notices, want)
					}
					temporaryTableRows(s, c, "notice_table", 11)
					s.utility(`CREATE TEMP TABLE "q""u\z"(v INT)`, nil)
					s.utility(`CREATE TEMP TABLE "q""u\z"(v INT)`, tempReferenceDiagnostic("42P07", `relation "q"u\z" already exists`, "", ""))
				})
			}
		})
	}
}
