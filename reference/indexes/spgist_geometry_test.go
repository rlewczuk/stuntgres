package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTGeometryPredicates(t *testing.T) {
	for _, tc := range []struct{ typ, a, b string }{
		{"box", "(2,2),(0,0)", "(12,12),(10,10)"},
		{"polygon", "((0,0),(2,0),(2,2),(0,2))", "((10,10),(12,10),(12,12),(10,12))"},
	} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE spgist_geometry(id integer,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO spgist_geometry VALUES(1,'"+tc.a+"'),(2,'"+tc.b+"'),(3,NULL)", 3))
				check := func(changed bool) {
					for _, op := range []struct {
						name          string
						first, second bool
					}{
						{"<<", true, false}, {"&<", true, true}, {"&&", false, true}, {"&>", false, true}, {">>", false, false}, {"~=", false, true},
						{"@>", false, true}, {"<@", false, true}, {"&<|", true, true}, {"<<|", true, false}, {"|>>", false, false}, {"|&>", false, true},
					} {
						members := [][]any{{int64(1), op.first}, {int64(2), op.second}, {int64(3), nil}}
						if changed {
							members = [][]any{{int64(2), nil}, {int64(3), nil}, {int64(4), op.first}}
						}
						classRoutes(t, p, b, conn, fixture.Select("SELECT id,v "+op.name+" $1::"+tc.typ+" AS hit FROM spgist_geometry ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, members, tc.b))
					}
				}
				check(false)
				run(fixture.Exec("CREATE INDEX spgist_geometry_i ON spgist_geometry USING spgist(v)", 0))
				check(false)
				run(fixture.Exec("INSERT INTO spgist_geometry SELECT 4,v FROM spgist_geometry WHERE id=1", 1))
				run(fixture.Exec("UPDATE spgist_geometry SET v=NULL WHERE id=2", 1))
				run(fixture.Exec("DELETE FROM spgist_geometry WHERE id=1", 1))
				check(true)
				classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_geometry WHERE v << $1::"+tc.typ+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(4)}}, tc.b))
			})
		})
	}
	t.Run("point_and_exact_rechecks", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			run(fixture.Exec("CREATE TABLE spgist_exact(id integer,p point,g polygon,b box)", 0))
			run(fixture.Exec("INSERT INTO spgist_exact VALUES(1,'(0,0)','((0,0),(4,0),(0,4))','(2,2),(0,0)'),(2,'(10,10)','((4,4),(4,3),(3,4))','(12,12),(10,10)'),(3,NULL,NULL,NULL)", 3))
			check := func() {
				for _, op := range []struct {
					name string
					a, z bool
				}{{"<<", true, false}, {">>", false, false}, {"~=", false, true}, {"<<|", true, false}, {"|>>", false, false}, {"<^", true, false}, {">^", false, false}} {
					classRoutes(t, p, b, conn, fixture.Select("SELECT id,p "+op.name+" point '(10,10)' AS hit FROM spgist_exact ORDER BY id", []string{"id", "hit"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), op.a}, {int64(2), op.z}, {int64(3), nil}}))
				}
				classRoutes(t, p, b, conn, fixture.Select("SELECT id,p <@ box '(2,2),(0,0)' AS b,p <@ polygon '((0,0),(4,0),(0,4))' AS g FROM spgist_exact ORDER BY id", []string{"id", "b", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), false, false}, {int64(3), nil, nil}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT g && polygon '((4,4),(4,3),(3,4))' AS g,point '(3,3)' <@ g AS outside,point '(1,1)' <@ g AS inside FROM spgist_exact WHERE id=1", []string{"g", "outside", "inside"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}))
				classRoutes(t, p, b, conn, fixture.Select("SELECT p ~= point '(0.000001,0.000001)' AS epsilon,p ~= point '(0.000002,0.000002)' AS beyond,p << point '(0.000001,0)' AS fuzzy_left,point '(2,2)' <@ b AS boundary FROM spgist_exact WHERE id=1", []string{"epsilon", "beyond", "fuzzy_left", "boundary"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false, true}}))
			}
			check()
			for _, key := range []string{"p quad_point_ops", "p kd_point_ops", "g", "b"} {
				run(fixture.Exec("CREATE INDEX ON spgist_exact USING spgist("+key+")", 0))
			}
			check()
			run(fixture.Exec("INSERT INTO spgist_exact(id,p) VALUES(4,'(0,0)')", 1))
			run(fixture.Exec("UPDATE spgist_exact SET p='(0,0)' WHERE id=2", 1))
			run(fixture.Exec("DELETE FROM spgist_exact WHERE id=4", 1))
			run(fixture.Exec("UPDATE spgist_exact SET p='(10,10)' WHERE id=2", 1))
			check()
		})
	})
}

func TestReferenceSPGiSTNearestNeighbor(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE spgist_near(id integer,p point)", 0))
		run(fixture.Exec("INSERT INTO spgist_near VALUES(1,'(0,0)'),(2,'(3,4)'),(3,'(0,5)'),(4,NULL)", 4))
		const q = "SELECT id,p <-> point '(0,0)' AS d FROM spgist_near ORDER BY p <-> point '(0,0)' NULLS LAST,id"
		held := classHeld(t, p, conn, q)
		check := func(want [][]any) {
			s := fixture.Select(q, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, want)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		before := [][]any{{int64(1), float64(0)}, {int64(2), float64(5)}, {int64(3), float64(5)}, {int64(4), nil}}
		check(before)
		run(fixture.Exec("CREATE INDEX spgist_near_i ON spgist_near USING spgist(p) INCLUDE(id)", 0))
		run(fixture.Exec("CREATE INDEX spgist_near_kd ON spgist_near USING spgist(p kd_point_ops)", 0))
		check(before)
		classRoutes(t, p, b, conn, fixture.Select(q+" LIMIT 3", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, before[:3]))
		run(fixture.Exec("INSERT INTO spgist_near VALUES(5,'(1,0)')", 1))
		run(fixture.Exec("UPDATE spgist_near SET p='(6,8)' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM spgist_near WHERE id=3", 1))
		check([][]any{{int64(1), float64(0)}, {int64(5), float64(1)}, {int64(2), float64(10)}, {int64(4), nil}})
		// This scalar point/box distance is not point_ops ordering membership.
		classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_near ORDER BY p <-> box '(0,0),(0,0)' NULLS LAST,id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}, {int64(2)}, {int64(4)}}))
	})
	for _, tc := range []struct {
		typ, value string
		distance   float64
	}{{"box", "(2,2),(0,0)", 2}, {"polygon", "((0,0),(2,0),(2,2),(0,2))", 2}} {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				run(fixture.Exec("CREATE TABLE spgist_shape(id integer,v "+tc.typ+")", 0))
				run(fixture.Exec("INSERT INTO spgist_shape VALUES(1,'"+tc.value+"'),(2,'"+tc.value+"'),(3,NULL)", 3))
				q := "SELECT id,v <-> point '(4,0)' AS d FROM spgist_shape ORDER BY v <-> point '(4,0)' NULLS LAST,id"
				step := fixture.Select(q, []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), tc.distance}, {int64(2), tc.distance}, {int64(3), nil}})
				classRoutes(t, p, b, conn, step)
				run(fixture.Exec("CREATE INDEX spgist_shape_i ON spgist_shape USING spgist(v)", 0))
				classRoutes(t, p, b, conn, step)
				classRoutes(t, p, b, conn, fixture.Select("SELECT id,v <-> point '(0,0)' AS d FROM spgist_shape ORDER BY id", []string{"id", "d"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(1), float64(0)}, {int64(2), float64(0)}, {int64(3), nil}}))
			})
		})
	}
}
