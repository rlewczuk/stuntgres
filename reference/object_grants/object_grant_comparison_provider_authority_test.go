package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantArrayComparisonProviderAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_array_provider(id INT,a INT[],b INT[])", 0))
		run(fixture.Exec("INSERT INTO og_array_provider VALUES(1,'{1}','{2}'),(2,NULL,'{2}')", 2))
		run(fixture.Exec("GRANT SELECT ON og_array_provider TO og_object_reader", 0))
		for _, tc := range []struct {
			op, name string
			value    bool
		}{
			{"=", "array_eq", false}, {"<>", "array_ne", true},
			{"<", "array_lt", true}, {"<=", "array_le", true},
			{">", "array_gt", false}, {">=", "array_ge", false},
		} {
			q := "SELECT a" + tc.op + "b AS v FROM og_array_provider WHERE id=1"
			cases := []objectGrantProviderCase{
				{fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.value}}), true, true},
				{fixture.Select("SELECT a"+tc.op+"b AS v FROM og_array_provider WHERE id=2", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}), true, true},
				{fixture.Select("SELECT a"+tc.op+"b AS v FROM og_array_provider WHERE id>99", []string{"v"}, []string{"BOOL"}, nil), true, true},
				{fixture.Select("SELECT a"+tc.op+"b AS v FROM og_array_provider WHERE FALSE", []string{"v"}, []string{"BOOL"}, nil), true, true},
				{fixture.Select("SELECT NULL::INT[]"+tc.op+"b AS v FROM og_array_provider WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}), false, false},
				{fixture.Select("SELECT a"+tc.op+"$1::INT[] AS v FROM og_array_provider WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{tc.value}}, "{2}"), true, true},
			}
			other, otherValue := "=", false
			if tc.op == "=" {
				other, otherValue = "<>", true
			}
			control := fixture.Select("SELECT a"+other+"b AS v FROM og_array_provider WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{otherValue}})
			objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+tc.name+"(anyarray,anyarray)", "function", tc.name, cases, []fixture.Step{control})
		}
		// Structural comparison's INT4 leaf is internal support, not a
		// separately caller-checked routine. Demonstrate actual leaf denial
		// alongside successful structural equality on the same values.
		defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.int4eq(integer,integer)"})
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.int4eq(integer,integer) FROM PUBLIC", 0))
		fixture.RoleGrantActor(run, "og_object_reader", func() {
			run(fixture.Select("SELECT a=b AS v FROM og_array_provider ORDER BY id", []string{"v"}, []string{"BOOL"}, [][]any{{false}, {nil}}))
			s := fixture.ImplicitError("SELECT $1::INTEGER=$2::INTEGER AS v", fixture.Diagnostic{Code: "42501", Message: "permission denied for function int4eq"}, int64(1), int64(2))
			s.SelectRows, s.ErrorStage = true, "execute"
			run(s)
		})
	})
}

func TestReferenceObjectGrantBitComparisonProviderAuthority(t *testing.T) {
	for _, family := range []struct{ name, formal, left, right string }{
		{"bit", "bit", "a", "b"},
		{"varbit", "varbit", "v", "w"},
		{"mixed", "varbit", "a", "w"},
	} {
		t.Run(family.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_bit_cmp_provider(id INT,a BIT(1),b BIT(2),v BIT VARYING,w BIT VARYING)", 0))
				run(fixture.Exec("INSERT INTO og_bit_cmp_provider VALUES(1,B'1',B'01',B'1',B'01'),(2,NULL,B'01',NULL,B'01')", 2))
				run(fixture.Exec("GRANT SELECT ON og_bit_cmp_provider TO og_object_reader", 0))
				for _, tc := range []struct {
					op, suffix string
					value      bool
				}{
					{"=", "eq", false}, {"<>", "ne", true}, {"<", "lt", false},
					{"<=", "le", false}, {">", "gt", true}, {">=", "ge", true},
				} {
					name := "bit" + tc.suffix
					if family.formal == "varbit" {
						name = "varbit" + tc.suffix
					}
					expr := family.left + tc.op + family.right
					cases := []objectGrantProviderCase{
						{fixture.Select("SELECT "+expr+" AS v FROM og_bit_cmp_provider WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{tc.value}}), true, true},
						{fixture.Select("SELECT "+expr+" AS v FROM og_bit_cmp_provider WHERE id=2", []string{"v"}, []string{"BOOL"}, [][]any{{nil}}), true, true},
						{fixture.Select("SELECT "+expr+" AS v FROM og_bit_cmp_provider WHERE id>99", []string{"v"}, []string{"BOOL"}, nil), true, true},
						{fixture.Select("SELECT "+expr+" AS v FROM og_bit_cmp_provider WHERE FALSE", []string{"v"}, []string{"BOOL"}, nil), true, true},
						{fixture.Select("SELECT CASE WHEN FALSE THEN "+expr+" ELSE TRUE END AS v FROM og_bit_cmp_provider WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{true}}), false, false},
					}
					// Opposite-family same opcode stays allowed, including when
					// mixed operands selected VARBIT rather than BIT.
					controlLeft, controlRight := "v", "w"
					if family.formal == "varbit" {
						controlLeft, controlRight = "a", "b"
					}
					control := fixture.Select("SELECT "+controlLeft+tc.op+controlRight+" AS v FROM og_bit_cmp_provider WHERE id=1", []string{"v"}, []string{"BOOL"}, [][]any{{tc.value}})
					objectGrantProviderLifecycle(t, p, b, c, run, "pg_catalog."+name+"("+family.formal+","+family.formal+")", "function", name, cases, []fixture.Step{control})
				}
			})
		})
	}
}
