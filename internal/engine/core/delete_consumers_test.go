package core_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func TestDeleteCoreControls(t *testing.T) {
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(id INT NOT NULL,label TEXT,flag BOOL)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1,'gone',TRUE),(2,'keep',FALSE),(2,'keep',FALSE)")
	retained := enginetest.SessionSQL(t, b, "SELECT * FROM t ORDER BY id")
	defer retained.Rows.Close()
	before := enginetest.Tuples(retained)
	ref := enginetest.TransactionBegin(t, a)
	deleteCount(t, a, "DELETE FROM t WHERE id=1", 1)
	var private bytes.Buffer
	if err := engine.Dump(context.Background(), e, &private); err != nil || !strings.Contains(private.String(), "'gone'") {
		t.Fatal("Dump exposed private deletion", err, private.String())
	}
	enginetest.TransactionEnd(t, a, ref, false)
	deleteCount(t, b, "DELETE FROM t WHERE id=1", 1)
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "'gone'") || strings.Contains(dump.String(), "DELETE") || strings.Count(dump.String(), "'keep'") != 2 {
		t.Fatal("noncanonical survivors", dump.String())
	}
	target := engine.New()
	if err := engine.Restore(context.Background(), target, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	want := [][]driver.Value{{int64(2), "keep", false}, {int64(2), "keep", false}}
	if got := enginetest.Tuples(enginetest.Run(t, target, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	_, err := enginetest.Execute(target, context.Background(), enginetest.Statement(t, "INSERT INTO t VALUES(NULL,'bad',FALSE)"), nil)
	enginetest.State(t, err, "23502")
	epoch := target.CatalogRead().Epoch()
	if err := engine.Restore(context.Background(), target, strings.NewReader("DELETE FROM t")); err == nil || target.CatalogRead().Epoch() != epoch {
		t.Fatal("Restore DELETE whitelist widened", err)
	}
	if got := enginetest.Tuples(enginetest.Run(t, target, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, want) {
		t.Fatal("failed Restore changed rows", got)
	}
	if err := engine.Reset(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(enginetest.Tuples(retained), before) {
		t.Fatal("replacement invalidated original cursor")
	}
}

func TestDeleteCoreConsumers(t *testing.T) {
	t.Run("not_null_and_drop_shape", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE t(a INT,b TEXT,c BOOL)")
		enginetest.SessionSQL(t, s, "INSERT INTO t VALUES(NULL,'gone',TRUE),(7,'keep',FALSE)")
		ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
		old := e.Storage.Relations[ref.Address]
		retained := enginetest.SessionSQL(t, s, "SELECT * FROM t")
		defer retained.Rows.Close()
		deleteCount(t, s, "DELETE FROM t WHERE b='gone'", 1)
		ends := old.Ends
		enginetest.SessionSQL(t, s, "ALTER TABLE t ALTER a SET NOT NULL")
		enginetest.SessionSQL(t, s, "ALTER TABLE t DROP COLUMN b")
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
		got := enginetest.SessionSQL(t, s, "SELECT * FROM t")
		defer got.Rows.Close()
		if !reflect.DeepEqual(enginetest.Tuples(got), [][]driver.Value{{int64(7), false}}) {
			t.Fatal(enginetest.Tuples(got))
		}
		if !reflect.DeepEqual(enginetest.Tuples(retained), [][]driver.Value{{nil, "gone", true}, {int64(7), "keep", false}}) {
			t.Fatal("old width changed")
		}
		enginetest.SessionSQL(t, s, "ALTER TABLE t DROP COLUMN a")
		enginetest.SessionSQL(t, s, "ALTER TABLE t DROP COLUMN c")
		zero := enginetest.SessionSQL(t, s, "SELECT * FROM t")
		defer zero.Rows.Close()
		if zero.Count != 1 || len(zero.Rows.Schema()) != 0 || len(enginetest.Tuples(zero)) != 1 {
			t.Fatal("zero width lost occurrence", zero.Count)
		}
		var populated bytes.Buffer
		if err := engine.Dump(context.Background(), e, &populated); err != nil || strings.Count(populated.String(), "DEFAULT VALUES") != 1 {
			t.Fatal("zero-width live Dump", err, populated.String())
		}
		populatedTarget := engine.New()
		if err := engine.Restore(context.Background(), populatedTarget, bytes.NewReader(populated.Bytes())); err != nil || enginetest.Run(t, populatedTarget, "SELECT * FROM t").Count != 1 {
			t.Fatal("zero-width live Restore", err)
		}
		deleteCount(t, s, "DELETE FROM t", 1)
		if len(ends.Rows) != 1 || e.Storage.Relations[ref.Address].Ends == ends || len(e.Storage.Relations[ref.Address].Ends.Rows) != 2 {
			t.Fatal("committed deletion root mutated/reused")
		}
		deleteCount(t, s, "DELETE FROM t", 0)
		var dump bytes.Buffer
		if err := engine.Dump(context.Background(), e, &dump); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(dump.String(), "DEFAULT VALUES") {
			t.Fatal("zero-width deleted occurrence resurrected", dump.String())
		}
		restored := engine.New()
		if err := engine.Restore(context.Background(), restored, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if enginetest.Run(t, restored, "SELECT * FROM t").Count != 0 {
			t.Fatal("empty zero-width restore")
		}
	})
	t.Run("live_null_rejects", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE t(v INT)")
		enginetest.Run(t, e, "INSERT INTO t VALUES(NULL),(7)")
		_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE t ALTER v SET NOT NULL"), nil)
		enginetest.State(t, err, "23502")
		enginetest.Run(t, e, "DELETE FROM t WHERE v=7")
		_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "ALTER TABLE t ALTER v SET NOT NULL"), nil)
		enginetest.State(t, err, "23502")
		enginetest.Run(t, e, "DELETE FROM t")
		enginetest.Run(t, e, "ALTER TABLE t ALTER v SET NOT NULL")
		_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "INSERT INTO t VALUES(NULL)"), nil)
		enginetest.State(t, err, "23502")
	})
	t.Run("shape_end_token_fence", func(t *testing.T) {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE t(a INT,b INT)")
		enginetest.Run(t, e, "INSERT INTO t VALUES(1,2),(3,4)")
		stage, _, _ := alterDropTestStage(t, e)
		defer e.DiscardCatalogStage(stage)
		prepared, err := e.ValidateCatalogStage(context.Background(), stage)
		if err != nil {
			t.Fatal(err)
		}
		enginetest.Run(t, e, "DELETE FROM t WHERE a=1")
		_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: e.CatalogRead().Epoch()})
		var conflict catalog.Conflict
		if !errors.As(err, &conflict) {
			t.Fatal("delete did not fence captured shape", err)
		}
		if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t ORDER BY a")); !reflect.DeepEqual(got, [][]driver.Value{{int64(3), int64(4)}}) {
			t.Fatal(got)
		}
	})
}

func TestDeleteCoreCatalogReplacement(t *testing.T) {
	source := engine.New()
	enginetest.Run(t, source, "CREATE TABLE t(v INT)")
	enginetest.Run(t, source, "INSERT INTO t VALUES(1),(1),(2)")
	retained := enginetest.Run(t, source, "SELECT v FROM t ORDER BY v")
	enginetest.Run(t, source, "DELETE FROM t WHERE v=1")
	ref := enginetest.CatalogRef(t, source, source.CatalogRead(), "t")
	oldStore := source.Storage.Relations[ref.Address]
	oldLive := oldStore.Versions[2]
	if _, err := source.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	self := source.Storage.Relations[ref.Address]
	if self == oldStore || len(self.Versions) != 1 || self.Ends != nil || self.Versions[0].Row == oldLive.Row || self.Versions[0].Version == oldLive.Version || self.Versions[0].Born != source.Visibility.CommitSeq() {
		t.Fatal("self replacement did not detach/remint live heads")
	}
	if got := enginetest.Tuples(enginetest.Run(t, source, "SELECT v FROM t ORDER BY v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
		t.Fatal(got)
	}
	target := engine.New()
	enginetest.Run(t, target, "CREATE TABLE sentinel(v INT)")
	enginetest.Run(t, target, "INSERT INTO sentinel VALUES(99)")
	if _, err := target.ReplaceCatalog(context.Background(), source, source.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	imported := enginetest.CatalogRef(t, target, target.CatalogRead(), "t")
	store := target.Storage.Relations[imported.Address]
	if len(store.Versions) != 1 || store.Ends != nil || store.Versions[0].Row == 0 || store.Versions[0].Version == 0 || store.Versions[0].Born != target.Visibility.CommitSeq() {
		t.Fatal("foreign replacement imported history", store)
	}
	enginetest.Run(t, target, "INSERT INTO t VALUES(3)")
	if got := enginetest.Tuples(enginetest.Run(t, source, "SELECT v FROM t ORDER BY v")); !reflect.DeepEqual(got, [][]driver.Value{{int64(2)}}) {
		t.Fatal("foreign append changed source", got)
	}
	if got := enginetest.Tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(1)}, {int64(1)}, {int64(2)}}) {
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
	e := engine.New()
	a := enginetest.StartSession(t, e, nil)
	b := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, b, "CREATE TABLE t(v INT)")
	enginetest.SessionSQL(t, b, "INSERT INTO t VALUES(1),(2)")
	old := enginetest.SessionSQL(t, b, "SELECT v FROM t ORDER BY v")
	defer old.Rows.Close()
	deleteCount(t, b, "DELETE FROM t WHERE v=1", 1)
	ref := enginetest.TransactionBegin(t, a)
	enginetest.SessionSQL(t, a, "INSERT INTO t VALUES(7)")
	deleteCount(t, a, "DELETE FROM t WHERE v=2", 1)
	target := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	epoch, frontier, ends := e.CatalogRead().Epoch(), e.Visibility.CommitSeq(), e.Storage.Relations[target.Address].Ends
	own := a.Transactions.Deletes[target.Address]
	sentinel := errors.New("late replacement reader")
	if err := engine.Restore(context.Background(), e, &deleteFailingReader{text: "CREATE TABLE fresh(v INT); INSERT INTO fresh VALUES(99);", err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	for _, script := range []string{
		"CREATE TABLE fresh(v INT); INSERT INTO fresh VALUES(99); malformed",
		"CREATE TABLE fresh(v INT); INSERT INTO fresh VALUES(99); DELETE FROM fresh",
	} {
		if err := engine.Restore(context.Background(), e, strings.NewReader(script)); err == nil {
			t.Fatal("late replacement accepted", script)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := engine.Restore(ctx, e, strings.NewReader("CREATE TABLE fresh(v INT)")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if e.CatalogRead().Epoch() != epoch || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[target.Address].Ends != ends || a.TransactionState() != session.TxActive || a.Transactions.Deletes[target.Address] != own {
		t.Fatal("failed replacement changed committed/private authority")
	}
	enginetest.TransactionRows(t, a, "SELECT v FROM t ORDER BY v", 7)
	enginetest.TransactionRows(t, b, "SELECT v FROM t ORDER BY v", 2)
	enginetest.TransactionEnd(t, a, ref, false)
	if !reflect.DeepEqual(enginetest.Tuples(old), [][]driver.Value{{int64(1)}, {int64(2)}}) {
		t.Fatal("failed replacement changed retained data")
	}
	enginetest.LockEmpty(t, e)
}

func alterDropTestStage(t *testing.T, e *core.Engine) (*core.CatalogStage, ids.ObjectRef, *catalog.Relation) {
	t.Helper()
	ref := enginetest.RoleTableRef(t, e, "t")
	old := e.Catalog.State.MustRelation(ref.Address)
	next := *old
	next.ObjectRevision++
	next.Columns = append([]catalog.Column(nil), old.Columns[0])
	next.Attributes = []int32{1}
	next.Index = map[string]int{"a": 0}
	next.Security.ColumnACL = nil
	original := e.Storage.Relations[ref.Address]
	replacement := &storage.Relation{Versions: make([]storage.RowVersion, len(original.Versions)), Ends: original.Ends}
	for i, row := range original.Versions {
		row.Values = append([]sqltype.Value(nil), row.Values[0])
		replacement.Versions[i] = row
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	if err := stage.AlterRelation(context.Background(), ref, &next, original, replacement, len(original.Versions), original.Generation, original.Ends); err != nil {
		t.Fatal(err)
	}
	return stage, ref, old
}
