package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

type preparedReferenceReader struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	source  io.Reader
}

func (r *preparedReferenceReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.entered); <-r.release })
	return r.source.Read(p)
}

type preparedReferenceBrokenReader struct{}

func (preparedReferenceBrokenReader) Read([]byte) (int, error) {
	return 0, errors.New("prepared fixture reader failure")
}

func TestReferencePreparedTransactionNativeControls(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_control(v INT)", 0)
		referenceLockExec(t, p.ctx, peer, "INSERT INTO prepared_control VALUES(1)", 1)
		var baseline bytes.Buffer
		if !b.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &baseline); err != nil {
				t.Fatal(err)
			}
		}
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_control VALUES(7)", 1)
		preparedReferencePrepare(t, p, b, a, "native_controls")
		entry := preparedReferenceOne(t, p, peer, "native_controls")
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_control ORDER BY v", 1)
		if !b.pg {
			var private bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &private); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(private.Bytes(), baseline.Bytes()) {
				t.Fatal("prepared writes/GID leaked into Dump", private.String(), baseline.String())
			}
			for _, control := range []func() error{
				func() error { return stuntgres.Reset(p.ctx, p.native) },
				func() error {
					return stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v INT);INSERT INTO replacement VALUES(99);"))
				},
			} {
				transactionOrdinary(t, control(), "stuntgres: cannot replace catalog while prepared transactions exist")
				preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_control ORDER BY v", 1)
				got := preparedReferenceOne(t, p, peer, "native_controls")
				if got.xid != entry.xid || !got.prepared.Equal(entry.prepared) {
					t.Fatal(entry, got)
				}
			}
			if err := stuntgres.Restore(p.ctx, p.native, preparedReferenceBrokenReader{}); err == nil || !strings.Contains(err.Error(), "prepared fixture reader failure") {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.native, strings.NewReader("CREATE TABLE canceled(v INT);")); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			preparedReferenceOne(t, p, peer, "native_controls")
			retained, err := peer.QueryContext(p.ctx, "SELECT * FROM pg_prepared_xacts WHERE gid='native_controls'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			referenceLockExec(t, p.ctx, a, "COMMIT PREPARED 'native_controls'", 0)
			// Native eager rows retain the five-column copied entry after removal.
			if !retained.Next() {
				t.Fatal("prepared snapshot lost", retained.Err())
			}
			var xid any
			var gid, owner, database string
			var stamp time.Time
			if err := retained.Scan(&xid, &gid, &stamp, &owner, &database); err != nil || gid != entry.gid || !stamp.Equal(entry.prepared) {
				t.Fatal(gid, stamp, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
			retained.Close()
			var committed bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &committed); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(committed.Bytes(), baseline.Bytes()) {
				t.Fatal("committed INSERT missing from Dump")
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(committed.Bytes())); err != nil {
				t.Fatal(err)
			}
		} else {
			referenceLockExec(t, p.ctx, a, "COMMIT PREPARED 'native_controls'", 0)
		}
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_control ORDER BY v", 1, 7)
		preparedReferenceAbsent(t, p, peer, "native_controls")
		if !b.pg {
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferencePreparedTransactionRestoreReaderOverlap(t *testing.T) {
	preparedReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		a, peer := referenceLockConnections(t, p.ctx, b.db)
		referenceLockExec(t, p.ctx, peer, "CREATE TABLE prepared_overlap(v INT)", 0)
		referenceLockExec(t, p.ctx, peer, "INSERT INTO prepared_overlap VALUES(1)", 1)
		var reader *preparedReferenceReader
		var done chan error
		released := false
		joined := false
		if !b.pg {
			reader = &preparedReferenceReader{entered: make(chan struct{}), release: make(chan struct{}), source: strings.NewReader("CREATE TABLE replacement(v INT);")}
			done = make(chan error, 1)
			t.Cleanup(func() {
				if !released {
					close(reader.release)
				}
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("Restore worker did not join")
					}
				}
			})
			go func() { done <- stuntgres.Restore(p.ctx, p.native, reader) }()
			select {
			case <-reader.entered:
			case <-p.ctx.Done():
				t.Fatal(p.ctx.Err())
			}
		}
		referenceLockExec(t, p.ctx, a, "BEGIN", 0)
		referenceLockExec(t, p.ctx, a, "INSERT INTO prepared_overlap VALUES(7)", 1)
		preparedReferencePrepare(t, p, b, a, "reader_overlap")
		if !b.pg {
			close(reader.release)
			released = true
			select {
			case err := <-done:
				joined = true
				transactionOrdinary(t, err, "stuntgres: cannot replace catalog while prepared transactions exist")
			case <-p.ctx.Done():
				t.Fatal(p.ctx.Err())
			}
		}
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_overlap ORDER BY v", 1)
		preparedReferenceOne(t, p, peer, "reader_overlap")
		referenceLockExec(t, p.ctx, peer, "COMMIT PREPARED 'reader_overlap'", 0)
		preparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_overlap ORDER BY v", 1, 7)
		if !b.pg {
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader("CREATE TABLE replacement(v INT);INSERT INTO replacement VALUES(99);")); err != nil {
				t.Fatal(err)
			}
			preparedReferenceRows(t, p, b, peer, "SELECT v FROM replacement", 99)
		}
	})
}
