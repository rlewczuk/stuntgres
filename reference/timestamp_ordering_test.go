package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceTimestampOrderingAndNativeDump(t *testing.T) {
	p := newTimestampReferenceScenario(t)
	referenceTimestampStep(t, p, referenceExec("CREATE TABLE legacy_t(id INTEGER,v TIMESTAMP,p TIMESTAMP(0))", 0))
	inputs := []any{"-infinity", "0001-01-01 BC", "epoch", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-01-01", "2000-01-01 00:00:00.000001", "infinity", nil}
	for i, input := range inputs {
		referenceTimestampStep(t, p, referencePrepared(referenceExec("INSERT INTO legacy_t VALUES($1,$2,$2)", 1, i+1, input)))
	}
	// Query only the key: equal-key duplicates assert bag multiplicity without
	// inventing a secondary order for distinct row identifiers.
	expected := [][]any{{"-infinity"}, {time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(1999, 12, 31, 23, 59, 59, 999999000, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(2000, 1, 1, 0, 0, 0, 1000, time.UTC)}, {"infinity"}, {nil}}
	referenceTimestampStep(t, p, referenceSelect("SELECT v FROM legacy_t ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, expected))
	reverse := make([][]any, len(expected))
	for i := range expected {
		reverse[i] = expected[len(expected)-1-i]
	}
	referenceTimestampStep(t, p, referencePrepared(referenceSelect("SELECT v FROM legacy_t ORDER BY v DESC", []string{"v"}, []string{"TIMESTAMP"}, reverse)))
	referenceTimestampStep(t, p, referencePrepared(referenceSelect("SELECT id FROM legacy_t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}}, "2000-01-01")))
	referenceTimestampStep(t, p, referencePrepared(referenceSelect("SELECT id FROM legacy_t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)))
	referenceTimestampStep(t, p, referenceSelect("SELECT id FROM legacy_t WHERE p='2000-01-01 00:00:00.4'", []string{"id"}, []string{"INT4"}, nil))
	// Native controls retain their own authority; use the dumped SQL as an input
	// witness on real PG, not a false claim of PostgreSQL Reset/Restore APIs.
	native := p
	if e := stuntgres.Restore(native.ctx, native.native, bytes.NewBufferString("CREATE TABLE saved(v TIMESTAMP(6)); INSERT INTO saved VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999'),('0001-01-01 BC'),('infinity'),(NULL)")); e != nil {
		t.Fatal(e)
	}
	var dump bytes.Buffer
	if e := stuntgres.Dump(native.ctx, native.native, &dump); e != nil {
		t.Fatal(e)
	}
	if e := stuntgres.Reset(native.ctx, native.native); e != nil {
		t.Fatal(e)
	}
	if e := stuntgres.Restore(native.ctx, native.native, bytes.NewReader(dump.Bytes())); e != nil {
		t.Fatal(e)
	}
	var again bytes.Buffer
	if e := stuntgres.Dump(native.ctx, native.native, &again); e != nil || again.String() != dump.String() {
		t.Fatal(again.String(), e)
	}
	if native.postgres != nil {
		if _, e := native.postgres.ExecContext(native.ctx, dump.String()); e != nil {
			t.Fatal(e)
		}
	}
	referenceTimestampStep(t, native, referenceSelect("SELECT v FROM saved ORDER BY v", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}, {time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}, {"infinity"}, {nil}}))
	t.Run("direct-order-and-hidden-peers", func(t *testing.T) {
		p := newTimestampReferenceScenario(t)
		for _, backend := range p.backends() {
			t.Run(backend.name, func(t *testing.T) {
				db := backend.db
				ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
				referenceTimestampExec(t, ctx, db, "CREATE TABLE t(id INTEGER NOT NULL,v TIMESTAMP,p0 TIMESTAMP(0),p3 TIMESTAMP(3),p6 TIMESTAMP(6))")
				input := []any{"-infinity", "0001-01-01 BC", "epoch", "1999-12-31 23:59:59.999999", "2000-01-01", "2000-01-01", "2000-01-01 00:00:00.000001", "infinity", nil}
				for i, s := range input {
					referenceTimestampExec(t, ctx, db, "INSERT INTO t VALUES($1,$2,$2,$2,$2)", i+1, s)
				}
				referenceTimestampExec(t, ctx, db, "CREATE TABLE endpoint(v TIMESTAMP(6))")
				heldEndpoint, e := db.PrepareContext(ctx, "INSERT INTO endpoint VALUES(TIMESTAMP(0) '294276-12-31 23:59:59.999999')")
				if e != nil {
					t.Fatal(e)
				}
				if _, e = heldEndpoint.ExecContext(ctx); e != nil {
					t.Fatal(e)
				}
				heldEndpoint.Close()
				referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT v FROM endpoint", []string{"v"}, []string{"TIMESTAMP"}, [][]any{{time.Date(294277, 1, 1, 0, 0, 0, 0, time.UTC)}})
				asc := [][]any{}
				desc := [][]any{}
				for i := range 9 {
					asc = append(asc, []any{int64(i + 1)})
					desc = append(desc, []any{int64(9 - i)})
				}
				// Duplicate midnight keys have no portable secondary order: assert their bag.
				for _, q := range []string{"SELECT id FROM t ORDER BY v", "SELECT id FROM t ORDER BY v DESC"} {
					rows, e := db.QueryContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					var ids []int64
					for rows.Next() {
						var id int64
						if e = rows.Scan(&id); e != nil {
							t.Fatal(e)
						}
						ids = append(ids, id)
					}
					e = rows.Err()
					rows.Close()
					if e != nil || len(ids) != 9 {
						t.Fatal(ids, e)
					}
					want := asc
					if strings.HasSuffix(q, "DESC") {
						want = desc
					}
					duplicates := map[int64]int{}
					for i, id := range ids {
						expected := want[i][0].(int64)
						if (expected == 5 || expected == 6) && (id == 5 || id == 6) {
							duplicates[id]++
							continue
						}
						if id != expected {
							t.Fatal(ids, want)
						}
					}
					if duplicates[5] != 1 || duplicates[6] != 1 {
						t.Fatal("duplicate-key multiplicity", ids)
					}
				}
				referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT id FROM t WHERE v='2000-01-01' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}, {int64(6)}})
				referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}, "infinity")
				referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT id FROM t WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}, "-infinity")
				referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT id FROM t WHERE v=$1", []string{"id"}, []string{"INT4"}, nil, nil)
				referenceTimestampQuery(t, ctx, db, backend.recorder, "SELECT id FROM t WHERE p0='2000-01-01 00:00:00.4'", []string{"id"}, []string{"INT4"}, nil)
			})
		}
	})
}
