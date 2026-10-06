package stuntgres_test

import (
	"bytes"
	"context"
	"io"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

type sequenceGatedReader struct {
	started, release chan struct{}
	once             sync.Once
	reader           io.Reader
}

func (r *sequenceGatedReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started); <-r.release })
	return r.reader.Read(p)
}

func TestSequenceRestoreRejectsRecreatedCapturedOwner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	a := sequenceConn(t, db)
	for _, q := range []string{"CREATE USER imported_owner", "SET SESSION AUTHORIZATION imported_owner", "CREATE SEQUENCE imported", "RESET SESSION AUTHORIZATION", "CREATE SEQUENCE sentinel"} {
		sequenceExec(t, a, q)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	sequenceExec(t, a, "DROP SEQUENCE imported")
	reader := &sequenceGatedReader{started: make(chan struct{}), release: make(chan struct{}), reader: bytes.NewReader(dump.Bytes())}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Restore(ctx, db, reader) }()
	joined := false
	defer func() {
		select {
		case <-reader.release:
		default:
			close(reader.release)
		}
		if !joined {
			<-done
		}
	}()
	select {
	case <-reader.started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for _, q := range []string{"DROP ROLE imported_owner", "CREATE USER imported_owner"} {
		sequenceExec(t, a, q)
	}
	close(reader.release)
	err := <-done
	joined = true
	if err == nil {
		t.Fatal("captured owner silently adopted recreated role")
	}
	sequenceValue(t, a, "SELECT nextval('sentinel')", 1)
	if _, err := a.ExecContext(ctx, "SELECT nextval('imported')"); err == nil {
		t.Fatal("failed restore published imported sequence")
	}
}
