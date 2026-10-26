package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTNetworkPredicates(t *testing.T) {
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		run(fixture.Exec("CREATE TABLE spgist_net(id integer,v inet)", 0))
		run(fixture.Exec("INSERT INTO spgist_net VALUES(1,'10.0.0.1/24'),(2,'10.0.0.1/16'),(3,'10.0.1.1/24'),(4,'2001:db8::1/64'),(5,NULL),(6,'10.0.0.1/24')", 6))
		checks := []struct {
			op  string
			ids []int64
		}{
			{"=", []int64{1, 6}}, {"<<", nil}, {"<<=", []int64{1, 6}}, {">>", []int64{2}}, {">>=", []int64{1, 2, 6}}, {"&&", []int64{1, 2, 6}},
			{"<>", []int64{2, 3, 4}}, {"<", []int64{2}}, {"<=", []int64{1, 2, 6}}, {">", []int64{3, 4}}, {">=", []int64{1, 3, 4, 6}},
		}
		check := func(changed bool) {
			for _, c := range checks {
				q := "SELECT id FROM spgist_net WHERE v " + c.op + " $1::inet ORDER BY id"
				if changed {
					switch c.op {
					case "=", "<<=", ">>=", "&&", "<=":
						c.ids = []int64{1, 7}
					case "<>", ">":
						c.ids = []int64{2, 3, 4}
					case ">=":
						c.ids = []int64{1, 2, 3, 4, 7}
					default:
						c.ids = nil
					}
				}
				var rows [][]any
				for _, id := range c.ids {
					rows = append(rows, []any{id})
				}
				step := fixture.Select(q, []string{"id"}, []string{"INT4"}, rows, "10.0.0.1/24")
				classRoutes(t, p, b, conn, step)
				classRoutes(t, p, b, conn, fixture.Select("SELECT v "+c.op+" inet '10.0.0.1/24' AS hit FROM spgist_net WHERE id=5", []string{"hit"}, []string{"BOOL"}, [][]any{{nil}}))
			}
		}
		q := "SELECT id FROM spgist_net WHERE v && $1::inet ORDER BY id"
		held := classHeld(t, p, conn, q)
		check(false)
		run(fixture.Exec("CREATE INDEX spgist_net_i ON spgist_net USING spgist(v)", 0))
		check(false)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(6)}}, "10.0.0.1/24"))
		run(fixture.Exec("INSERT INTO spgist_net SELECT 7,v FROM spgist_net WHERE id=1", 1))
		run(fixture.Exec("UPDATE spgist_net SET v='192.0.2.1/32' WHERE id=2", 1))
		run(fixture.Exec("DELETE FROM spgist_net WHERE id=6", 1))
		check(true)
		classRoutes(t, p, b, conn, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(7)}}, "10.0.0.1/24"))
		classRoutes(t, p, b, conn, fixture.Select("SELECT inet '0.0.0.0/0' >>= inet '255.255.255.255/32' AS a,inet '::/0' >>= inet 'ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128' AS b,inet '2001:db8::1/128' << inet '2001:db8::/64' AS c,inet '::/0' && inet '0.0.0.0/0' AS d", []string{"a", "b", "c", "d"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false}}))
		run(fixture.Exec("CREATE DOMAIN spgist_net_domain AS cidr", 0))
		run(fixture.Exec("CREATE TABLE spgist_net_domain_rows(v spgist_net_domain)", 0))
		run(fixture.Exec("CREATE INDEX ON spgist_net_domain_rows USING spgist(v)", 0))
		run(fixture.Exec("INSERT INTO spgist_net_domain_rows VALUES('2001:db8::/64'),(NULL)", 2))
		classRoutes(t, p, b, conn, fixture.Select("SELECT v >>= inet '2001:db8::1/128' AS contains FROM spgist_net_domain_rows ORDER BY v NULLS LAST", []string{"contains"}, []string{"BOOL"}, [][]any{{true}, {nil}}))
	})
}
