package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDefaultExistingTableSkipNotice(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			db, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
			run(fixture.Exec(`CREATE TABLE d(i INT DEFAULT 7)`, 0))
			run(fixture.Exec(`CREATE TABLE IF NOT EXISTS d(i INT DEFAULT 'oops')`, 0))
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "d" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatalf("notices=%#v want=%#v", notices, want)
			}
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Exec(`INSERT INTO d DEFAULT VALUES`, 1))
			fixture.RunStep(t, p.Ctx, db, recorder, fixture.Select(`SELECT * FROM d`, []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
		})
	}
}
