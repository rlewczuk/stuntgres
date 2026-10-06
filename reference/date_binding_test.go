//go:build reference

package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

func TestReferenceDateBindingRawMetadataAndNull(t *testing.T) {
	p := newReferencePair(t)
	for _, b := range []struct {
		name   string
		pg     bool
		db     *sql.DB
		record *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			for _, prepared := range []bool{false, true} {
				rows, err := b.db.QueryContext(p.ctx, "SELECT v FROM d")
				if err != nil {
					t.Fatal(err)
				}
				meta, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				scan := reflect.TypeFor[string]()
				if b.pg {
					scan = reflect.TypeFor[time.Time]()
				}
				if len(meta) != 1 || meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != scan {
					t.Fatal(meta)
				}
				if _, _, ok := meta[0].DecimalSize(); ok {
					t.Fatal("DATE precision")
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("empty DATE", rows.Err())
				}
				// EOF closes database/sql.Rows, not its captured ColumnType values.
				if meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != scan {
					t.Fatal("DATE metadata changed at EOF", meta[0])
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				if meta[0].DatabaseTypeName() != "DATE" || meta[0].ScanType() != scan {
					t.Fatal("DATE metadata changed after Close", meta[0])
				}
				if _, _, ok := meta[0].DecimalSize(); ok {
					t.Fatal("retained DATE precision")
				}
				for i, arg := range []any{[]byte("2000-02-29"), time.Date(2000, 1, 1, 0, 1, 0, 0, time.FixedZone("east", 14*3600)), time.Date(1999, 12, 31, 23, 59, 0, 0, time.FixedZone("west", -12*3600)), time.Date(0, 2, 29, 0, 0, 0, 0, time.UTC), "infinity", "-infinity", nil} {
					s := referenceExec("INSERT INTO d VALUES($1,$2)", 1, i, arg)
					s.prepared = prepared
					run(s)
				}
				rows, err = b.db.QueryContext(p.ctx, "SELECT v FROM d ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				expected := []any{"2000-02-29", "2000-01-01", "1999-12-31", "0001-02-29 BC", "infinity", "-infinity", nil}
				if b.pg {
					// pq formats BC time.Time by AddDate into AD; year 0's
					// leap day normalizes to March 1 in nonleap AD year 1.
					expected = []any{time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(0, 3, 1, 0, 0, 0, 0, time.UTC), []byte("infinity"), []byte("-infinity"), nil}
				}
				for _, want := range expected {
					if !rows.Next() {
						t.Fatal("missing row", rows.Err())
					}
					var got any
					if err := rows.Scan(&got); err != nil {
						t.Fatal(err)
					}
					if date, ok := want.(time.Time); ok {
						actual, valid := got.(time.Time)
						if !valid || !actual.Equal(date) {
							t.Fatalf("raw DATE %#v (%T) want %v (time.Time)", got, got, date)
						}
					} else if !reflect.DeepEqual(got, want) {
						t.Fatalf("raw DATE %#v (%T) want %#v (%T)", got, got, want, want)
					}
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("extra row", rows.Err())
				}
				rows.Close()
				var unknown any
				if err := b.db.QueryRowContext(p.ctx, "SELECT v=NULL FROM d WHERE id=0").Scan(&unknown); err != nil || unknown != nil {
					t.Fatal(unknown, err)
				}
				run(referenceExec("DROP TABLE d", 0))
				run(referenceExec("CREATE TABLE d(id INTEGER,v DATE)", 0))
			}
		})
	}
}

func TestReferenceDateOrderingAndOperators(t *testing.T) {
	p := newReferencePair(t)
	inputs := []string{"-infinity", "4714-11-24 BC", "0002-12-31 BC", "0001-01-01 BC", "0001-12-31 BC", "0001-01-01", "1900-02-28", "1900-03-01", "1999-12-31", "2000-01-01", "2000-02-28", "2000-02-29", "2000-03-01", "5874897-12-31", "infinity"}
	finite := []time.Time{time.Date(-4713, 11, 24, 0, 0, 0, 0, time.UTC), time.Date(-1, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(0, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1900, 2, 28, 0, 0, 0, 0, time.UTC), time.Date(1900, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(1999, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2000, 2, 28, 0, 0, 0, 0, time.UTC), time.Date(2000, 2, 29, 0, 0, 0, 0, time.UTC), time.Date(2000, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(5874897, 12, 31, 0, 0, 0, 0, time.UTC)}
	for _, b := range []struct {
		name   string
		pg     bool
		db     *sql.DB
		record *referenceRecorder
	}{{"native", false, p.memory, p.memoryRecord}, {"postgres", true, p.postgres, p.postgresRecord}} {
		t.Run(b.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.record, s) }
			run(referenceExec("CREATE TABLE d(v DATE,w DATE)", 0))
			want := make([][]any, 0, len(inputs)+1)
			for i, input := range inputs {
				run(referenceExec("INSERT INTO d VALUES($1,$1)", 1, input))
				var v any = input
				if b.pg && i > 0 && i < len(inputs)-1 {
					v = finite[i-1]
				}
				want = append(want, []any{v})
			}
			run(referenceExec("INSERT INTO d VALUES(NULL,NULL)", 1))
			want = append(want, []any{nil})
			for _, prepared := range []bool{false, true} {
				s := referenceSelect("SELECT v FROM d ORDER BY v", []string{"v"}, []string{"DATE"}, want)
				s.prepared = prepared
				run(s)
				rev := make([][]any, len(want))
				for i := range want {
					rev[i] = want[len(want)-1-i]
				}
				s = referenceSelect("SELECT v FROM d ORDER BY v DESC", []string{"v"}, []string{"DATE"}, rev)
				s.prepared = prepared
				run(s)
				for _, tc := range []struct {
					op   string
					want bool
				}{{"=", false}, {"<>", true}, {"!=", true}, {"<", true}, {"<=", true}, {">", false}, {">=", false}} {
					s = referenceSelect("SELECT v"+tc.op+"DATE '2000-03-01',DATE '2000-02-28'"+tc.op+"v,v=w FROM d WHERE v=$1", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{tc.want, tc.want, true}}, "2000-02-29")
					s.prepared = prepared
					run(s)
					s = referenceSelect("SELECT v FROM d WHERE $1"+tc.op+"v ORDER BY v", []string{"v"}, []string{"DATE"}, nil, nil)
					s.prepared = prepared
					run(s)
					s = referenceSelect("SELECT $1"+tc.op+"v FROM d WHERE v='2000-02-29'", []string{"?column?"}, []string{"BOOL"}, [][]any{{tc.want}}, "2000-02-28")
					s.prepared = prepared
					run(s)
					indices := map[string][]int{"=": {11}, "<>": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14}, "!=": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 12, 13, 14}, "<": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, "<=": {0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}, ">": {12, 13, 14}, ">=": {11, 12, 13, 14}}
					for _, leftParameter := range []bool{false, true} {
						op := tc.op
						predicate := "v" + op + "$1"
						if leftParameter {
							predicate = "$1" + op + "v"
							op = map[string]string{"=": "=", "<>": "<>", "!=": "!=", "<": ">", "<=": ">=", ">": "<", ">=": "<="}[op]
						}
						selected := make([][]any, 0, len(indices[op]))
						for _, i := range indices[op] {
							selected = append(selected, want[i])
						}
						s = referenceSelect("SELECT v FROM d WHERE "+predicate+" ORDER BY v", []string{"v"}, []string{"DATE"}, selected, "2000-02-29")
						s.prepared = prepared
						run(s)
					}
				}
			}
		})
	}
}
