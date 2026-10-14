package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Builtin names and their independently specified visible spellings avoid
// allocator identities and do not derive expectations from either backend.
var regObjectBoundaryAliases = []struct{ kind, input, output string }{
	{"regcollation", `pg_catalog."C"`, `"C"`},
	{"regnamespace", "pg_catalog", "pg_catalog"},
}

func TestReferenceRegObjectBoundaryStringCasts(t *testing.T) {
	regObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, alias := range regObjectBoundaryAliases {
			t.Run(alias.kind, func(t *testing.T) {
				literal := regObjectLiteral(alias.input)
				value := literal + "::" + alias.kind
				for _, category := range []struct{ sqlType, metadata string }{
					{"text", "TEXT"},
					{"varchar(20)", "VARCHAR"},
					{"character(20)", "BPCHAR"},
					{"name", "NAME"},
				} {
					t.Run(category.metadata, func(t *testing.T) {
						output := alias.output
						if category.metadata == "BPCHAR" {
							output += strings.Repeat(" ", 20-len(output))
						}
						query := "SELECT " + value + "::" + category.sqlType + " AS s," +
							literal + "::" + category.sqlType + "::" + alias.kind + " AS r," +
							"NULL::" + alias.kind + "::" + category.sqlType + " AS ns," +
							"NULL::" + category.sqlType + "::" + alias.kind + " AS nr"
						regObjectBoth(t, p, b, c, regObjectSelect(query,
							[]string{"s", "r", "ns", "nr"},
							[]string{category.metadata, strings.ToUpper(alias.kind), category.metadata, strings.ToUpper(alias.kind)},
							[][]any{{output, alias.output, nil, nil}}))
					})
				}
				// Explicit character/varchar typmods truncate the display string;
				// they neither reject it nor retarget the underlying object identity.
				regObjectBoth(t, p, b, c, regObjectSelect(
					"SELECT "+value+"::character(2) AS c,"+value+"::varchar(2) AS v",
					[]string{"c", "v"}, []string{"BPCHAR", "VARCHAR"},
					[][]any{{alias.output[:2], alias.output[:2]}}))
				regObjectBoth(t, p, b, c, regObjectError("SELECT TRUE::"+alias.kind,
					"42846", "cannot cast type boolean to "+alias.kind, "12", ""))
				// Even NULL must not make an unrelated object-alias cast admissible.
				regObjectBoth(t, p, b, c, regObjectError("SELECT NULL::"+alias.kind+"::regtype AS r",
					"42846", "cannot cast type "+alias.kind+" to regtype", "26", ""))
			})
		}
	})
}

func TestReferenceRegObjectBoundaryByteParameters(t *testing.T) {
	regObjectScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, alias := range regObjectBoundaryAliases {
			t.Run(alias.kind, func(t *testing.T) {
				query := "SELECT $1::" + alias.kind + " AS r"
				statement, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				defer statement.Close()
				for _, route := range []struct {
					name     string
					executor fixture.Executor
				}{
					{"direct", c},
					{"held", fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}},
				} {
					t.Run(route.name, func(t *testing.T) {
						valid := regObjectSelect(query, []string{"r"}, []string{strings.ToUpper(alias.kind)},
							[][]any{{alias.output}}, []byte(alias.input))
						regObjectRunStep(t, p, b, route.executor, valid)
						// Non-nil empty bytes remain malformed name input, not SQL NULL.
						empty := regObjectError(query, "42602", "invalid name syntax", "",
							"unnamed portal parameter $1 = ''", []byte{})
						empty.ErrorStage = "execute"
						regObjectRunStep(t, p, b, route.executor, empty)
						for _, value := range []any{nil, []byte(nil)} {
							regObjectRunStep(t, p, b, route.executor, regObjectSelect(query,
								[]string{"r"}, []string{strings.ToUpper(alias.kind)}, [][]any{{nil}}, value))
						}
						// In particular, the same held frame survives error -> typed
						// nil -> valid without retaining a prior payload or diagnostic.
						regObjectRunStep(t, p, b, route.executor, valid)
					})
				}
			})
		}
	})
}
