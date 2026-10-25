package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/session"
	"testing"
)

func TestDeleteCoreControls(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1,'gone',TRUE),(2,'keep',FALSE),(2,'keep',FALSE)")
	retained := sessionSQL(t, b, "SELECT * FROM t ORDER BY id")
	defer retained.Rows.Close()
	before := tuples(retained)
	ref := transactionBegin(t, a)
	deleteCount(t, a, "DELETE FROM t WHERE id=1", 1)
	var private bytes.Buffer
	if err := e.Dump(context.Background(), &private); err != nil || !strings.Contains(private.String(), "'gone'") {
		t.Fatal("Dump exposed private deletion", err, private.String())
	}
	transactionEnd(t, a, ref, false)
	deleteCount(t, b, "DELETE FROM t WHERE id=1", 1)
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "'gone'") || strings.Contains(dump.String(), "DELETE") || strings.Count(dump.String(), "'keep'") != 2 {
		t.Fatal("noncanonical survivors", dump.String())
	}
	target := New()
	if err := target.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	want := [][]driver.Value{{int64(2), "keep", false}, {int64(2), "keep", false}}
	if got := tuples(run(t, target, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	_, err := testExecute(target, context.Background(), statement(t, "INSERT INTO t VALUES(NULL,'bad',FALSE)"), nil)
	state(t, err, "23502")
	epoch := target.CatalogRead().Epoch()
	if err := target.Restore(context.Background(), strings.NewReader("DELETE FROM t")); err == nil || target.CatalogRead().Epoch() != epoch {
		t.Fatal("Restore DELETE whitelist widened", err)
	}
	if got := tuples(run(t, target, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
		t.Fatal("failed Restore changed rows", got)
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(tuples(retained), before) {
		t.Fatal("replacement invalidated original cursor")
	}
}

func TestDeleteCoreConsumers(t *testing.T) {
	t.Run("not_null_and_drop_shape", func(t *testing.T) {
		e := New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE t(a INT,b TEXT,c BOOL)")
		sessionSQL(t, s, "INSERT INTO t VALUES(NULL,'gone',TRUE),(7,'keep',FALSE)")
		ref := catalogRef(t, e, e.CatalogRead(), "t")
		old := e.Storage.Relations[ref.Address]
		retained := sessionSQL(t, s, "SELECT * FROM t")
		defer retained.Rows.Close()
		deleteCount(t, s, "DELETE FROM t WHERE b='gone'", 1)
		ends := old.Ends
		sessionSQL(t, s, "ALTER TABLE t ALTER a SET NOT NULL")
		sessionSQL(t, s, "ALTER TABLE t DROP COLUMN b")
		current := e.Storage.Relations[ref.Address]
		if current == old || current.Ends != ends || len(current.Versions) != 2 || len(old.Versions[0].Values) != 3 {
			t.Fatal("shape lost history/end authority")
		}
		for i, version := range current.Versions {
			before := old.Versions[i]
			if version.Row != before.Row || version.Version != before.Version || version.Born != before.Born {
				t.Fatal("shape remapped history")
			}
		}
		got := sessionSQL(t, s, "SELECT * FROM t")
		defer got.Rows.Close()
		if !reflect.DeepEqual(tuples(got), [][]driver.Value{{int64(7), false}}) {
			t.Fatal(tuples(got))
		}
		if !reflect.DeepEqual(tuples(retained), [][]driver.Value{{nil, "gone", true}, {int64(7), "keep", false}}) {
			t.Fatal("old width changed")
		}
		sessionSQL(t, s, "ALTER TABLE t DROP COLUMN a")
		sessionSQL(t, s, "ALTER TABLE t DROP COLUMN c")
		zero := sessionSQL(t, s, "SELECT * FROM t")
		defer zero.Rows.Close()
		if zero.Count != 1 || len(zero.Rows.Schema()) != 0 || len(tuples(zero)) != 1 {
			t.Fatal("zero width lost occurrence", zero.Count)
		}
		var populated bytes.Buffer
		if err := e.Dump(context.Background(), &populated); err != nil || strings.Count(populated.String(), "DEFAULT VALUES") != 1 {
			t.Fatal("zero-width live Dump", err, populated.String())
		}
		populatedTarget := New()
		if err := populatedTarget.Restore(context.Background(), bytes.NewReader(populated.Bytes())); err != nil || run(t, populatedTarget, "SELECT * FROM t").Count != 1 {
			t.Fatal("zero-width live Restore", err)
		}
		deleteCount(t, s, "DELETE FROM t", 1)
		if len(ends.Rows) != 1 || e.Storage.Relations[ref.Address].Ends == ends || len(e.Storage.Relations[ref.Address].Ends.Rows) != 2 {
			t.Fatal("committed deletion root mutated/reused")
		}
		deleteCount(t, s, "DELETE FROM t", 0)
		var dump bytes.Buffer
		if err := e.Dump(context.Background(), &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "DEFAULT VALUES") {
			t.Fatal("zero-width deleted occurrence resurrected", dump.String())
		}
		restored := New()
		if err := restored.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if run(t, restored, "SELECT * FROM t").Count != 0 {
			t.Fatal("empty zero-width restore")
		}
	})
	t.Run("live_null_rejects", func(t *testing.T) {
		e := New()
		run(t, e, "CREATE TABLE t(v INT)")
		run(t, e, "INSERT INTO t VALUES(NULL),(7)")
		_, err := testExecute(e, context.Background(), statement(t, "ALTER TABLE t ALTER v SET NOT NULL"), nil)
		state(t, err, "23502")
		run(t, e, "DELETE FROM t WHERE v=7")
		_, err = testExecute(e, context.Background(), statement(t, "ALTER TABLE t ALTER v SET NOT NULL"), nil)
		state(t, err, "23502")
		run(t, e, "DELETE FROM t")
		run(t, e, "ALTER TABLE t ALTER v SET NOT NULL")
		_, err = testExecute(e, context.Background(), statement(t, "INSERT INTO t VALUES(NULL)"), nil)
		state(t, err, "23502")
	})
	t.Run("shape_end_token_fence", func(t *testing.T) {
		e := New()
		run(t, e, "CREATE TABLE t(a INT,b INT)")
		run(t, e, "INSERT INTO t VALUES(1,2),(3,4)")
		stage, _, _ := alterDropTestStage(t, e)
		defer e.DiscardCatalogStage(stage)
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		run(t, e, "DELETE FROM t WHERE a=1")
		_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: e.CatalogRead().Epoch()})
		var conflict catalog.Conflict
		if !errors.As(err, &conflict) {
			t.Fatal("delete did not fence captured shape", err)
		}
		if got := tuples(run(t, e, "SELECT * FROM t ORDER BY a")); !reflect.DeepEqual(got, [][]driver.Value{{int64(3), int64(4)}}) {
			t.Fatal(got)
		}
	})
}

func TestDeleteCoreCatalogReplacement(t *testing.T) {
	source := New()
	run(t, source, "CREATE TABLE t(v INT)")
	run(t, source, "INSERT INTO t VALUES(1),(1),(2)")
	retained := run(t, source, "SELECT v FROM t ORDER BY v")
	run(t, source, "DELETE FROM t WHERE v=1")
	ref := catalogRef(t, source, source.CatalogRead(), "t")
	oldStore := source.Storage.Relations[ref.Address]
	oldLive := oldStore.Versions[2]
	if _, err := source.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	self := source.Storage.Relations[ref.Address]
	if self == oldStore || len(self.Versions) != 1 || self.Ends != nil || self.Versions[0].Row == oldLive.Row || self.Versions[0].Version == oldLive.Version || self.Versions[0].Born != source.Visibility.CommitSeq() {
		t.Fatal("self replacement did not detach/remint live heads")
	}
	if got := tuples(run(t, source, "SELECT v FROM t ORDER BY v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
		t.Fatal(got)
	}
	target := New()
	run(t, target, "CREATE TABLE sentinel(v INT)")
	run(t, target, "INSERT INTO sentinel VALUES(99)")
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	imported := catalogRef(t, target, target.CatalogRead(), "t")
	store := target.Storage.Relations[imported.Address]
	if len(store.Versions) != 1 || store.Ends != nil || store.Versions[0].Row == 0 || store.Versions[0].Version == 0 || store.Versions[0].Born != target.Visibility.CommitSeq() {
		t.Fatal("foreign replacement imported history", store)
	}
	run(t, target, "INSERT INTO t VALUES(3)")
	if got := tuples(run(t, source, "SELECT v FROM t ORDER BY v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
		t.Fatal("foreign append changed source", got)
	}
	if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(1)}, {int64(2)}}) {
		t.Fatal("replacement changed retained source", got)
	}
}

type deleteFailingReader struct {
	text string
	err  error
}

func (r *deleteFailingReader) Read(dst []byte) (int, error) {
	if len(r.text) == 0 {
		return 0, r.err
	}
	n := copy(dst, r.text)
	r.text = r.text[n:]
	return n, nil
}

func TestDeleteCoreFailedReplacement(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	b := sessionStart(t, e, nil)
	sessionSQL(t, b, "CREATE TABLE t(v INT)")
	sessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
	old := sessionSQL(t, b, "SELECT v FROM t ORDER BY v")
	defer old.Rows.Close()
	deleteCount(t, b, "DELETE FROM t WHERE v=1", 1)
	ref := transactionBegin(t, a)
	sessionSQL(t, a, "INSERT INTO t VALUES(7)")
	deleteCount(t, a, "DELETE FROM t WHERE v=2", 1)
	target := catalogRef(t, e, e.CatalogRead(), "t")
	epoch, frontier, ends := e.CatalogRead().Epoch(), e.Visibility.CommitSeq(), e.Storage.Relations[target.Address].Ends
	own := a.Transactions.Deletes[target.Address]
	sentinel := errors.New("late replacement reader")
	if err := e.Restore(context.Background(), &deleteFailingReader{text: "CREATE TABLE fresh(v INT); INSERT INTO fresh VALUES(99);", err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	for _, script := range []string{
		"CREATE TABLE fresh(v INT); INSERT INTO fresh VALUES(99); malformed",
		"CREATE TABLE fresh(v INT); INSERT INTO fresh VALUES(99); DELETE FROM fresh",
	} {
		if err := e.Restore(context.Background(), strings.NewReader(script)); err == nil {
			t.Fatal("late replacement accepted", script)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.Restore(ctx, strings.NewReader("CREATE TABLE fresh(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != epoch || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address].Ends != ends || a.TransactionState() != session.TxActive || a.Transactions.Deletes[target.Address] != own {
		t.Fatal("failed replacement changed committed/private authority")
	}
	transactionRows(t, a, "SELECT v FROM t ORDER BY v", 7)
	transactionRows(t, b, "SELECT v FROM t ORDER BY v", 2)
	transactionEnd(t, a, ref, false)
	if !reflect.DeepEqual(tuples(old), [][]driver.Value{{int64(1)}, {int64(2)}}) {
		t.Fatal("failed replacement changed retained data")
	}
	lockEmpty(t, e)
}
