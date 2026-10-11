package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewCheckConflictUpdate(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("held=%v", held), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s ...fixture.Step) { viewCheckRun(t, p, b, held, s...) }
					run(fixture.Exec("CREATE TABLE t(a INT PRIMARY KEY,b INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10)", 1), fixture.Exec("CREATE VIEW v AS SELECT a,b FROM t WHERE b>0 WITH CHECK OPTION", 0))
					q := "INSERT INTO v VALUES($1,$2) ON CONFLICT(a) DO UPDATE SET b=excluded.b"
					var e fixture.Executor = b.DB
					if held {
						e = fixture.ViewHold(t, p, b, q)
					}
					fixture.RunStep(t, p.Ctx, e, b.Recorder, viewCheckError(q, "v", "1, -2", 1, -2))
					run(viewCheckPairRows([]any{int64(1), int64(10)}))
					fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 1, 1, 11))
					run(viewCheckPairRows([]any{int64(1), int64(11)}))
					fixture.RunStep(t, p.Ctx, e, b.Recorder, viewCheckError(q, "v", "2, -2", 2, -2))
					run(viewCheckPairRows([]any{int64(1), int64(11)}))
					run(fixture.Exec("INSERT INTO v VALUES(1,-3) ON CONFLICT(a) DO UPDATE SET b=5", 1), viewCheckPairRows([]any{int64(1), int64(5)}), fixture.Exec("INSERT INTO v VALUES(1,-4) ON CONFLICT(a) DO UPDATE SET b=excluded.b WHERE false", 0), viewCheckPairRows([]any{int64(1), int64(5)}), viewCheckError("INSERT INTO v VALUES(2,20),(1,-2) ON CONFLICT(a) DO UPDATE SET b=excluded.b", "v", "1, -2"), viewCheckPairRows([]any{int64(1), int64(5)}))
				})
			}
		})
	}
}
