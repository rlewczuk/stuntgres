package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnOrdering(t *testing.T) {
	for _, role := range isnConversionRoles {
		t.Run(role.name, func(t *testing.T) {
			isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				run(fixture.Exec("CREATE TABLE isn_order(id integer NOT NULL,v "+role.name+")", 0))
				// Even empty and NULL-only input must bind the real installed sort.
				q := "SELECT v FROM isn_order ORDER BY v ASC NULLS LAST,id ASC"
				isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"v"}, []string{strings.ToUpper(role.name)}, [][]any{}, []bool{true}))
				run(fixture.Exec("INSERT INTO isn_order VALUES(4,NULL)", 1))
				isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"v"}, []string{strings.ToUpper(role.name)}, [][]any{{nil}}, []bool{true}))
				low, high := role.low, role.high
				if role.name == "ean13" {
					low, high = "0123456789012", "9781234567897"
				}
				run(fixture.Exec("INSERT INTO isn_order VALUES(1,"+isnConversionLiteral(high)+"),(2,"+isnConversionLiteral(high+"!")+"),(3,"+isnConversionLiteral(low)+"),(5,"+isnConversionLiteral(high)+")", 4))
				for _, s := range []fixture.Step{
					isnConversionIDs("SELECT id FROM isn_order ORDER BY v ASC NULLS LAST,id ASC", 3, 1, 5, 2, 4),
					isnConversionIDs("SELECT id FROM isn_order ORDER BY v DESC NULLS FIRST,id ASC", 4, 2, 1, 5, 3),
					isnConversionIDs("SELECT id FROM isn_order WHERE v < "+isnConversionLiteral(high)+"::"+role.name+" ORDER BY id", 3),
					isnConversionIDs("SELECT id FROM isn_order WHERE v = "+isnConversionLiteral(high)+"::"+role.name+" ORDER BY id", 1, 5),
				} {
					isnConversionRoutes(t, p, b, c, s)
				}
				if role.name == "ean13" {
					run(fixture.Exec("INSERT INTO isn_order VALUES(6,'0000000000000'),(7,'9999999999994')", 2))
					isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM isn_order ORDER BY v ASC NULLS LAST,id ASC", 6, 3, 1, 5, 2, 7, 4))
				}
			})
		})
	}
}
