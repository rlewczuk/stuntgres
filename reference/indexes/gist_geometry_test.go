package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceGiSTGeometryPredicates(t *testing.T) {
	for _, tc := range []struct{ typ, a, b string }{
		{"box", "(2,2),(0,0)", "(12,12),(10,10)"},
		{"polygon", "((0,0),(2,0),(2,2),(0,2))", "((10,10),(12,10),(12,12),(10,12))"},
		{"circle", "<(1,1),1>", "<(11,11),1>"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE gist_geometry(id integer,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO gist_geometry VALUES(1,'"+tc.a+"'),(2,'"+tc.b+"'),(3,NULL)", 3))
				check := func() {
					for _, op := range []struct {
						name          string
						first, second bool
					}{
						{"<<", true, false}, {"&<", true, true}, {"&&", false, true}, {"&>", false, true}, {">>", false, false}, {"~=", false, true},
						{"@>", false, true}, {"<@", false, true}, {"&<|", true, true}, {"<<|", true, false}, {"|>>", false, false}, {"|&>", false, true},
					} {
						classRoutes(t, p, b, conn, fixture.Select("SELECT id,v "+op.name+" $1::"+tc.typ+" AS hit FROM gist_geometry ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), op.first}, {int64(2), op.second}, {int64(3), nil}}, tc.b))
					}
				}
				check()
				run(fixture.Exec("CREATE INDEX gist_geometry_i ON gist_geometry USING gist(v)", 0))
				check()
				run(fixture.Exec("INSERT INTO gist_geometry SELECT 4,v FROM gist_geometry WHERE id=1", 1))
				run(fixture.Exec("UPDATE gist_geometry SET v=NULL WHERE id=2", 1))
				run(fixture.Exec("DELETE FROM gist_geometry WHERE id=1", 1))
				classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_geometry WHERE v << $1::"+tc.typ+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}, tc.b))
			})
		})
	}
	t.Run("point_and_exact_rechecks", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			run(fixture.Exec("CREATE TABLE gist_exact(id integer,p point,g polygon,c circle,b box)", 0))
			run(fixture.Exec("INSERT INTO gist_exact VALUES(1,'(0,0)','((0,0),(4,0),(0,4))','<(0,0),1>','(2,2),(0,0)'),(2,'(10,10)','((4,4),(4,3),(3,4))','<(1.9,1.9),1>','(12,12),(10,10)'),(3,NULL,NULL,NULL,NULL)", 3))
			check := func() {
				for _, op := range []struct {
					name string
					a, z bool
				}{{"<<", true, false}, {">>", false, false}, {"~=", false, true}, {"<<|", true, false}, {"|>>", false, false}, {"<^", true, false}, {">^", false, false}} {
					classRoutes(t, p, b, conn, fixture.Select("SELECT id,p "+op.name+" point '(10,10)' AS hit FROM gist_exact ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), op.a}, {int64(2), op.z}, {int64(3), nil}}))
				}
				classRoutes(t, p, b, conn, fixture.Select("SELECT id,p <@ box '(2,2),(0,0)' AS b,p <@ polygon '((0,0),(4,0),(0,4))' AS g,p <@ circle '<(0,0),1>' AS c FROM gist_exact ORDER BY id", []string{"id", "b", "g", "c"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, true}, {int64(2), false, false, false}, {int64(3), nil, nil, nil}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT g && polygon '((4,4),(4,3),(3,4))' AS g,c && circle '<(1.9,1.9),1>' AS c,point '(3,3)' <@ g AS outside,point '(1,1)' <@ g AS inside FROM gist_exact WHERE id=1", []string{"g", "c", "outside", "inside"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, false, false, true}}))
			}
			check()
			run(fixture.Exec("CREATE INDEX gist_exact_i ON gist_exact USING gist(p,g,c,b)", 0))
			check()
		})
	})
}

func TestReferenceGiSTNearestNeighbor(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE gist_near(id integer,p point)", 0))
		run(fixture.Exec("INSERT INTO gist_near VALUES(1,'(0,0)'),(2,'(3,4)'),(3,'(0,5)'),(4,NULL)", 4))
		const q = "SELECT id,p <-> point '(0,0)' AS d FROM gist_near ORDER BY p <-> point '(0,0)' NULLS LAST,id"
		held := classHeld(t, p, conn, q)
		check := func(want [][]any) {
			s := fixture.Select(q, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, want)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		before := [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}, {int64(3), float64(5)}, {int64(4), nil}}
		check(before)
		run(fixture.Exec("CREATE INDEX gist_near_i ON gist_near USING gist(p) INCLUDE(id)", 0))
		check(before)
		classRoutes(t, p, b, conn, fixture.Select(q+" LIMIT 3", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, before[:3]))
		run(fixture.Exec("INSERT INTO gist_near VALUES(5,'(1,0)')", 1))
		run(fixture.Exec("UPDATE gist_near SET p='(6,8)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM gist_near WHERE id=3", 1))
		check([][]any{{int64(1), float64(0)}, {int64(5), float64(1)}, {int64(2), float64(10)}, {int64(4), nil}})
		// This scalar point/box distance is not point_ops ordering membership.
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM gist_near ORDER BY p <-> box '(0,0),(0,0)' NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}, {int64(2)}, {int64(4)}}))
	})
	for _, tc := range []struct {
		typ, value string
		distance   float64
	}{{"box", "(2,2),(0,0)", 2}, {"polygon", "((0,0),(2,0),(2,2),(0,2))", 2}, {"circle", "<(0,0),1>", 3}} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE gist_shape(id integer,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO gist_shape VALUES(1,'"+tc.value+"'),(2,'"+tc.value+"'),(3,NULL)", 3))
				q := "SELECT id,v <-> point '(4,0)' AS d FROM gist_shape ORDER BY v <-> point '(4,0)' NULLS LAST,id"
				step := fixture.Select(q, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), tc.distance}, {int64(2), tc.distance}, {int64(3), nil}})
				classRoutes(t, p, b, conn, step)
				run(fixture.Exec("CREATE INDEX gist_shape_i ON gist_shape USING gist(v)", 0))
				classRoutes(t, p, b, conn, step)
				classRoutes(t, p, b, conn, fixture.Select("SELECT id,v <-> point '(0,0)' AS d FROM gist_shape ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(0)}, {int64(3), nil}}))
			})
		})
	}
}
