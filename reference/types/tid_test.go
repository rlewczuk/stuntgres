package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func tidScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func tidQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

type tidValuer struct{ value driver.Value }

func (v tidValuer) Value() (driver.Value, error) { return v.value, nil }

func tidMetadata(t *testing.T, rows *sql.Rows, pg, declared bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 {
		t.Fatal(columns, err)
	}
	c := columns[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if c.Name() != "v" || c.DatabaseTypeName() != "TID" || c.ScanType() != scan {
		t.Fatalf("TID metadata: name=%s type=%s scan=%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
	}
	if _, ok := c.Length(); ok {
		t.Fatal("unexpected TID length")
	}
	if _, _, ok := c.DecimalSize(); ok {
		t.Fatal("unexpected TID precision")
	}
	n, known := c.Nullable()
	if pg || !declared {
		if n || known {
			t.Fatal("unexpected nullability", n, known)
		}
	} else if !n || !known {
		t.Fatal("missing declared nullability", n, known)
	}
}

func TestReferenceTIDValues(t *testing.T) {
	tidScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		vectors := [][2]string{
			{"(0,0)", "(0,0)"}, {"(0,1)", "(0,1)"}, {"(4294967295,65535)", "(4294967295,65535)"},
			{"(-1,0)", "(4294967295,0)"}, {"(-2147483648,0)", "(2147483648,0)"}, {"(+0001, +0002)", "(1,2)"},
			{" (1,2)junk", "(1,2)"}, {"junk(1,2)", "(1,2)"}, {",1,2)", "(1,2)"}, {"1,2,3)", "(2,3)"},
			{"(,1)", "(0,1)"}, {"(1,)", "(1,0)"}, {"(,)", "(0,0)"},
			{"(18446744073709551615,1)", "(4294967295,1)"}, {"(-18446744073709551615,-18446744073709551615)", "(1,1)"},
		}
		for _, ws := range []string{" ", "\t", "\n", "\v", "\f", "\r"} {
			vectors = append(vectors, [2]string{"(" + ws + "1," + ws + "2)", "(1,2)"})
		}
		for _, v := range vectors {
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"SELECT " + tidQuote(v[0]) + "::tid AS v", "SELECT TID " + tidQuote(v[0]) + " AS v", "SELECT pg_catalog.tid " + tidQuote(v[0]) + " AS v", "SELECT CAST(" + tidQuote(v[0]) + " AS pg_catalog.tid) AS v"} {
					s := fixture.Select(q, []string{"v"}, []string{"TID"}, [][]any{{v[1]}})
					s.Prepared = prepared
					run(s)
				}
				for _, arg := range []any{v[0], []byte(v[0]), tidValuer{v[0]}} {
					s := fixture.Select("SELECT $1::tid AS v", []string{"v"}, []string{"TID"}, [][]any{{v[1]}}, arg)
					s.Prepared = prepared
					run(s)
				}
			}
		}
		run(fixture.Exec("CREATE TABLE tid_values(id integer,v tid)", 0))
		run(fixture.Exec("INSERT INTO tid_values VALUES(1,'(1,2)'),(2,'(-1,0)'),(3,NULL)", 3))
		for _, q := range []string{"SELECT v FROM tid_values ORDER BY id", "SELECT v FROM tid_values WHERE id=0", "SELECT v FROM tid_values WHERE id=3", "SELECT NULL::tid AS v"} {
			var want [][]any
			switch q {
			case "SELECT v FROM tid_values ORDER BY id":
				want = [][]any{{"(1,2)"}, {"(4294967295,0)"}, {nil}}
			case "SELECT v FROM tid_values WHERE id=3", "SELECT NULL::tid AS v":
				want = [][]any{{nil}}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"v"}, []string{"TID"}, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) { tidMetadata(t, r, b.PG, q != "SELECT NULL::tid AS v") }
				s.CheckScan = func(t *testing.T, r *sql.Rows, index int) {
					var raw any
					if err := r.Scan(&raw); err != nil {
						t.Fatal(err)
					}
					if want[index][0] == nil {
						if raw != nil {
							t.Fatal(raw)
						}
						return
					}
					if b.PG {
						if v, ok := raw.([]byte); !ok || string(v) != want[index][0] {
							t.Fatalf("raw TID %T %v", raw, raw)
						}
					} else if v, ok := raw.(string); !ok || v != want[index][0] {
						t.Fatalf("raw TID %T %v", raw, raw)
					}
				}
				run(s)
			}
		}
		q := "SELECT $1::tid AS v"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, frame := range []struct{ arg, want any }{{"(1,2)", "(1,2)"}, {[]byte("(-1,0)"), "(4294967295,0)"}, {nil, nil}, {[]byte(nil), nil}} {
			s := fixture.Select(q, []string{"v"}, []string{"TID"}, [][]any{{frame.want}}, frame.arg)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		d := tidDiagnostic("bad")
		d.Context = "unnamed portal parameter $1 = '...'"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, "bad"))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TID"}, [][]any{{"(2,3)"}}, "(2,3)"))
		source := []byte("(1,2)")
		retained, err := stmt.QueryContext(p.Ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		source[1] = '9'
		tidMetadata(t, retained, b.PG, false)
		if !retained.Next() {
			t.Fatal("missing retained value", retained.Err())
		}
		var text string
		if err := retained.Scan(&text); err != nil || text != "(1,2)" {
			t.Fatal("mutable input changed retained TID", text, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("unexpected retained rows", retained.Err())
		}
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		plain := fixture.Select("SELECT '(1,2)'::tid::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"(1,2)"}})
		plain.NonContext = true
		run(plain)
	})
}

func TestReferenceTIDComparison(t *testing.T) {
	tidScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE tid_order(id integer,v tid)", 0))
		run(fixture.Exec("INSERT INTO tid_order VALUES(1,'(1,2)'),(2,'(1,10)'),(3,'(2,0)'),(4,'(10,0)'),(5,'(4294967295,0)'),(6,NULL),(7,'(1,2)')", 7))
		for _, prepared := range []bool{false, true} {
			for _, s := range []fixture.Step{
				fixture.Select("SELECT id,v FROM tid_order ORDER BY v ASC NULLS LAST,id", []string{"id", "v"}, []string{"INT4", "TID"}, [][]any{{int64(1), "(1,2)"}, {int64(7), "(1,2)"}, {int64(2), "(1,10)"}, {int64(3), "(2,0)"}, {int64(4), "(10,0)"}, {int64(5), "(4294967295,0)"}, {int64(6), nil}}),
				fixture.Select("SELECT id FROM tid_order ORDER BY v DESC NULLS FIRST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}, {int64(5)}, {int64(4)}, {int64(3)}, {int64(2)}, {int64(1)}, {int64(7)}}),
				fixture.Select("SELECT DISTINCT v FROM tid_order ORDER BY v", []string{"v"}, []string{"TID"}, [][]any{{"(1,2)"}, {"(1,10)"}, {"(2,0)"}, {"(10,0)"}, {"(4294967295,0)"}, {nil}}),
				fixture.Select("SELECT v,count(*) AS n FROM tid_order GROUP BY v ORDER BY v", []string{"v", "n"}, []string{"TID", "INT8"}, [][]any{{"(1,2)", int64(2)}, {"(1,10)", int64(1)}, {"(2,0)", int64(1)}, {"(10,0)", int64(1)}, {"(4294967295,0)", int64(1)}, {nil, int64(1)}}),
				fixture.Select("SELECT id FROM tid_order WHERE v=$1 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(5)}}, "(-1,0)"),
				fixture.Select("SELECT a.id,b.id FROM tid_order a JOIN tid_order b ON a.v=b.v WHERE a.id=1 ORDER BY b.id", []string{"id", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(1), int64(7)}}),
				fixture.Select("SELECT id FROM tid_order WHERE v IN ('(1,2)'::tid,'(2,0)'::tid) ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(3)}, {int64(7)}}),
			} {
				s.Prepared = prepared
				run(s)
			}
			for _, pair := range []struct {
				left, right string
				want        []any
			}{{"(1,2)", "(1,2)", []any{true, false, false, true, false, true}}, {"(1,2)", "(1,10)", []any{false, true, true, true, false, false}}, {"(2,65535)", "(10,0)", []any{false, true, true, true, false, false}}, {"(2147483647,65535)", "(2147483648,0)", []any{false, true, true, true, false, false}}} {
				for i, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
					s := fixture.Select("SELECT "+tidQuote(pair.left)+"::tid "+op+" "+tidQuote(pair.right)+"::tid AS v", []string{"v"}, []string{"BOOL"}, [][]any{{pair.want[i]}})
					s.Prepared = prepared
					run(s)
					s = fixture.Select("SELECT NULL::tid "+op+" '(1,2)'::tid AS v", []string{"v"}, []string{"BOOL"}, [][]any{{nil}})
					s.Prepared = prepared
					run(s)
				}
			}
		}
		for _, method := range []string{"btree", "hash"} {
			run(fixture.Exec("CREATE INDEX tid_"+method+" ON tid_order USING "+method+"(v)", 0))
			run(fixture.Select("SELECT id FROM tid_order WHERE v='(1,2)' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(7)}}))
			run(fixture.Select("SELECT id FROM tid_order WHERE v>'(2,0)' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}, {int64(5)}}))
		}
	})
}
