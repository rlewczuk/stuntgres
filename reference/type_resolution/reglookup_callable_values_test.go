package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var callableKinds = []struct {
	name, family, input, output string
	oid                         int64
}{
	{"to_regoper", "REGOPER", "||/", "||/", 597},
	{"to_regoperator", "REGOPERATOR", "+(int4,int4)", "+(integer,integer)", 551},
	{"to_regproc", "REGPROC", "boolin", "boolin", 1242},
	{"to_regprocedure", "REGPROCEDURE", "abs(integer)", "abs(integer)", 1397},
}

// The cursor helper already owns the aliases' string/pq-byte transport. Its
// descriptor expectations apply equally to callable results, including no rows.
func callableSelect(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	return callableSelectMetadata(b, q, names, types, values, nil, args...)
}

func callableSelectMetadata(b fixture.Backend, q string, names, types []string, values [][]any, nullable []bool, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.CheckRaw = func(t *testing.T, column, row int, value any) bool {
		switch types[column] {
		case "REGOPER", "REGOPERATOR", "REGPROC", "REGPROCEDURE":
			fixture.RegtypeRaw(t, b.PG, value)
			return true
		}
		return false
	}
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		fixture.RegtypeCursorMetadata(t, rows, b.PG, names, types, nullable)
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		for i, col := range columns {
			if !strings.HasPrefix(types[i], "REG") {
				continue
			}
			length, lengthOK := col.Length()
			precision, scale, decimalOK := col.DecimalSize()
			if length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
				t.Errorf("%s alias flags length=%d/%t decimal=%d,%d/%t", names[i], length, lengthOK, precision, scale, decimalOK)
			}
		}
	}
	return s
}

func TestReferenceRegLookupCallableValues(t *testing.T) {
	regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, family, input, output string
			oid                         int64
		}{
			{"to_regoper", "REGOPER", "||/", "||/", 597},
			{"to_regoper", "REGOPER", `"pg_catalog"."||/"`, "||/", 597},
			{"to_regoperator", "REGOPERATOR", "+(int4,int4)", "+(integer,integer)", 551},
			{"to_regoperator", "REGOPERATOR", "pg_catalog.+(smallint,integer)", "+(smallint,integer)", 552},
			{"to_regoperator", "REGOPERATOR", "+(integer,smallint)", "+(integer,smallint)", 553},
			{"to_regoperator", "REGOPERATOR", "-(NONE,int)", "-(NONE,integer)", 558},
			{"to_regoperator", "REGOPERATOR", ` "pg_catalog" . "+" ( pg_catalog.int4 , integer ) `, "+(integer,integer)", 551},
			{"to_regproc", "REGPROC", "boolin", "boolin", 1242},
			{"to_regproc", "REGPROC", "pg_catalog.now", "now", 1299},
			{"to_regproc", "REGPROC", `"RI_FKey_check_ins"`, `"RI_FKey_check_ins"`, 1644},
			{"to_regprocedure", "REGPROCEDURE", "abs(integer)", "abs(integer)", 1397},
			{"to_regprocedure", "REGPROCEDURE", "pg_catalog.abs(bigint)", "abs(bigint)", 1396},
			{"to_regprocedure", "REGPROCEDURE", `"RI_FKey_check_ins"()`, `"RI_FKey_check_ins"()`, 1644},
		} {
			t.Run(tc.name+"/"+tc.input, func(t *testing.T) {
				expr := tc.name + "(" + regObjectLiteral(tc.input) + ")"
				regObjectBoth(t, p, b, c, callableSelect(b, "SELECT "+expr+" AS value", []string{"value"}, []string{tc.family}, [][]any{{tc.output}}))
				regObjectBoth(t, p, b, c, fixture.Select("SELECT "+expr+"::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{tc.oid}}))
			})
		}
		for _, kind := range callableKinds {
			t.Run(kind.name, func(t *testing.T) {
				q := "SELECT " + kind.name + "($1) AS value"
				held := regLookupHeld(t, p, c, q)
				defer held.Statement.Close()
				regObjectRunStep(t, p, b, held, callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{kind.output}}, kind.input))
				t.Run("held_hard_error", func(t *testing.T) {
					input := "otherdb.pg_catalog." + kind.input
					identity := input
					if opener := strings.IndexByte(identity, '('); opener >= 0 {
						identity = identity[:opener]
					}
					want := callableDiagnostic("0A000", "cross-database references are not implemented: "+identity)
					regObjectRunStep(t, p, b, held, callableErrorStep(q, want, input))
				})
				for _, tc := range []struct{ input, output any }{
					{kind.input, kind.output}, {[]byte(kind.input), kind.output},
					{nil, nil}, {[]byte(nil), nil}, {[]byte{}, nil},
					{"", nil}, {" \t ", nil}, {"a..b", nil}, {"4294967296", nil},
					{kind.input, kind.output}, {"4294967295", "4294967295"},
				} {
					s := callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{tc.output}}, tc.input)
					regObjectBoth(t, p, b, c, s)
					regObjectRunStep(t, p, b, held, s)
				}
				zero := "0"
				if kind.family == "REGPROC" || kind.family == "REGPROCEDURE" {
					zero = "-"
				}
				for _, input := range []string{"0", "4294967295"} {
					output, id := zero, int64(0)
					if input == "4294967295" {
						output, id = input, 4294967295
					}
					regObjectBoth(t, p, b, c, callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{output}}, input))
					regObjectBoth(t, p, b, c, fixture.Select("SELECT "+kind.name+"($1)::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{id}}, input))
				}
				if zero == "-" {
					regObjectRunStep(t, p, b, held, callableSelect(b, q, []string{"value"}, []string{kind.family}, [][]any{{"-"}}, "-"))
				}
				for _, suffix := range []string{"", " WHERE FALSE"} {
					rows := [][]any{{nil}}
					if suffix != "" {
						rows = nil
					}
					regObjectBoth(t, p, b, c, callableSelect(b, "SELECT "+kind.name+"(NULL::text) AS value"+suffix, []string{"value"}, []string{kind.family}, rows))
				}
			})
		}
		var database string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
			t.Fatal(err)
		}
		for _, kind := range callableKinds {
			input := fixture.RegtypeIdentifier(database) + ".pg_catalog." + kind.input
			regObjectBoth(t, p, b, c, callableSelect(b, "SELECT "+kind.name+"($1::text) AS value", []string{"value"}, []string{kind.family}, [][]any{{kind.output}}, input))
		}
	})
}
