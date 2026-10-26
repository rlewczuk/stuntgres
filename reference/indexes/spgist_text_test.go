package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSPGiSTTextPredicates(t *testing.T) {
	for _, typ := range []string{"text", "varchar", "spgist_text_domain"} {
		t.Run(typ, func(t *testing.T) {
			classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				conn, run := classConn(t, p, b)
				if typ == "spgist_text_domain" {
					run(fixture.Exec("CREATE DOMAIN spgist_text_domain AS text", 0))
				}
				run(fixture.Exec("CREATE TABLE spgist_text(id integer,v "+typ+")", 0))
				run(fixture.Exec("INSERT INTO spgist_text VALUES(1,''),(2,'a'),(3,'aa'),(4,'b'),(5,'é'),(6,NULL),(7,'a')", 7))
				check := func(changed bool) {
					for _, c := range []struct {
						op  string
						ids []int64
					}{
						{"=", []int64{2, 7}}, {"<", []int64{1}}, {"<=", []int64{1, 2, 7}}, {">", []int64{3, 4, 5}}, {">=", []int64{2, 3, 4, 5, 7}},
						{"~<~", []int64{1}}, {"~<=~", []int64{1, 2, 7}}, {"~>~", []int64{3, 4, 5}}, {"~>=~", []int64{2, 3, 4, 5, 7}}, {"^@", []int64{2, 3, 7}},
					} {
						if changed {
							switch c.op {
							case "=":
								c.ids = nil
							case "<", "<=", "~<~", "~<=~":
								c.ids = []int64{1}
							case "^@":
								c.ids = []int64{3, 8}
							default:
								c.ids = []int64{2, 3, 4, 5, 8}
							}
						}
						var rows [][]any
						for _, id := range c.ids {
							rows = append(rows, []any{id})
						}
						classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_text WHERE v "+c.op+" $1::text ORDER BY id", []string{"id"}, []string{"INT4"}, rows, "a"))
						classRoutes(t, p, b, conn, fixture.Select("SELECT v "+c.op+" 'a' AS hit FROM spgist_text WHERE id=6", []string{"hit"}, []string{"BOOL"}, [][]any{{nil}}))
					}
					members := [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(7)}}
					if changed {
						members = [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(4)}, {int64(5)}, {int64(8)}}
					}
					classRoutes(t, p, b, conn, fixture.Select("SELECT id FROM spgist_text WHERE v ^@ '' ORDER BY id", []string{"id"}, []string{"INT4"}, members))
				}
				q := "SELECT id FROM spgist_text WHERE v ^@ $1::text ORDER BY id"
				held := classHeld(t, p, conn, q)
				check(false)
				run(fixture.Exec("CREATE INDEX spgist_text_i ON spgist_text USING spgist(v text_ops)", 0))
				check(false)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}, {int64(7)}}, "a"))
				run(fixture.Exec("INSERT INTO spgist_text VALUES(8,'ab')", 1))
				run(fixture.Exec("UPDATE spgist_text SET v='z' WHERE id=2", 1))
				run(fixture.Exec("DELETE FROM spgist_text WHERE id=7", 1))
				check(true)
				classRoutes(t, p, b, conn, fixture.Select(q, []string{"id"}, []string{"INT4"}, [][]any{{int64(3)}, {int64(8)}}, "a"))
				classRoutes(t, p, b, conn, fixture.Select("SELECT text_pattern_lt('a','aa') AS prefix,text_pattern_le('a','a') AS eq,text_pattern_ge('é','b') AS utf8,text_pattern_gt('a ','a') AS spaces,text_pattern_lt('A','a') AS case_order,text_pattern_gt(NULL::text,'a') AS strict", []string{"prefix", "eq", "utf8", "spaces", "case_order", "strict"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, nil}}))
			})
		})
	}
}
