package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGeneratedVirtualCopies(t *testing.T) {
	generatedStoredScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, unused func(fixture.Step)) {
		var notices []fixture.Diagnostic
		_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
		run(fixture.Exec(`CREATE TABLE p(a INT,b INT GENERATED ALWAYS AS(1/a) VIRTUAL)`, 0))
		run(fixture.Exec(`CREATE TABLE clone(LIKE p INCLUDING GENERATED)`, 0))
		run(fixture.Exec(`INSERT INTO clone(a) VALUES(0)`, 1))
		run(fixture.Select(`SELECT a FROM clone`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		s := fixture.ImplicitError(`SELECT b FROM clone`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows = true
		run(s)
		run(fixture.Exec(`CREATE TABLE ordinary(LIKE p)`, 0))
		run(fixture.Exec(`INSERT INTO ordinary VALUES(0,99)`, 1))
		run(fixture.Select(`SELECT b FROM ordinary`, []string{"b"}, []string{"INT4"}, [][]any{{int64(99)}}))
		run(fixture.Exec(`CREATE TABLE inherited() INHERITS(p)`, 0))
		run(fixture.Exec(`INSERT INTO inherited(a) VALUES(0)`, 1))
		run(fixture.Select(`SELECT a FROM inherited`, []string{"a"}, []string{"INT4"}, [][]any{{int64(0)}}))
		s = fixture.ImplicitError(`SELECT b FROM inherited`, fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		s.SelectRows = true
		run(s)
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `moving and merging column "b" with inherited definition`, Detail: "User-specified column moved to the position of the inherited column."}}
		notices = nil
		run(fixture.Exec(`CREATE TABLE overridden(b INT GENERATED ALWAYS AS(a+4) VIRTUAL) INHERITS(p)`, 0))
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices %#v want %#v", notices, want)
		}
		run(fixture.Exec(`INSERT INTO overridden(a) VALUES(0)`, 1))
		run(fixture.Select(`SELECT b FROM overridden`, []string{"b"}, []string{"INT4"}, [][]any{{int64(4)}}))
		notices = nil
		run(fixture.ImplicitError(`CREATE TABLE bad(b INT GENERATED ALWAYS AS(a+4) STORED) INHERITS(p)`, fixture.Diagnostic{Code: "42611", Message: `column "b" inherits from generated column of different kind`, Detail: "Parent column is VIRTUAL, child column is STORED."}))
		if !reflect.DeepEqual(notices, want) {
			t.Fatalf("notices %#v want %#v", notices, want)
		}
		run(fixture.ImplicitError(`SELECT * FROM bad`, fixture.Diagnostic{Code: "42P01", Message: `relation "bad" does not exist`, Position: "15"}))
		run(fixture.Exec(`CREATE TABLE bad(a INT)`, 0))
	})
}
