package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmallintArithmeticValues(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,s SMALLINT,h SMALLINT,i INT,b BIGINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,7,2,2,2),(2,-7,-2,-2,-2),(3,NULL,NULL,NULL,NULL)", 3))
			check := func(expr string, types []string, rows [][]any) {
				s := fixture.Select("SELECT "+expr+" FROM t ORDER BY id", strings.Split(strings.Repeat("?column?,", len(types)-1)+"?column?", ","), types, rows)
				for _, prepared := range []bool{false, true} {
					if prepared {
						run(fixture.Prepared(s))
					} else {
						run(s)
					}
				}
			}
			check("s+h,s-h,s*h,s/h,s%h,+s,-s", []string{"INT2", "INT2", "INT2", "INT2", "INT2", "INT2", "INT2"}, [][]any{{int64(9), int64(5), int64(14), int64(3), int64(1), int64(7), int64(-7)}, {int64(-9), int64(-5), int64(14), int64(3), int64(-1), int64(-7), int64(7)}, {nil, nil, nil, nil, nil, nil, nil}})
			for _, peer := range []struct{ name, typ string }{{"i", "INT4"}, {"b", "INT8"}} {
				for j, op := range []string{"+", "-", "*", "/", "%"} {
					forward := []int64{9, 5, 14, 3, 1}
					reverse := []int64{9, -5, 14, 0, 2}
					negativeForward := []int64{-9, -5, 14, 3, -1}
					negativeReverse := []int64{-9, 5, 14, 0, -2}
					check("s"+op+peer.name+","+peer.name+op+"s", []string{peer.typ, peer.typ}, [][]any{{forward[j], reverse[j]}, {negativeForward[j], negativeReverse[j]}, {nil, nil}})
				}
			}
			check("s+2,s/2,s%2", []string{"INT4", "INT4", "INT4"}, [][]any{{int64(9), int64(3), int64(1)}, {int64(-5), int64(-3), int64(-1)}, {nil, nil, nil}})
			check("s+2147483648", []string{"INT8"}, [][]any{{int64(2147483655)}, {int64(2147483641)}, {nil}})
			check("s+'2',s%'2'", []string{"INT2", "INT2"}, [][]any{{int64(9), int64(1)}, {int64(-5), int64(-1)}, {nil, nil}})
			run(fixture.Exec("INSERT INTO t VALUES(4,NULL,0,0,0)", 1))
			for _, where := range []string{"id=4", "FALSE"} {
				var rows [][]any
				if where == "id=4" {
					rows = [][]any{{nil, nil, nil, nil, nil, nil}}
				}
				s := fixture.Select("SELECT s/h,s%h,s/i,s%i,s/b,s%b FROM t WHERE "+where, []string{"?column?", "?column?", "?column?", "?column?", "?column?", "?column?"}, []string{"INT2", "INT2", "INT4", "INT4", "INT8", "INT8"}, rows)
				for _, prepared := range []bool{false, true} {
					var executor fixture.Executor = backend.DB
					if prepared {
						stmt, err := backend.DB.PrepareContext(p.Ctx, s.Query)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						executor = fixture.HeldStatement{backend.DB, stmt, s.Query}
					}
					fixture.RunStep(t, p.Ctx, referenceArithmeticMetadataExecutor{executor, t, backend.PG, s.Types}, backend.Recorder, s)
				}
			}
			s := fixture.Select("SELECT (s+h)*h,s+h*h,- -s,s% -1,s%(-1),(s+1)::bigint,s::bigint+1 FROM t WHERE id=1", []string{"?column?", "?column?", "?column?", "?column?", "?column?", "int8", "?column?"}, []string{"INT2", "INT2", "INT2", "INT4", "INT4", "INT8", "INT8"}, [][]any{{int64(18), int64(11), int64(7), int64(0), int64(0), int64(8), int64(8)}})
			run(s)
			run(fixture.Prepared(s))
		})
	}
}

func TestReferenceSmallintArithmeticSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(s SMALLINT,h SMALLINT,i INT,b BIGINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(7,2,2,2)", 1))
			s := fixture.Select("SELECT s+h,s/h,s%h,s+i,i-s,s*b,b/s,s%i,s%b,+s,-s FROM t", strings.Split(strings.Repeat("?column?,", 10)+"?column?", ","), []string{"INT2", "INT2", "INT2", "INT4", "INT4", "INT8", "INT8", "INT4", "INT8", "INT2", "INT2"}, [][]any{{int64(9), int64(3), int64(1), int64(9), int64(-5), int64(14), int64(0), int64(1), int64(1), int64(7), int64(-7)}})
			run(s)
			stmt, err := backend.DB.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{backend.DB, stmt, s.Query}, backend.Recorder, s)
		})
	}
	t.Run("held_parameter_frames", TestReferenceSmallintArithmeticParameters)
	t.Run("checked_widths", TestReferenceSmallintArithmeticWidths)
	t.Run("publication", TestReferenceSmallintArithmeticAtomicity)
	t.Run("persistent_controls", TestReferenceSmallintArithmeticControls)
}

func TestReferenceSmallintArithmeticMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,s SMALLINT,i INT,b BIGINT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,7,2,2),(2,NULL,NULL,NULL)", 2))
	for _, tc := range []struct {
		where string
		rows  [][]any
	}{{"id=1", [][]any{{int64(14), int64(9), int64(1)}}}, {"id=2", [][]any{{nil, nil, nil}}}, {"FALSE", nil}} {
		referenceArithmeticMetadata(t, p, fixture.Select("SELECT s+s,s+i,s%b FROM t WHERE "+tc.where, []string{"?column?", "?column?", "?column?"}, []string{"INT2", "INT4", "INT8"}, tc.rows))
	}
}
