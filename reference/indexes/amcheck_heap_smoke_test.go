package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapFeatureSmoke(t *testing.T) {
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		heapInstall(t, p, b, c)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO heap_t VALUES(1,$1),(2,NULL)", 2, strings.Repeat("typed large text ", 1000)))
		for _, q := range []string{"SELECT * FROM verify_heapam('heap_t')", "SELECT * FROM verify_heapam(relation=>'heap_t',on_error_stop=>true)"} {
			t.Run(q, func(t *testing.T) { heapBoth(t, p, b, c, heapEmpty(q)) })
		}
		t.Run("null", func(t *testing.T) {
			heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam('heap_t',NULL::boolean)", heapDiagnostic("22023", "on_error_stop cannot be null")))
		})
		t.Run("skip", func(t *testing.T) {
			d := heapDiagnostic("22023", "invalid skip option")
			d.Hint = `Valid skip options are "all-visible", "all-frozen", and "none".`
			heapBoth(t, p, b, c, heapError("SELECT * FROM verify_heapam('heap_t',skip=>'invalid')", d))
		})
		t.Run("toast", func(t *testing.T) {
			q := "SELECT * FROM verify_heapam('heap_t',check_toast=>true)"
			s := heapEmpty(q)
			if !b.PG { s = heapError(q, heapPhysicalDiagnostic()) }
			heapBoth(t, p, b, c, s)
		})
		if !b.PG {
			var snapshot bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &snapshot); err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(snapshot.Bytes())); err != nil { t.Fatal(err) }
			heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam('heap_t')"))
		}
	})
}
