package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
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
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_control(v INT)", 0)
		fixture.LockExec(t, p.Ctx, peer, "INSERT INTO prepared_control VALUES(1)", 1)
		var baseline bytes.Buffer
		if !b.PG {
			if err := stuntgres.Dump(p.Ctx, p.Native, &baseline); err != nil {
				t.Fatal(err)
			}
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_control VALUES(7)", 1)
		fixture.PreparedReferencePrepare(t, p, b, a, "native_controls")
		entry := fixture.PreparedReferenceOne(t, p, peer, "native_controls")
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_control ORDER BY v", 1)
		if !b.PG {
			var private bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &private); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(private.Bytes(), baseline.Bytes()) {
				t.Fatal("prepared writes/GID leaked into Dump", private.String(), baseline.String())
			}
			for _, control := range []func() error{
				func() error { return stuntgres.Reset(p.Ctx, p.Native) },
				func() error {
					return stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(v INT);INSERT INTO replacement VALUES(99);"))
				},
			} {
				fixture.TransactionOrdinary(t, control(), "stuntgres: cannot replace catalog while prepared transactions exist")
				fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_control ORDER BY v", 1)
				got := fixture.PreparedReferenceOne(t, p, peer, "native_controls")
				if got.Xid != entry.Xid || !got.Prepared.Equal(entry.Prepared) {
					t.Fatal(entry, got)
				}
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, preparedReferenceBrokenReader{}); err == nil || !strings.Contains(err.Error(), "prepared fixture reader failure") {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE canceled(v INT);")); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			fixture.PreparedReferenceOne(t, p, peer, "native_controls")
			retained, err := peer.QueryContext(p.Ctx, "SELECT * FROM pg_prepared_xacts WHERE gid='native_controls'")
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			fixture.LockExec(t, p.Ctx, a, "COMMIT PREPARED 'native_controls'", 0)
			// Native eager rows retain the five-column copied entry after removal.
			if !retained.Next() {
				t.Fatal("prepared snapshot lost", retained.Err())
			}
			var xid any
			var gid, owner, database string
			var stamp time.Time
			if err := retained.Scan(&xid, &gid, &stamp, &owner, &database); err != nil || gid != entry.Gid || !stamp.Equal(entry.Prepared) {
				t.Fatal(gid, stamp, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
			retained.Close()
			var committed bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(committed.Bytes(), baseline.Bytes()) {
				t.Fatal("committed INSERT missing from Dump")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(committed.Bytes())); err != nil {
				t.Fatal(err)
			}
		} else {
			fixture.LockExec(t, p.Ctx, a, "COMMIT PREPARED 'native_controls'", 0)
		}
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_control ORDER BY v", 1, 7)
		fixture.PreparedReferenceAbsent(t, p, peer, "native_controls")
		if !b.PG {
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferencePreparedTransactionRestoreReaderOverlap(t *testing.T) {
	fixture.PreparedReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
		fixture.LockExec(t, p.Ctx, peer, "CREATE TABLE prepared_overlap(v INT)", 0)
		fixture.LockExec(t, p.Ctx, peer, "INSERT INTO prepared_overlap VALUES(1)", 1)
		var reader *preparedReferenceReader
		var done chan error
		released := false
		joined := false
		if !b.PG {
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
			go func() { done <- stuntgres.Restore(p.Ctx, p.Native, reader) }()
			select {
			case <-reader.entered:
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
		}
		fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
		fixture.LockExec(t, p.Ctx, a, "INSERT INTO prepared_overlap VALUES(7)", 1)
		fixture.PreparedReferencePrepare(t, p, b, a, "reader_overlap")
		if !b.PG {
			close(reader.release)
			released = true
			select {
			case err := <-done:
				joined = true
				fixture.TransactionOrdinary(t, err, "stuntgres: cannot replace catalog while prepared transactions exist")
			case <-p.Ctx.Done():
				t.Fatal(p.Ctx.Err())
			}
		}
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_overlap ORDER BY v", 1)
		fixture.PreparedReferenceOne(t, p, peer, "reader_overlap")
		fixture.LockExec(t, p.Ctx, peer, "COMMIT PREPARED 'reader_overlap'", 0)
		fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM prepared_overlap ORDER BY v", 1, 7)
		if !b.PG {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(v INT);INSERT INTO replacement VALUES(99);")); err != nil {
				t.Fatal(err)
			}
			fixture.PreparedReferenceRows(t, p, b, peer, "SELECT v FROM replacement", 99)
		}
	})
}
