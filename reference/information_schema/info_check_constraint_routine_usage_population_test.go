package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCheckConstraintRoutineUsagePopulation(t *testing.T) {
	ccruScenario(t, func(c *ccruCase) {
		t := c.t
		f, g := c.seed()
		c.exec(
			"CREATE TABLE ccru_one(v integer CONSTRAINT ccru_one CHECK(ccru_f(v)))",
			"CREATE TABLE ccru_two(v integer CONSTRAINT ccru_two CHECK(ccru_f(v) AND ccru_g(v) AND ccru_f(v)))",
			"CREATE DOMAIN ccru_d AS integer CONSTRAINT ccru_dom CHECK(ccru_f(VALUE))",
			"CREATE TABLE ccru_negative(v integer NOT NULL UNIQUE, CONSTRAINT ccru_constant CHECK(true), CONSTRAINT ccru_builtin CHECK(abs(v)>0), CONSTRAINT ccru_null CHECK(NULL::regprocedure IS NULL), CONSTRAINT ccru_text CHECK(v::text <> 'ccru_f(integer)'))",
			"CREATE FUNCTION ccru_h(integer) RETURNS boolean RETURN ccru_g($1)",
			"CREATE TABLE ccru_wrap(v integer CONSTRAINT ccru_wrap CHECK(ccru_h(v)))",
			"CREATE DOMAIN ccru_wrap_d AS integer CONSTRAINT ccru_wrap_dom CHECK(ccru_h(VALUE))",
			"CREATE TABLE ccru_alias(v integer CONSTRAINT ccru_alias CHECK('ccru_g(integer)'::regprocedure IS NOT NULL))",
			"CREATE FUNCTION ccru_f(bigint) RETURNS boolean RETURN $1 > 0",
			"CREATE TABLE ccru_over(v bigint CONSTRAINT ccru_over CHECK(ccru_f(v)))",
		)
		h := c.identity("public", "ccru_h", "integer")
		over := c.identity("public", "ccru_f", "bigint")
		// Both names occupy 63 UTF8 bytes, with different three-byte-rune
		// alignment at every possible decimal OID suffix length.
		longNames := []string{strings.Repeat("雪", 21), "a" + strings.Repeat("雪", 20) + "aa"}
		var longRows [][]any
		var longEdges [][2]string
		crossesRune := false
		for i, name := range longNames {
			constraint := []string{"ccru_long_a", "ccru_long_b"}[i]
			c.exec(`CREATE FUNCTION "`+name+`"(integer) RETURNS boolean RETURN $1 > 0`, `CREATE TABLE `+constraint+`(v integer CONSTRAINT `+constraint+` CHECK("`+name+`"(v)))`)
			specific := c.identity("public", name, "integer")
			longRows = append(longRows, c.row("public", constraint, "public", specific))
			longEdges = append(longEdges, [2]string{constraint, `"` + name + `"(integer)`})
			room := 63 - len(specific[strings.LastIndexByte(specific, '_'):])
			if !utf8.ValidString(name[:room]) {
				crossesRune = true
			}
		}
		if !crossesRune {
			t.Fatal("long-name witnesses did not put a UTF8 rune across the actual OID-prefix byte cutoff")
		}
		want := [][]any{
			c.row("public", "ccru_one", "public", f), c.row("public", "ccru_two", "public", f), c.row("public", "ccru_two", "public", g),
			c.row("public", "ccru_dom", "public", f), c.row("public", "ccru_wrap", "public", h), c.row("public", "ccru_wrap_dom", "public", h),
			c.row("public", "ccru_alias", "public", g), c.row("public", "ccru_over", "public", over),
		}
		want = append(want, longRows...)
		c.read(want...)
		// All procedure edges, not only those matching the expected routine.
		q := `SELECT c.conname,d.classid='pg_constraint'::regclass::oid AS constraint_class,d.objid::bigint,d.objsubid,d.refclassid='pg_proc'::regclass::oid AS procedure_class,d.refobjid::bigint,d.refobjsubid,d.deptype FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace JOIN pg_depend d ON d.classid='pg_constraint'::regclass::oid AND d.objid=c.oid AND d.refclassid='pg_proc'::regclass::oid WHERE n.nspname='public' AND c.conname LIKE 'ccru_%'`
		var edges [][]any
		pairs := [][2]string{{"ccru_one", "ccru_f(integer)"}, {"ccru_two", "ccru_f(integer)"}, {"ccru_two", "ccru_g(integer)"}, {"ccru_dom", "ccru_f(integer)"}, {"ccru_wrap", "ccru_h(integer)"}, {"ccru_wrap_dom", "ccru_h(integer)"}, {"ccru_alias", "ccru_g(integer)"}, {"ccru_over", "ccru_f(bigint)"}}
		pairs = append(pairs, longEdges...)
		for _, pair := range pairs {
			var routineID, constraintID int64
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT $1::regprocedure::oid::bigint", pair[1]).Scan(&routineID); err != nil || routineID <= 0 {
				t.Fatalf("routine identity=%d error=%v", routineID, err)
			}
			if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT c.oid::bigint FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname='public' AND c.conname=$1", pair[0]).Scan(&constraintID); err != nil || constraintID <= 0 {
				t.Fatalf("constraint identity=%d error=%v", constraintID, err)
			}
			edges = append(edges, []any{pair[0], true, constraintID, int64(0), true, routineID, int64(0), "n"})
		}
		s := fixture.Select(q, []string{"conname", "constraint_class", "objid", "objsubid", "procedure_class", "refobjid", "refobjsubid", "deptype"}, []string{"NAME", "BOOL", "INT8", "INT4", "BOOL", "INT8", "INT4", "CHAR"}, edges)
		s.Bag = true
		c.run(s)
		c.run(fixture.Prepared(s))
		q = `SELECT c.conname,c.contype,r.relname,t.typname,c.convalidated,c.conenforced,pg_get_constraintdef(c.oid),pg_get_expr(c.conbin,c.conrelid) FROM pg_constraint c LEFT JOIN pg_class r ON r.oid=c.conrelid LEFT JOIN pg_type t ON t.oid=c.contypid WHERE c.conname IN ('ccru_one','ccru_dom') ORDER BY c.conname`
		c.run(fixture.Select(q, []string{"conname", "contype", "relname", "typname", "convalidated", "conenforced", "pg_get_constraintdef", "pg_get_expr"}, []string{"NAME", "CHAR", "NAME", "NAME", "BOOL", "BOOL", "TEXT", "TEXT"}, [][]any{
			{"ccru_dom", "c", nil, "ccru_d", true, true, "CHECK (ccru_f(VALUE))", "ccru_f(VALUE)"},
			{"ccru_one", "c", "ccru_one", nil, true, true, "CHECK (ccru_f(v))", "ccru_f(v)"},
		}))
	})
	t.Run("same_name_distinct", func(t *testing.T) {
		ccruScenario(t, func(c *ccruCase) {
			f, g := c.seed()
			c.exec("CREATE TABLE ccru_a(v integer CONSTRAINT ccru_dup CHECK(ccru_f(v)))", "CREATE TABLE ccru_b(v integer CONSTRAINT ccru_dup CHECK(ccru_g(v)))", "CREATE TABLE ccru_c(v integer CONSTRAINT ccru_dup CHECK(ccru_f(v)))", "CREATE SCHEMA ccru_moved", "CREATE TABLE ccru_moved.ccru_d(v integer CONSTRAINT ccru_dup CHECK(public.ccru_f(v)))")
			rows := [][]any{c.row("public", "ccru_dup", "public", f), c.row("public", "ccru_dup", "public", g), c.row("ccru_moved", "ccru_dup", "public", f)}
			c.read(rows...)
			q := `SELECT a.oid<>b.oid AS different_constraints,a.conrelid<>b.conrelid AS different_tables,a.connamespace=b.connamespace AS same_schema FROM pg_constraint a JOIN pg_constraint b ON a.conname=b.conname WHERE a.conname='ccru_dup' AND a.conrelid='ccru_a'::regclass::oid AND b.conrelid='ccru_b'::regclass::oid`
			c.run(fixture.Select(q, []string{"different_constraints", "different_tables", "same_schema"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
			c.exec("DROP TABLE ccru_a")
			c.read(rows...)
			c.exec("DROP TABLE ccru_c")
			c.read(rows[1:]...)
			c.exec("DROP TABLE ccru_moved.ccru_d")
			c.read(rows[1])
		})
	})
}
