package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBtreeGINNetworkSearch(t *testing.T) {
	for _, tc := range networkCases {
		t.Run(tc.typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				compoundCleanup(t, p, b, conn)
				run(fixture.Exec("CREATE TABLE network_search(id integer NOT NULL CHECK(id>0),v "+tc.typ+")", 0))
				networkSeed(run, "network_search", tc)
				queries := make([]string, 6)
				held := make([]fixture.HeldStatement, 6)
				for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
					queries[i] = "SELECT id FROM network_search WHERE v " + op + " $1::" + tc.typ + " ORDER BY id"
					held[i] = classHeld(t, p, conn, queries[i])
				}
				check := func(want [][]int64) {
					t.Helper()
					for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
						classRoutes(t, p, b, conn, ginIDs(queries[i], want[i], tc.boundary))
						for _, arg := range []any{tc.boundary, []byte(tc.boundary), nil} {
							ids := want[i]
							if arg == nil {
								ids = nil
							}
							fixture.RunStep(t, p.Ctx, held[i], b.Recorder, ginIDs(queries[i], ids, arg))
						}
						classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_search WHERE v "+op+" "+networkLiteral(tc.boundary, tc.typ)+" ORDER BY id", want[i]))
						run(ginIDs("SELECT id FROM network_search WHERE v "+op+" NULL::"+tc.typ+" ORDER BY id", nil))
					}
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_search WHERE v IS NULL ORDER BY id", []int64{5}))
				}
				check(networkTruth)
				values := networkValues(tc)
				valueStep := networkValueStep(b, "SELECT id,v FROM network_search ORDER BY id", tc, values)
				classRoutes(t, p, b, conn, valueStep)
				classRoutes(t, p, b, conn, networkValueStep(b, "SELECT id,v FROM network_search WHERE false", tc, nil))
				run(fixture.Exec("CREATE EXTENSION btree_gin VERSION '1.3'", 0))
				run(fixture.Exec("CREATE INDEX network_search_i ON network_search USING gin(v public."+tc.typ+"_ops)", 0))
				check(networkTruth)
				// Default class resolution on a genuinely empty fixture, followed by writes.
				run(fixture.Exec("CREATE TABLE network_empty(id integer NOT NULL,v "+tc.typ+")", 0))
				run(fixture.Prepared(fixture.Exec("CREATE INDEX network_empty_i ON network_empty USING gin(v)", 0)))
				classRoutes(t, p, b, conn, networkValueStep(b, "SELECT id,v FROM network_empty ORDER BY id", tc, nil))
				networkSeed(run, "network_empty", tc)
				classRoutes(t, p, b, conn, networkValueStep(b, "SELECT id,v FROM network_empty ORDER BY id", tc, values))
				for i, op := range []string{"<", "<=", "=", ">=", ">", "<>"} {
					q := "SELECT id FROM network_empty WHERE v" + op + "$1::" + tc.typ + " ORDER BY id"
					classRoutes(t, p, b, conn, ginIDs(q, networkTruth[i], tc.boundary))
					held := classHeld(t, p, conn, q)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, networkTruth[i], tc.boundary))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, ginIDs(q, nil, nil))
				}
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_empty WHERE v IS NULL ORDER BY id", []int64{5}))
				for _, edge := range []struct{ op, value string }{{"<", tc.lower}, {">", tc.upper}, {"=", tc.absent}} {
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_search WHERE v"+edge.op+"$1::"+tc.typ+" ORDER BY id", nil, edge.value))
				}
				insertSQL := "INSERT INTO network_search VALUES($1,$2::" + tc.typ + ")"
				insert := classHeld(t, p, conn, insertSQL)
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 6, tc.boundary))
				check([][]int64{{1}, {1, 2, 3, 6}, {2, 3, 6}, {2, 3, 4, 6}, {4}, {1, 4}})
				updateSQL := "UPDATE network_search SET v=$1::" + tc.typ + " WHERE id=$2"
				update := classHeld(t, p, conn, updateSQL)
				fixture.RunStep(t, p.Ctx, update, b.Recorder, fixture.Exec(updateSQL, 1, tc.lower, 6))
				check([][]int64{{1, 6}, {1, 2, 3, 6}, {2, 3}, {2, 3, 4}, {4}, {1, 4, 6}})
				run(fixture.Prepared(fixture.Exec("DELETE FROM network_search WHERE id=$1", 1, 6)))
				check(networkTruth)
				// A later failed row must not leak an earlier valid tuple or its postings.
				bad := "INSERT INTO network_search VALUES(90,$1::" + tc.typ + "),(-1,$1::" + tc.typ + ")"
				d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "network_search" violates check constraint "network_search_id_check"`)
				d.Detail = "Failing row contains (-1, " + tc.boundary + ")."
				d.Schema = "public"
				d.Table = "network_search"
				d.Constraint = "network_search_id_check"
				s := fixture.ErrorDiagnostic(bad, d, tc.boundary)
				s.ErrorStage = "execute"
				classRoutes(t, p, b, conn, s)
				run(ginIDs("SELECT id FROM network_search WHERE id=90 ORDER BY id", nil))
				check(networkTruth)
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SAVEPOINT network_before", 0))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insertSQL, 1, 6, tc.boundary))
				run(fixture.Exec("ROLLBACK TO network_before", 0))
				check(networkTruth)
				run(fixture.Exec("COMMIT", 0))
				classRoutes(t, p, b, conn, valueStep)
			})
		})
	}
	t.Run("distinguishing_orders", func(t *testing.T) {
		classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			conn, run := classConn(t, p, b)
			compoundCleanup(t, p, b, conn)
			run(fixture.Exec("CREATE EXTENSION btree_gin", 0))
			for _, tc := range []struct{ typ, rows, boundary string }{
				{"inet", "(1,'10.0.0.0/16'),(2,'10.0.0.0/24'),(3,'10.0.0.1/24'),(4,'255.255.255.255/32'),(5,'::/128')", "10.0.0.1/24"},
				{"cidr", "(1,'10.0.0.0/16'),(2,'10.0.0.0/24'),(3,'10.0.1.0/24'),(4,'255.255.255.255/32'),(5,'::/128')", "10.0.1.0/24"},
				{"uuid", "(1,'00000001-0000-0000-0000-000000000000'),(2,'00000100-0000-0000-0000-000000000000')", "00000100-0000-0000-0000-000000000000"},
			} {
				table := "network_order_" + tc.typ
				run(fixture.Exec("CREATE TABLE "+table+"(id integer,v "+tc.typ+")", 0))
				n := int64(5)
				want := []int64{1, 2, 3, 4, 5}
				equal := []int64{3}
				if tc.typ == "uuid" {
					n = 2
					want = []int64{1, 2}
					equal = []int64{2}
				}
				run(fixture.Exec("INSERT INTO "+table+" VALUES"+tc.rows, n))
				check := func() {
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" ORDER BY v", want))
					classRoutes(t, p, b, conn, ginIDs("SELECT id FROM "+table+" WHERE v=$1::"+tc.typ+" ORDER BY id", equal, tc.boundary))
				}
				check()
				run(fixture.Exec("CREATE INDEX "+table+"_i ON "+table+" USING gin(v "+tc.typ+"_ops)", 0))
				check()
				if tc.typ == "cidr" {
					run(fixture.Exec("CREATE INDEX network_cidr_inet ON "+table+" USING gin(v inet_ops)", 0))
					check()
					classRoutes(t, p, b, conn, fixture.Select("SELECT v FROM "+table+" WHERE id=2", []string{"v"}, []string{"CIDR"}, [][]any{{"10.0.0.0/24"}}))
				}
			}
			run(fixture.Exec("CREATE TABLE network_oid_integer(id integer,v integer)", 0))
			run(fixture.Exec("INSERT INTO network_oid_integer VALUES(1,0),(2,2147483647),(3,-2147483648),(4,-1),(5,NULL)", 5))
			check := func() {
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_oid_integer WHERE v<0 ORDER BY id", []int64{3, 4}))
				classRoutes(t, p, b, conn, ginIDs("SELECT id FROM network_oid_integer WHERE v>=0 ORDER BY id", []int64{1, 2}))
			}
			check()
			run(fixture.Exec("CREATE INDEX network_oid_integer_i ON network_oid_integer USING gin(v oid_ops)", 0))
			check()
		})
	})
}
