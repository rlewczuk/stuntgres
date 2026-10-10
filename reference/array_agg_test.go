package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
)

func arrayAggSetup(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE aa_scalar(id INT,v INT)", 0))
	run(referenceExec("INSERT INTO aa_scalar VALUES(1,3),(2,NULL),(3,1),(4,NULL),(5,3)", 5))
	run(referenceExec("CREATE TABLE aa_array(id INT,v INT[])", 0))
	run(referenceExec("INSERT INTO aa_array VALUES(1,'{1,NULL}'),(2,'{3,4}')", 2))
}

func arrayAggSelect(q, typ string, rows [][]any, pg bool, args ...any) referenceStep {
	s := arrayReferenceSelect(q, []string{"array_agg"}, []string{typ}, []string{"_INT4"}, rows, pg, args...)
	s.checkMetadata = arrayAggMetadata(pg, []string{"_INT4"})
	return s
}

func arrayAggMetadata(pg bool, families []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != len(families) {
			t.Fatalf("metadata columns=%d want %d", len(cols), len(families))
		}
		for i, c := range cols {
			want := reflect.TypeFor[string]()
			if pg {
				want = reflect.TypeFor[any]()
			}
			if c.ScanType() != want {
				t.Fatalf("%s ScanType=%v want %v", c.Name(), c.ScanType(), want)
			}
			if n, known := c.Nullable(); n != !pg || known != !pg {
				t.Fatalf("nullable=%t/%t", n, known)
			}
			if n, known := c.Length(); n != 0 || known {
				t.Fatalf("length=%d/%t", n, known)
			}
			p, s, known := c.DecimalSize()
			if pg && families[i] == "_NUMERIC" {
				if p != 65535 || s != 65531 || !known {
					t.Fatalf("numeric precision=%d/%d/%t", p, s, known)
				}
			} else if p != 0 || s != 0 || known {
				t.Fatalf("precision=%d/%d/%t", p, s, known)
			}
		}
	}
}

func arrayAggBoth(run func(referenceStep), s referenceStep) {
	run(s)
	run(referencePrepared(s))
}

func TestReferenceArrayAggScalars(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			arrayAggSetup(run)
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{
				{"SELECT array_agg(v ORDER BY id) FROM aa_scalar", [][]any{{"{3,NULL,1,NULL,3}"}}},
				{"SELECT array_agg(ALL v ORDER BY id) FROM aa_scalar", [][]any{{"{3,NULL,1,NULL,3}"}}},
				{"SELECT array_agg(v ORDER BY v DESC NULLS LAST,id) FROM aa_scalar", [][]any{{"{3,3,1,NULL,NULL}"}}},
				{"SELECT array_agg(DISTINCT v ORDER BY v) FROM aa_scalar", [][]any{{"{1,3,NULL}"}}},
				{"SELECT array_agg(v ORDER BY id) FILTER(WHERE v IS NULL) FROM aa_scalar", [][]any{{"{NULL,NULL}"}}},
				{"SELECT array_agg(NULL::integer ORDER BY id) FROM aa_scalar WHERE id<=3", [][]any{{"{NULL,NULL,NULL}"}}},
				{"SELECT array_agg(NULL::integer) FROM aa_scalar WHERE id<=3", [][]any{{"{NULL,NULL,NULL}"}}},
				{"SELECT array_agg(v) FROM aa_scalar WHERE FALSE", [][]any{{nil}}},
				{"SELECT array_agg(v) FILTER(WHERE FALSE) FROM aa_scalar", [][]any{{nil}}},
				{"SELECT array_agg(v) FROM aa_scalar WHERE FALSE GROUP BY id", nil},
				{"SELECT array_agg(v) FROM aa_scalar WHERE FALSE GROUP BY ()", [][]any{{nil}}},
				{"SELECT array_agg(NULL::integer)", [][]any{{"{NULL}"}}},
				{"SELECT array_agg(v ORDER BY 1,id DESC) FROM aa_scalar", [][]any{{"{3,NULL,1,NULL,3}"}}},
				{"SELECT array_agg(v ORDER BY id) FROM (SELECT id,v FROM aa_scalar WHERE id<=3) AS source", [][]any{{"{3,NULL,1}"}}},
				{"SELECT array_agg(v ORDER BY id) FROM aa_scalar HAVING count(v)=3 ORDER BY count(v)", [][]any{{"{3,NULL,1,NULL,3}"}}},
			} {
				arrayAggBoth(run, arrayAggSelect(tc.q, "_INT4", tc.rows, b.pg))
			}
			arrayAggBoth(run, referenceSelect("SELECT array_agg(v ORDER BY id),count(v),string_agg(v::text,',' ORDER BY id) FROM aa_scalar", []string{"array_agg", "count", "string_agg"}, []string{"_INT4", "INT8", "TEXT"}, [][]any{{"{3,NULL,1,NULL,3}", int64(3), "3,1,3"}}))
			// Unordered aggregation is checked as a multiplicity bag, not scan order.
			s := arrayAggSelect("SELECT array_agg(v) FROM aa_scalar", "_INT4", nil, b.pg)
			s.checkScan = func(t *testing.T, rows *sql.Rows, _ int) {
				var v any
				if err := rows.Scan(&v); err != nil {
					t.Fatal(err)
				}
				referenceAssertRawValue(t, b.pg, "_INT4", v)
				text := referenceNormalize(v).(string)
				bag := map[string]int{}
				for _, item := range strings.Split(strings.Trim(text, "{}"), ",") {
					bag[item]++
				}
				if !reflect.DeepEqual(bag, map[string]int{"3": 2, "1": 1, "NULL": 2}) {
					t.Fatalf("unordered elements=%v", bag)
				}
			}
			// checkScan is supplementary; the fixture still needs its full row.
			// Separate one-row bag scan avoids a fabricated normalized row golden.
			for _, prepared := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := b.db.PrepareContext(p.ctx, s.query)
					if e != nil {
						t.Fatal(e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.ctx)
				} else {
					rows, err = b.db.QueryContext(p.ctx, s.query)
				}
				if err != nil {
					t.Fatal(err)
				}
				s.checkMetadata(t, rows)
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing global row", rows.Err())
				}
				s.checkScan(t, rows, 0)
				if rows.Next() || rows.Err() != nil {
					rows.Close()
					t.Fatal("unexpected extra row/error", rows.Err())
				}
				rows.Close()
			}
		})
	}
}

func TestReferenceArrayAggArrays(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			arrayAggSetup(run)
			for _, tc := range []struct {
				q    string
				rows [][]any
			}{
				{"SELECT array_agg(v ORDER BY id) FROM aa_array", [][]any{{"{{1,NULL},{3,4}}"}}},
				{"SELECT array_agg(v) FROM aa_array WHERE id=1", [][]any{{"{{1,NULL}}"}}},
				{"SELECT array_agg(v) FROM aa_array WHERE FALSE", [][]any{{nil}}},
				{"SELECT array_agg(v) FROM aa_array WHERE FALSE GROUP BY id", nil},
				{"SELECT array_agg('{{{{{7}}}}}'::integer[])", [][]any{{"{{{{{{7}}}}}}"}}},
			} {
				arrayAggBoth(run, arrayAggSelect(tc.q, "_INT4", tc.rows, b.pg))
			}
			run(referenceExec("CREATE TABLE aa_bounds(id INT,v INT[])", 0))
			run(referenceExec("INSERT INTO aa_bounds VALUES(1,'[-1:0][3:4]={{1,NULL},{2,3}}'),(2,'[-1:0][3:4]={{4,5},{NULL,6}}')", 2))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_bounds", "_INT4", [][]any{{"[1:2][-1:0][3:4]={{{1,NULL},{2,3}},{{4,5},{NULL,6}}}"}}, b.pg))
		})
	}
}
