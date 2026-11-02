package reference_test

import (
	"bytes"
	"database/sql"
	"sync"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Both controls must release engine/catalog locks before entering user I/O.
// Gate admission, not elapsed time, establishes the independently observable
// concurrent SQL witness. Every exit releases and joins the owned operation.
func intarrayExternalIO(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, dump []byte) {
	t.Helper()
	for _, restore := range []bool{false, true} {
		func() {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			done := make(chan error, 1)
			completed := false
			writer := &fixture.CatalogControlGatedWriter{Started: entered, Release: release}
			reader := &fixture.ReferenceRangeGateReader{Reader: bytes.NewReader(dump), Entered: entered, Release: release}
			go func() {
				if restore {
					done <- stuntgres.Restore(p.Ctx, p.Native, reader)
				} else {
					done <- stuntgres.Dump(p.Ctx, p.Native, writer)
				}
			}()
			defer func() {
				unblock()
				if !completed {
					if err := <-done; err != nil {
						t.Errorf("gated control restore=%t: %v", restore, err)
					}
				}
				if !restore && !bytes.Equal(writer.Output.Bytes(), dump) {
					t.Error("gated Dump changed committed snapshot")
				}
			}()
			select {
			case <-entered:
			case err := <-done:
				completed = true
				t.Fatal("control completed before entering external I/O", err)
			case <-p.Ctx.Done():
				t.Fatal("control did not enter external I/O", p.Ctx.Err())
			}
			// A real mutation requires the catalog/store publication locks; a
			// read-only ping would not prove that control I/O released them.
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_values VALUES (98,'{98}')", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ia_values WHERE id=98", 1))
		}()
	}
}
