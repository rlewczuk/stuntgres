package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Published release examples followed by independently source-derived boundaries.
var ean13Values = []struct{ input, output string }{
	{"9780123456786", "978-0-12-345678-6"},
	{"9790123456785", "979-0-1234-5678-5"},
	{"9791234567896", "979-123456789-6"},
	{"9771234567898", "977-1234-567-89-8"},
	{"0123456789012", "012-345678901-2"},
	{"1234567890128", "123-456789012-8"},
	{"123-456789012-8", "123-456789012-8"},
	{" 123 456789012 8 ", "123-456789012-8"},
	{"123456789012?", "123-456789012-8"},
	{"1234567890120!", "123-456789012-8!"},
	{"1234567890128!", "123-456789012-8!"},
	{"0000000000000", "000-000000000-0"},
	{"1400000000007", "140000000000-7"},
	{"0999999999993", "099-999999999-3"},
	{"1000000000009", "100-000000000-9"},
	{"1399999999990", "139-999999999-0"},
	{"9780000000002", "978-0-00-000000-2"},
	{"9780199999996", "978-0-19-999999-6"},
	{"9780200000000", "978-0-200-00000-0"},
	{"9780699999991", "978-0-699-99999-1"},
	{"9780700000005", "978-0-7000-0000-5"},
	{"9780849999994", "978-0-8499-9999-4"},
	{"9780850000009", "978-0-85000-000-9"},
	{"9780899999999", "978-0-89999-999-9"},
	{"9780900000003", "978-0-900000-00-3"},
	{"9780949999993", "978-0-949999-99-3"},
	{"9780950000008", "978-0-9500000-0-8"},
	{"9780999999998", "978-0-9999999-9-8"},
	{"9781000000009", "978-1-00-000000-9"},
	{"9790000000001", "979-0-000-00000-1"},
	{"9790099999996", "979-0-099-99999-6"},
	{"9790100000000", "979-0-1000-0000-0"},
	{"9790399999993", "979-0-3999-9999-3"},
	{"9790400000007", "979-0-40000-000-7"},
	{"9790699999990", "979-0-69999-999-0"},
	{"9790700000004", "979-0-700000-00-4"},
	{"9790899999998", "979-0-899999-99-8"},
	{"9790900000002", "979-0-9000000-0-2"},
	{"9790999999997", "979-0-9999999-9-7"},
	{"9791000000008", "979-10-00-00000-8"},
	{"9769999999993", "976999999999-3"},
	{"9770000000003", "977-0000-000-00-3"},
	{"9779999999992", "977-9999-999-99-2"},
}

func TestReferenceEan13Values(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION isn", 0))
		run(fixture.Exec("CREATE TABLE ean_values(id integer NOT NULL,v ean13)", 0))
		insert := ean13Held(t, p, c, "INSERT INTO ean_values VALUES($1,$2)")
		read := ean13Held(t, p, c, "SELECT v FROM ean_values WHERE id=$1")
		for i, tc := range ean13Values {
			t.Run(fmt.Sprintf("value_%02d", i), func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, prepared := range []bool{false, true} {
					s := ean13Select(b, "SELECT "+ean13Literal(tc.input)+"::ean13 AS v", []string{"v"}, []string{"EAN13"}, [][]any{{tc.output}}, nil)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				id := int64(i*4 + 1)
				run(fixture.Exec(fmt.Sprintf("INSERT INTO ean_values VALUES(%d,%s)", id, ean13Literal(tc.input)), 1))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, id+1, tc.input))
				fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, id+2, []byte(tc.input)))
				run(fixture.Prepared(fixture.Exec("INSERT INTO ean_values VALUES($1,$2)", 1, id+3, tc.output)))
				for n := id; n < id+4; n++ {
					s := ean13Select(b, read.SQL, []string{"v"}, []string{"EAN13"}, [][]any{{tc.output}}, []bool{true}, n)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, read, b.Recorder, s)
				}
				run(fixture.Exec(fmt.Sprintf("INSERT INTO ean_values SELECT %d,v FROM ean_values WHERE id=%d", 1000+id, id), 1))
				run(fixture.Select(fmt.Sprintf("SELECT v::text AS v FROM ean_values WHERE id=%d", 1000+id), []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}))
			})
		}
		run(fixture.Exec("INSERT INTO ean_values VALUES(9000,NULL)", 1))
		fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, int64(9001), nil))
		run(fixture.Prepared(fixture.Exec("INSERT INTO ean_values VALUES($1,$2)", 1, int64(9002), nil)))
		for _, id := range []int64{9000, 9001, 9002} {
			s := ean13Select(b, read.SQL, []string{"v"}, []string{"EAN13"}, [][]any{{nil}}, []bool{true}, id)
			run(s)
			run(fixture.Prepared(s))
			fixture.RunStep(t, p.Ctx, read, b.Recorder, s)
		}
	})
}
