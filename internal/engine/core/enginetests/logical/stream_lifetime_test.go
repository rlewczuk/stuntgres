package logical

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	stream "stuntgres/internal/engine/logical"
	"stuntgres/internal/engine/logical/testdecoding"
	"stuntgres/internal/util"
)

type partialPlugin struct {
	stream.Plugin
	entered, release chan struct{}
}

func (p *partialPlugin) Change(o *stream.Output, t *stream.Txn, c *stream.Change) error {
	if err := p.Plugin.Change(o, t, c); err != nil {
		return err
	}
	// The real formatter has emitted BEGIN and the actual table row before this
	// one-shot barrier. No fabricated output or timing-based cancellation window.
	if p.entered != nil {
		close(p.entered)
		p.entered = nil
		select {
		case <-p.release:
		case <-o.Context().Done():
		}
	}
	return nil
}
func TestLogicalStreamCancellationConfirmation(t *testing.T) {
	for _, point := range []string{"partial", "final_fence"} {
		t.Run(point, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var armed bool
			e := engine.NewWithStartup(nil, "postgres", core.Startup{LogicalWAL: true, OutputPlugins: map[string]stream.Factory{"partial": func() stream.Plugin {
				p := &partialPlugin{Plugin: testdecoding.New()}
				if armed {
					p.entered, p.release = entered, release
				}
				return p
			}}})
			f := &fixture{t: t, e: e}
			s := enginetest.StartSession(t, e, nil)
			peer := enginetest.StartSession(t, e, nil)
			f.must(s, "SET output_plugin_libraries=partial")
			f.must(peer, "SET output_plugin_libraries=partial")
			f.must(s, "CREATE TABLE t(id integer)")
			f.must(s, "SELECT pg_create_logical_replication_slot('cancel','partial')")
			f.must(s, "INSERT INTO t VALUES(1)")
			before := f.value(peer, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name='cancel'")
			source, stop := context.WithTimeout(context.Background(), 10*time.Second)
			defer stop()
			pause := util.NewExecutionPause("logical.decode.before-confirm")
			defer pause.Resume()
			if point == "final_fence" {
				source = util.WithExecutionPause(source, pause)
			} else {
				armed = true
			}
			ctx, cancel := context.WithCancel(source)
			defer cancel()
			done := async(ctx, s, "SELECT data FROM pg_logical_slot_get_changes('cancel',NULL,NULL,'include-xids','0')")
			joined := false
			defer func() {
				cancel()
				pause.Resume()
				if !joined {
					receive(t, "cancel cleanup", done)
				}
			}()
			if point == "partial" {
				signalled(t, "partial formatted output", entered)
			} else {
				if err := pause.Wait(source); err != nil {
					t.Fatal(err)
				}
			}
			cancel()
			close(release)
			pause.Resume()
			result := receive(t, "cancelled output owner", done)
			joined = true
			armed = false
			if !errors.Is(result.err, context.Canceled) {
				t.Fatal("cancellation lost", result.err)
			}
			if after := f.value(peer, "SELECT confirmed_flush_lsn::text FROM pg_replication_slots WHERE slot_name='cancel'"); after != before {
				t.Fatal("cancelled pass confirmed", before, after)
			}
			if active := f.value(peer, "SELECT active FROM pg_replication_slots WHERE slot_name='cancel'"); active != "f" {
				t.Fatal("held slot leaked", active)
			}
			f.expect(f.changes(peer, "cancel", "include-xids", "0"), "BEGIN", "table public.t: INSERT: id[integer]:1", "COMMIT")
		})
	}
}
func TestLogicalStreamRetentionControls(t *testing.T) {
	f := newFixture(t)
	r, w := f.session(), f.session()
	f.must(r, "SET output_plugin_libraries=test_decoding")
	f.must(r, "SET debug_logical_replication_streaming=immediate")
	f.must(r, "CREATE TABLE t(id integer)")
	for _, name := range []string{"first", "lagging"} {
		f.must(r, "SELECT pg_create_logical_replication_slot('"+name+"','test_decoding')")
	}
	f.must(w, "BEGIN")
	f.must(w, "INSERT INTO t VALUES(1)")
	first := f.changes(r, "first", streamOptions...)
	if len(first) != 3 {
		t.Fatal(first)
	}
	f.must(r, "SELECT pg_copy_logical_replication_slot('first','copied')")
	f.expect(f.changes(r, "first", streamOptions...))
	f.must(w, "INSERT INTO t VALUES(2)")
	f.must(w, "COMMIT")
	continued := []string{"opening a streamed block for transaction", "streaming change for transaction", "streaming change for transaction", "closing a streamed block for transaction", "committing streamed transaction"}
	f.expect(f.changes(r, "first", streamOptions...), continued...)
	f.must(r, "SELECT pg_drop_replication_slot('first')")
	f.expect(f.changes(r, "copied", streamOptions...), continued...)
	all := f.changes(r, "lagging", streamOptions...)
	if len(all) != 7 {
		t.Fatal("lagging slot lost retained prefix", all)
	}
	f.must(r, "SELECT pg_replication_slot_advance('lagging','FFFFFFFF/0')")
	f.must(r, "SELECT pg_drop_replication_slot('lagging')")
	f.must(r, "SELECT pg_drop_replication_slot('copied')")
	f.must(r, "SELECT pg_create_logical_replication_slot('fresh','test_decoding')")
	f.expect(f.changes(r, "fresh", streamOptions...))
}
