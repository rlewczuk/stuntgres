package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

type aclitemGateWriter struct {
	buffer           bytes.Buffer
	entered, release chan struct{}
	first            bool
}

func (w *aclitemGateWriter) Write(src []byte) (int, error) {
	if !w.first {
		w.first = true
		close(w.entered)
		<-w.release
	}
	return w.buffer.Write(src)
}

func TestReferenceACLItemDumpSnapshot(t *testing.T) {
	aclitemScenario(t, []string{"acl_snapshot_a", "acl_snapshot_b"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.acl_snapshot (id INT,a aclitem,aa aclitem[])", 0))
		run(fixture.Exec("INSERT INTO public.acl_snapshot VALUES (1,'acl_snapshot_a=r*/acl_snapshot_b','[0:1]={acl_snapshot_a=r*/acl_snapshot_b,NULL}')", 1))
		check := fixture.Select("SELECT a,aa FROM public.acl_snapshot ORDER BY id", []string{"a", "aa"}, []string{"ACLITEM", "_ACLITEM"}, [][]any{{"acl_snapshot_a=r*/acl_snapshot_b", "[0:1]={acl_snapshot_a=r*/acl_snapshot_b,NULL}"}})
		run(check)
		run(fixture.Prepared(check))
		if b.PG {
			return
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		writer := &aclitemGateWriter{entered: make(chan struct{}), release: make(chan struct{})}
		done := make(chan error, 1)
		go func() { done <- stuntgres.Dump(p.Ctx, p.Native, writer) }()
		released, joined := false, false
		defer func() {
			if !released {
				close(writer.release)
			}
			if !joined {
				select {
				case <-done:
				case <-p.Ctx.Done():
					t.Error("Dump worker failed to join")
				}
			}
		}()
		select {
		case <-writer.entered:
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		// Writer I/O is outside engine locks. Mutate roles after the capture,
		// then require the same complete dump from the immutable old snapshot.
		for _, q := range []string{"DROP ROLE acl_snapshot_a", "CREATE ROLE acl_snapshot_a", "DROP ROLE acl_snapshot_b", "CREATE ROLE acl_snapshot_b"} {
			run(fixture.Exec(q, 0))
		}
		close(writer.release)
		released = true
		select {
		case err := <-done:
			joined = true
			if err != nil {
				t.Fatal(err)
			}
		case <-p.Ctx.Done():
			t.Fatal(p.Ctx.Err())
		}
		if writer.buffer.String() != before.String() {
			t.Fatal("Dump consulted roles changed during writer IO")
		}
		run(fixture.Select("SELECT a=$1::aclitem AS same FROM public.acl_snapshot", []string{"same"}, []string{"BOOL"}, [][]any{{false}}, "acl_snapshot_a=r*/acl_snapshot_b"))
		staleWriter := &aclitemCountingWriter{}
		if err := stuntgres.Dump(p.Ctx, p.Native, staleWriter); err == nil || staleWriter.calls != 0 {
			t.Fatalf("later stale Dump error=%v writer calls=%d", err, staleWriter.calls)
		}
	})
}
