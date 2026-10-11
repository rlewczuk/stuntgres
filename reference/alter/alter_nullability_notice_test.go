package reference_test

import (
	"fmt"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterNullabilityReviewCascadeNotice(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, conn, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, recorder, s) }
					run(fixture.Exec("CREATE TABLE t(a INT,b INT,c INT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(7,9,11)", 1))
					run(fixture.Exec("CREATE SEQUENCE owned OWNED BY t.b", 0))
					run(fixture.Exec("CREATE VIEW v AS SELECT b,c FROM t", 0))
					run(fixture.Exec("CREATE VIEW w AS SELECT * FROM v", 0))
					if len(notices) != 0 {
						t.Fatal("unexpected setup notice", notices)
					}
					step := fixture.Exec("ALTER TABLE t DROP b CASCADE, DROP c RESTRICT, ALTER a SET NOT NULL", 0)
					step.Prepared = prepared
					run(step)
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to 2 other objects", Detail: "drop cascades to view v\ndrop cascades to view w"}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal("selected cascade notice", notices, want)
					}
					run(fixture.Select("SELECT * FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}
