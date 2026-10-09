package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceDefaultExistingTableSkipNotice(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var notices []referenceDiagnostic
			db, conn, recorder := noticeReferencePool(t, p, backend.pg, &notices)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
			run(referenceExec(`CREATE TABLE d(i INT DEFAULT 7)`, 0))
			run(referenceExec(`CREATE TABLE IF NOT EXISTS d(i INT DEFAULT 'oops')`, 0))
			want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "d" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			referenceRunStep(t, p.ctx, db, recorder, referenceExec(`INSERT INTO d DEFAULT VALUES`, 1))
			referenceRunStep(t, p.ctx, db, recorder, referenceSelect(`SELECT * FROM d`, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
