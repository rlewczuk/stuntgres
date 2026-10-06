package stuntgres_test

import (
	"context"
	"errors"
	"stuntgres"
	"sync"
	"testing"
	"time"
)

func TestDateConcurrentPreparedOwnership(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	execSQL(t, db, "INSERT INTO d VALUES('epoch'),('2000-02-29'),('infinity')")
	stmt, err := db.Prepare("SELECT v FROM d WHERE v=$1")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	var wg sync.WaitGroup
	for _, tc := range []struct{ arg, want string }{{"epoch", "1970-01-01"}, {"2000-02-29", "2000-02-29"}, {"infinity", "infinity"}} {
		wg.Add(1)
		go func(arg, want string) {
			defer wg.Done()
			for range 8 {
				var got any
				err := stmt.QueryRow(arg).Scan(&got)
				if err != nil || got != want {
					t.Errorf("%s: %v %v", arg, got, err)
					return
				}
			}
		}(tc.arg, tc.want)
	}
	wg.Wait()
}

func TestDateCanceledStagedRestorePreservesSnapshot(t *testing.T) {
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	execSQL(t, db, "CREATE TABLE d(v DATE)")
	execSQL(t, db, "INSERT INTO d VALUES('epoch')")
	old, err := db.Query("SELECT v FROM d")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reader := &gatedReader{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- stuntgres.Restore(ctx, db, reader) }()
	select {
	case <-reader.started:
	case <-ctx.Done():
		close(reader.release)
		<-done
		t.Fatal("restore reader not reached")
	}
	cancel()
	close(reader.release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dateCheckRows(t, old, []any{"1970-01-01"})
	rows, err := db.Query("SELECT v FROM d")
	if err != nil {
		t.Fatal(err)
	}
	dateCheckRows(t, rows, []any{"1970-01-01"})
}
