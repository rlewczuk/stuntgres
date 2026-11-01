package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnValidationValues(t *testing.T) {
	for i, role := range isnConversionRoles {
		t.Run(role.name, func(t *testing.T) {
			isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				isnValidationMode(run, false)
				validInput := role.input
				if i == 0 {
					validInput = "2222222222222"
				}
				for _, runtime := range []bool{false, true} {
					expression := "$1::" + role.name
					if runtime {
						expression = "$1::text::" + role.name
					}
					for _, tc := range []struct {
						input any
						marked, null bool
					}{
						{validInput, false, false}, {[]byte(validInput), false, false},
						{validInput + "!", true, false}, {isnValidationBad[i] + "!", true, false},
						{isnValidationBad[i][:len(isnValidationBad[i])-1] + "?", false, false},
						{isnValidationBad[i][:len(isnValidationBad[i])-1] + "?!", false, false},
						{nil, false, true}, {[]byte(nil), false, true},
					} {
						step := isnValidationValueStep(b, i, expression, tc.input, tc.marked, tc.null)
						if bytes, typedBytes := tc.input.([]byte); runtime && !b.PG && typedBytes && bytes == nil {
							// The established native TEXT adapter imports typed-nil
							// bytes as empty text; pq instead sends SQL NULL. Keep
							// actual empty ISN input erroneous, with no portal context.
							step = isnValidationQueryError(step.Query, isnConversionDiagnostic("22P02", `invalid input syntax for `+isnConversionInputFamily(role.name)+` number: ""`), tc.input)
						}
						isnConversionRoutes(t, p, b, c, step)
					}
				}
				q := "SELECT is_valid(NULL::" + role.name + ") AS valid,make_valid(NULL::" + role.name + ") AS repaired"
				isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"valid", "repaired"}, []string{"BOOL", strings.ToUpper(role.name)}, [][]any{{nil, nil}}, nil))
				run(fixture.Exec("CREATE DOMAIN iv_domain AS "+role.name, 0))
				domainQuery := "SELECT is_valid($1::iv_domain) AS valid,make_valid($1::iv_domain)::text AS repaired"
				isnConversionRoutes(t, p, b, c, fixture.Select(domainQuery, []string{"valid", "repaired"}, []string{"BOOL", "TEXT"}, [][]any{{false, isnValidationOutput(i)}}, validInput+"!"))
				isnConversionRoutes(t, p, b, c, fixture.Select(domainQuery, []string{"valid", "repaired"}, []string{"BOOL", "TEXT"}, [][]any{{nil, nil}}, nil))
				isnValidationMode(run, true)
				for _, expression := range []string{"$1::" + role.name, "$1::text::" + role.name} {
					for _, input := range []any{isnValidationBad[i], []byte(isnValidationBad[i])} {
						isnConversionRoutes(t, p, b, c, isnValidationValueStep(b, i, expression, input, true, false))
					}
				}
				run(fixture.Exec("CREATE TABLE iv_values(id integer,v "+role.name+")", 0))
				run(fixture.Exec("INSERT INTO iv_values VALUES(1,$1),(2,NULL)", 2, isnValidationBad[i]))
				isnValidationMode(run, false)
				q = "SELECT id,is_valid(v) AS valid,make_valid(v)::text AS repaired,is_valid(make_valid(make_valid(v))) AS idempotent,is_valid(v) AS unchanged FROM iv_values ORDER BY id"
				isnConversionRoutes(t, p, b, c, fixture.Select(q, []string{"id", "valid", "repaired", "idempotent", "unchanged"}, []string{"INT4", "BOOL", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), false, isnValidationOutput(i), true, false}, {int64(2), nil, nil, nil, nil}}))
				for _, input := range []string{role.low, role.high} {
					q = "SELECT is_valid($1::" + role.name + ") AS valid,is_valid(make_valid($1::" + role.name + ")) AS repaired,make_valid($1::" + role.name + ")::ean13=$2::ean13 AS preserved"
					isnConversionRoutes(t, p, b, c, fixture.Select(q, []string{"valid", "repaired", "preserved"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true}}, input+"!", input))
				}
				if i == 5 {
					marked, repaired := "977-1234-567-89-8!", "977-1234-567-89-8"
					if b.PG {
						marked, repaired = "1234-5679", "1234-5679"
					}
					q = "SELECT $1::issn AS original,make_valid($1::issn) AS repaired,is_valid($1::issn) AS valid,($1::issn)::ean13::text AS full"
					isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"original", "repaired", "valid", "full"}, []string{"ISSN", "ISSN", "BOOL", "TEXT"}, [][]any{{marked, repaired, false, "977-1234-567-89-8!"}}, nil, "9771234567898!"))
				}
			})
		})
	}
}

func TestReferenceIsnValidationAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			isnValidationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				isnValidationMode(run, false)
				run(fixture.Exec("CREATE TABLE iv_atomic(id integer,v ean13)", 0))
				run(fixture.Exec("INSERT INTO iv_atomic VALUES(1,'2222222222222'),(2,NULL)", 2))
				for _, q := range []string{"INSERT INTO iv_atomic VALUES(3,$1::text::ean13),(4,$2::text::ean13)", "UPDATE iv_atomic SET v=CASE WHEN id=1 THEN $1::text::ean13 ELSE $2::text::ean13 END"} {
					s := isnConversionRuntimeError(q, isnValidationChecksum(0, isnValidationBad[0]), "2222222222222", isnValidationBad[0])
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					run(fixture.Select("SELECT id,v::text AS v FROM iv_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "222-222222222-2"}, {int64(2), nil}}))
				}
				isnValidationMode(run, true)
				run(fixture.Exec("INSERT INTO iv_atomic VALUES(3,$1),(4,$1)", 2, isnValidationBad[0]))
				run(fixture.Exec("UPDATE iv_atomic SET v=make_valid(v) WHERE id=3", 1))
				run(fixture.Select("SELECT id,is_valid(v) AS valid,v::text AS v FROM iv_atomic ORDER BY id", []string{"id", "valid", "v"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), true, "222-222222222-2"}, {int64(2), nil, nil}, {int64(3), true, "222-222222222-2"}, {int64(4), false, "222-222222222-2!"}}))
			})
		})
	}
}
