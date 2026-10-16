package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func mergeReturningRoutes(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, bool), beforeScenario ...func(*testing.T)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			for _, setup := range beforeScenario {
				setup(t)
			}
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) { body(t, p, b, prepared) })
			}
		})
	}
}
func mergeReturningRun(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

const mergeReturningMixed = "MERGE INTO t USING s ON t.id=s.sid WHEN MATCHED AND s.sid=1 THEN DELETE WHEN MATCHED AND s.delta>0 THEN UPDATE SET v=t.v+s.delta WHEN NOT MATCHED BY SOURCE THEN DELETE WHEN NOT MATCHED BY TARGET AND s.delta>0 THEN INSERT VALUES(s.sid,s.delta)"

func mergeReturningSeed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE t(id INT,v INT)", 0), fixture.Exec("CREATE TABLE s(sid INT,delta INT)", 0), fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(5,50)", 4), fixture.Exec("INSERT INTO s VALUES(1,7),(2,8),(4,9),(5,0),(6,0)", 5)} {
		mergeReturningRun(t, p, b, s)
	}
}
func mergeReturningCoreStep(prepared bool) fixture.Step {
	return insertReturningBag(mergeReturningMixed+" RETURNING merge_action(),s.sid,s.delta,t.id,t.v,old.id,old.v,new.id,new.v", []string{"merge_action", "sid", "delta", "id", "v", "id", "v", "id", "v"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"DELETE", int64(1), int64(7), int64(1), int64(10), int64(1), int64(10), nil, nil}, {"UPDATE", int64(2), int64(8), int64(2), int64(28), int64(2), int64(20), int64(2), int64(28)}, {"INSERT", int64(4), int64(9), int64(4), int64(9), nil, nil, int64(4), int64(9)}, {"DELETE", nil, nil, int64(3), int64(30), int64(3), int64(30), nil, nil}}, prepared)
}
func mergeReturningSurvivors() fixture.Step {
	return fixture.Select("SELECT * FROM t ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(28)}, {int64(4), int64(9)}, {int64(5), int64(50)}})
}
func TestReferenceMergeReturningCore(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		mergeReturningSeed(t, p, b)
		run(mergeReturningCoreStep(prepared))
		run(mergeReturningSurvivors())
		run(fixture.Exec("DELETE FROM t", 3))
		run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20),(3,30),(5,50)", 4))
		n := int64(4)
		if b.PG {
			n = 0
		}
		s := fixture.Exec(mergeReturningCoreStep(prepared).Query, n)
		s.Prepared = prepared
		run(s)
		run(mergeReturningSurvivors())
		for _, pred := range []string{"FALSE", "NULL", "TRUE"} {
			rows := [][]any{}
			if pred == "TRUE" {
				rows = [][]any{{"UPDATE", int64(28), int64(28)}}
			}
			run(insertReturningBag("MERGE INTO t USING (VALUES(2)) x(id) ON t.id=x.id WHEN MATCHED AND "+pred+" THEN UPDATE SET v=t.v RETURNING merge_action(),old.v,new.v", []string{"merge_action", "v", "v"}, []string{"TEXT", "INT4", "INT4"}, rows, prepared))
			count := int64(0)
			if pred == "TRUE" && !b.PG {
				count = 1
			}
			exec := fixture.Exec("MERGE INTO t USING (VALUES(2)) x(id) ON t.id=x.id WHEN MATCHED AND "+pred+" THEN UPDATE SET v=t.v RETURNING merge_action()", count)
			exec.Prepared = prepared
			run(exec)
		}
		run(insertReturningBag("MERGE INTO t USING (SELECT 1 AS id WHERE FALSE) x ON t.id=x.id WHEN MATCHED THEN DELETE RETURNING merge_action(),old.id,new.id", []string{"merge_action", "id", "id"}, []string{"TEXT", "INT4", "INT4"}, [][]any{}, prepared))
	})
}
func TestReferenceMergeReturningStars(t *testing.T) {
	for _, tc := range []struct {
		out   string
		names []string
		rows  [][]any
	}{
		{"*", []string{"sid", "delta", "id", "v"}, [][]any{{int64(1), int64(7), int64(1), int64(10)}, {int64(2), int64(8), int64(2), int64(28)}, {int64(4), int64(9), int64(4), int64(9)}, {nil, nil, int64(3), int64(30)}}},
		{"s.*,t.*,old.*,new.*", []string{"sid", "delta", "id", "v", "id", "v", "id", "v"}, [][]any{{int64(1), int64(7), int64(1), int64(10), int64(1), int64(10), nil, nil}, {int64(2), int64(8), int64(2), int64(28), int64(2), int64(20), int64(2), int64(28)}, {int64(4), int64(9), int64(4), int64(9), nil, nil, int64(4), int64(9)}, {nil, nil, int64(3), int64(30), int64(3), int64(30), nil, nil}}},
	} {
		t.Run(tc.out, func(t *testing.T) {
			mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				mergeReturningSeed(t, p, b)
				types := make([]string, len(tc.names))
				for i := range types {
					types[i] = "INT4"
				}
				mergeReturningRun(t, p, b, insertReturningBag(mergeReturningMixed+" RETURNING "+tc.out, tc.names, types, tc.rows, prepared))
			})
		})
	}
}
func TestReferenceMergeReturningAliases(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) { mergeReturningRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		for _, aliases := range []string{"OLD AS new,NEW AS old", "NEW AS old,OLD AS new"} {
			run(insertReturningBag("MERGE INTO t AS target USING (VALUES(1)) source(id) ON target.id=source.id WHEN MATCHED THEN UPDATE SET v=target.v+1 RETURNING WITH("+aliases+") new.v,old.v,target.v,source.id", []string{"v", "v", "v", "id"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(10), int64(11), int64(11), int64(1)}}, prepared))
			run(fixture.Exec("UPDATE t SET v=10", 1))
		}
		run(insertReturningBag("MERGE INTO t USING (VALUES(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET v=11 RETURNING WITH(OLD AS new) new.v,t.v", []string{"v", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(10), int64(11)}}, prepared))
		run(insertReturningBag("MERGE INTO t AS old USING (VALUES(1)) new(id) ON old.id=new.id WHEN MATCHED THEN UPDATE SET v=12 RETURNING old.v,new.id", []string{"v", "id"}, []string{"INT4", "INT4"}, [][]any{{int64(12), int64(1)}}, prepared))
	})
}

// Reuse the established composite codec contract while leaving scalar columns
// to the fixture's ordinary raw-type assertions.
func mergeReturningComposite(t *testing.T, b fixture.Backend, s fixture.Step) fixture.Step {
	composite := insertReturningComposite(t, b, s)
	s.CheckRaw = func(t *testing.T, column, row int, v any) bool {
		if s.Types[column] != "" {
			return false
		}
		return composite.CheckRaw(t, column, row, v)
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		meta, e := rows.ColumnTypes()
		if e != nil {
			t.Fatal(e)
		}
		want := reflect.TypeOf("")
		if b.PG {
			want = reflect.TypeOf((*any)(nil)).Elem()
		}
		for i, m := range meta {
			if s.Types[i] == "" && (m.DatabaseTypeName() != "" || m.ScanType() != want) {
				t.Fatalf("composite %s %q %v want blank/%v", m.Name(), m.DatabaseTypeName(), m.ScanType(), want)
			}
		}
	}
	return s
}
func TestReferenceMergeReturningWholeRows(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := func(s fixture.Step) {
			if s.SelectRows {
				for _, typ := range s.Types {
					if typ == "" {
						s = mergeReturningComposite(t, b, s)
						break
					}
				}
			}
			mergeReturningRun(t, p, b, s)
		}
		mergeReturningSeed(t, p, b)
		run(insertReturningBag(mergeReturningMixed+" RETURNING merge_action(),t,old,new,s", []string{"merge_action", "t", "old", "new", "s"}, []string{"TEXT", "", "", "", ""}, [][]any{{"DELETE", "(1,10)", "(1,10)", nil, "(1,7)"}, {"UPDATE", "(2,28)", "(2,20)", "(2,28)", "(2,8)"}, {"INSERT", "(4,9)", nil, "(4,9)", "(4,9)"}, {"DELETE", "(3,30)", "(3,30)", nil, nil}}, prepared))
		run(fixture.Exec("DELETE FROM s", 5))
		run(insertReturningBag("MERGE INTO t USING s ON t.id=s.sid WHEN NOT MATCHED BY SOURCE THEN UPDATE SET v=t.v+1 RETURNING merge_action(),s.sid,t.id,old.v,new.v,s", []string{"merge_action", "sid", "id", "v", "v", "s"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4", ""}, [][]any{{"UPDATE", nil, int64(2), int64(28), int64(29), nil}, {"UPDATE", nil, int64(4), int64(9), int64(10), nil}, {"UPDATE", nil, int64(5), int64(50), int64(51), nil}}, prepared))
		run(fixture.Exec("CREATE TABLE nullable(a INT,b TEXT)", 0))
		run(fixture.Exec("INSERT INTO nullable VALUES(NULL,NULL)", 1))
		run(insertReturningBag("MERGE INTO nullable USING (VALUES(1)) s(x) ON TRUE WHEN MATCHED THEN DELETE RETURNING old,new,nullable", []string{"old", "new", "nullable"}, []string{"", "", ""}, [][]any{{"(,)", nil, "(,)"}}, prepared))
	})
}
func TestReferenceMergeReturningExpressions(t *testing.T) {
	mergeReturningRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		mergeReturningSeed(t, p, b)
		mergeReturningRun(t, p, b, insertReturningBag(mergeReturningMixed+" RETURNING CASE merge_action() WHEN 'DELETE' THEN 'D' WHEN 'UPDATE' THEN 'U' ELSE 'I' END AS action,(SELECT merge_action()) AS nested,(SELECT new.v-old.v) AS difference,(SELECT s.delta) AS frozen,(SELECT x.v FROM (VALUES(99)) x(v)) AS shadow,(SELECT 1 WHERE merge_action()='UPDATE') AS update_marker", []string{"action", "nested", "difference", "frozen", "shadow", "update_marker"}, []string{"TEXT", "TEXT", "INT4", "INT4", "INT4", "INT4"}, [][]any{{"D", "DELETE", nil, int64(7), int64(99), nil}, {"U", "UPDATE", int64(8), int64(8), int64(99), int64(1)}, {"I", "INSERT", nil, int64(9), int64(99), nil}, {"D", "DELETE", nil, nil, int64(99), nil}}, prepared))
	})
}
