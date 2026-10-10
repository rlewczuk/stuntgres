package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceDeleteUsingSequenceDemand(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(referenceExec("CREATE SEQUENCE draws START 101 CACHE 1", 0))
		q := "DELETE FROM t d USING s x WHERE d.id=x.k RETURNING nextval('draws')"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(referenceSelect("SELECT last_value,is_called FROM draws", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(101), false}}))
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		step := referenceSelect(q, []string{"nextval"}, []string{"INT8"}, [][]any{{int64(101)}, {int64(102)}, {int64(103)}})
		step.bag = true
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		step.rows = nil
		referenceRunStep(t, p.ctx, held, b.recorder, step)
		run(referenceSelect("SELECT nextval('draws')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(104)}}))
		run(deleteUsingTargets(deleteUsingSurvivors()))
		deleteUsingSourcesUnchanged(run)
	})
}

func TestReferenceDeleteUsingCancellation(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		if b.pg {
			// lib/pq may finish a fast command before reporting precancellation.
			// PostgreSQL cancellation is asserted at an admitted server wait in
			// TestReferenceDeleteUsingWaitTimeout instead.
			return
		}
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		q := "DELETE FROM t d USING s x WHERE d.id=x.k RETURNING d.id"
		result, err := c.ExecContext(ctx, q)
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
		rows, err := c.QueryContext(ctx, q)
		if rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(rows, err)
		}
		run(deleteUsingTargets(deleteUsingOriginal()))
		deleteUsingSourcesUnchanged(run)
	})
}

func TestReferenceDeleteUsingControls(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		updateFromRun(t, p, b, tx)(referenceExec("DELETE FROM t d USING s x WHERE d.id=x.k", 3))
		updateFromRun(t, p, b, tx)(deleteUsingTargets(deleteUsingSurvivors()))
		updateFromRun(t, p, b, b.db)(deleteUsingTargets(deleteUsingOriginal()))
		var private bytes.Buffer
		if !b.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &private); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(deleteUsingTargets(deleteUsingOriginal()))
		if !b.pg {
			var committed bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &committed); err != nil || private.String() != committed.String() {
				t.Fatal("private deletion changed committed dump", err, private.String(), committed.String())
			}
		}
		run(referenceExec("DELETE FROM t d USING s x WHERE d.id=x.k", 3))
		run(deleteUsingTargets(deleteUsingSurvivors()))
		deleteUsingSourcesUnchanged(run)
		if b.pg {
			return
		}
		var saved bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &saved); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(saved.String(), "DELETE") {
			t.Fatal("dump contains deletion history", saved.String())
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(saved.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(deleteUsingTargets(deleteUsingSurvivors()))
		deleteUsingSourcesUnchanged(run)
		peer := sql.OpenDB(p.nativeConnector)
		defer peer.Close()
		deleteUsingNonContextRun(t, p, b, peer)(deleteUsingTargets(deleteUsingSurvivors()))
	})
}

func TestReferenceDeleteUsingRetainedOutput(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		q := "DELETE FROM t d USING s x WHERE d.id=x.k RETURNING d.id,d.v,x.label,new.id"
		want := [][]any{{int64(2), int64(20), "two", nil}, {int64(2), int64(20), "two", nil}, {int64(3), int64(30), "three", nil}}
		if b.pg {
			run(referenceExec("BEGIN", 0))
			step := referenceSelect(q, []string{"id", "v", "label", "id"}, []string{"INT4", "INT4", "TEXT", "INT4"}, want)
			step.bag = true
			run(step)
			run(referenceExec("ROLLBACK", 0))
			run(deleteUsingTargets(deleteUsingOriginal()))
			return
		}
		raw, err := p.nativeConnector.Connect(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer raw.Close()
		if _, err := raw.(driver.ExecerContext).ExecContext(p.ctx, "BEGIN", nil); err != nil {
			t.Fatal(err)
		}
		retained, err := raw.(driver.QueryerContext).QueryContext(p.ctx, q, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		if _, err := raw.(driver.ExecerContext).ExecContext(p.ctx, "ROLLBACK", nil); err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retained.Columns(), []string{"id", "v", "label", "id"}) {
			t.Fatal(retained.Columns())
		}
		for i, name := range []string{"INT4", "INT4", "TEXT", "INT4"} {
			if retained.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name {
				t.Fatal("retained source metadata", i)
			}
		}
		var got [][]any
		dst := make([]driver.Value, 4)
		for {
			err := retained.Next(dst)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			row := make([]any, len(dst))
			for i := range dst {
				row[i] = dst[i]
			}
			got = append(got, row)
		}
		// Identical duplicate outputs make ordering irrelevant; compare as a bag.
		if len(got) != 3 {
			t.Fatal(got)
		}
		counts := map[int64]int{}
		for _, row := range got {
			id := row[0].(int64)
			label := map[int64]string{2: "two", 3: "three"}[id]
			if !reflect.DeepEqual(row, []any{id, id * 10, label, nil}) {
				t.Fatal(row)
			}
			counts[id]++
		}
		if counts[2] != 2 || counts[3] != 1 {
			t.Fatal(counts)
		}
		run(deleteUsingTargets(deleteUsingOriginal()))
		deleteUsingSourcesUnchanged(run)
	})
}
