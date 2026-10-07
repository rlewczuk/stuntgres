//go:build reference

package reference_test

import (
	"database/sql"
	"slices"
	"testing"
)

func TestReferenceQueryOrderRepeatedHiddenVolatileRegression(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE v(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO v VALUES(1),(2),(3)", 3))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			where string
			rows  [][]any
			next  int64
		}{
			{"", [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, 4},
			{"WHERE id>1", [][]any{{int64(2)}, {int64(3)}}, 3},
		} {
			p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
			step := referenceSelect("SELECT id FROM v "+tc.where+" ORDER BY nextval('s'),nextval('s') DESC", []string{"id"}, []string{"INT4"}, tc.rows)
			// Input traversal is not an oracle. The admitted bag and subsequent
			// sequence value independently distinguish shared SQL99 keys.
			step.bag, step.prepared = true, prepared
			p.step(t, step)
			p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{tc.next}}))
			if prepared {
				p.step(t, step)
				p.step(t, referenceSelect("SELECT nextval('s')", []string{"nextval"}, []string{"INT8"}, [][]any{{2 * tc.next}}))
			}
			p.step(t, referenceExec("DROP SEQUENCE s", 0))
		}
	}
}

func TestReferenceQueryOrderRepeatedHiddenPreparedFreshness(t *testing.T) {
	p := newReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE v(id INT)", 0))
	p.step(t, referenceExec("INSERT INTO v VALUES(1),(2),(3),(4),(5)", 5))
	p.step(t, referenceExec("CREATE SEQUENCE s START WITH 1", 0))
	for _, backend := range []struct {
		name string
		db   *sql.DB
	}{{"memory", p.memory}, {"postgres", p.postgres}} {
		t.Run(backend.name, func(t *testing.T) {
			stmt, err := backend.db.PrepareContext(p.ctx, "SELECT id FROM v WHERE id>2 ORDER BY nextval('s'),nextval('s') DESC")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			for _, nextWant := range []int64{4, 8} {
				rows, err := stmt.QueryContext(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				names, err := rows.Columns()
				if err != nil || !slices.Equal(names, []string{"id"}) {
					rows.Close()
					t.Fatal(names, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT4" {
					rows.Close()
					t.Fatal(types, err)
				}
				var ids []int64
				for rows.Next() {
					var id int64
					if err := rows.Scan(&id); err != nil {
						rows.Close()
						t.Fatal(err)
					}
					ids = append(ids, id)
				}
				err = rows.Err()
				rows.Close()
				slices.Sort(ids)
				if err != nil || !slices.Equal(ids, []int64{3, 4, 5}) {
					t.Fatal(ids, err)
				}
				var next int64
				if err := backend.db.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != nextWant {
					t.Fatal(next, nextWant, err)
				}
			}
		})
	}
}
