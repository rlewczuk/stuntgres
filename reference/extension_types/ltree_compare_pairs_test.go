package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLtreeComparisonPairs(t *testing.T) {
	// Signs follow ordered labels in REL_18_6, not serialized path ordering.
	pairs := []struct{ name, left, right string; sign int }{
		{"empty", "", "a", -1}, {"depth", "a", "a.b", -1},
		{"label_boundary", "a.b", "a-b", -1}, {"label_before_depth", "a.z.z", "b", -1},
		{"label_length", "aa", "b", -1}, {"not_numeric", "a.10", "a.2", -1},
		{"case", "A", "a", -1}, {"equal", "a.b", "a.b", 0},
		{"empty_equal", "", "", 0}, {"boundaries", "a.b", "ab", -1},
		{"label_prefix", "a.b", "a.bc", -1},
	}
	for _, tc := range pairs {
		t.Run(tc.name, func(t *testing.T) {
			ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ltreeInstall(t, p, b, c)
				ltreeComparisonRun(t, p, b, c, fixture.Exec("CREATE TABLE lt_cmp_pair(l ltree,r ltree)", 0))
				q := "SELECT " + ltreeComparisonProjection("$1::ltree", "$2::ltree")
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil { t.Fatal(err) }
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, reverse := range []bool{false, true} {
					left, right, sign := tc.left, tc.right, tc.sign
					if reverse { left, right, sign = right, left, -sign }
					want := ltreeComparisonRow(sign)
					literal := "SELECT " + ltreeComparisonProjection("'"+left+"'::ltree", "'"+right+"'::ltree")
					step := ltreeComparisonStep(literal, want)
					ltreeComparisonRun(t, p, b, c, step)
					ltreeComparisonRun(t, p, b, c, fixture.Prepared(step))
					ltreeComparisonRun(t, p, b, held, ltreeComparisonStep(q, want, left, []byte(right)))
					ltreeComparisonRun(t, p, b, c, fixture.Exec("INSERT INTO lt_cmp_pair VALUES ($1,$2)", 1, left, right))
					ltreeComparisonRun(t, p, b, c, ltreeComparisonStep("SELECT "+ltreeComparisonProjection("l", "r")+" FROM lt_cmp_pair", want))
					ltreeComparisonRun(t, p, b, c, fixture.Exec("DELETE FROM lt_cmp_pair", 1))
				}
			})
		})
	}
}

func TestReferenceLtreeComparisonBoundaries(t *testing.T) {
	long := strings.Repeat("a", 1000)
	deep := strings.Repeat("a.", 65534) + "a"
	for _, tc := range []struct{ name, left, right string; sign int }{
		{"long_equal", long, long, 0}, {"long_last", long, long[:999]+"b", -1},
		{"long_first", "b"+long[1:], long, 1},
		{"deep_equal", deep, deep, 0}, {"deep_last", deep, deep[:len(deep)-1]+"b", -1},
		{"deep_prefix", deep[:len(deep)-2], deep, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				ltreeInstall(t, p, b, c)
				q := "SELECT " + ltreeComparisonProjection("$1::ltree", "$2::ltree")
				for _, reverse := range []bool{false, true} {
					left, right, sign := tc.left, tc.right, tc.sign
					if reverse { left, right, sign = right, left, -sign }
					s := ltreeComparisonStep(q, ltreeComparisonRow(sign), left, []byte(right))
					ltreeComparisonRun(t, p, b, c, s)
					ltreeComparisonRun(t, p, b, c, fixture.Prepared(s))
				}
			})
		})
	}
}

func TestReferenceLtreeComparisonBinding(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "lt_cmp_binding")
	ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE SCHEMA lt_cmp_binding", "CREATE EXTENSION ltree SCHEMA lt_cmp_binding", "SET search_path=public,lt_cmp_binding"} {
			ltreeComparisonRun(t, p, b, c, fixture.Exec(q, 0))
		}
		q := "SELECT " + ltreeComparisonProjection("$1::ltree", "$2::ltree")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil { t.Fatal(err) }
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, args := range [][]any{{nil, "a"}, {"a", nil}, {nil, nil}, {"", nil}} {
			s := ltreeComparisonStep(q, []any{nil,nil,nil,nil,nil,nil}, args...)
			ltreeComparisonRun(t, p, b, c, s)
			ltreeComparisonRun(t, p, b, c, fixture.Prepared(s))
			ltreeComparisonRun(t, p, b, held, s)
		}
		for _, operands := range [][2]string{{"NULL::ltree","'a'::ltree"},{"'a'::ltree","NULL::ltree"},{"NULL::ltree","NULL::ltree"}} {
			s := ltreeComparisonStep("SELECT "+ltreeComparisonProjection(operands[0],operands[1]),[]any{nil,nil,nil,nil,nil,nil})
			ltreeComparisonRun(t,p,b,c,s)
			ltreeComparisonRun(t,p,b,c,fixture.Prepared(s))
		}
		inferred := "SELECT "+ltreeComparisonProjection("$1","$2::ltree")
		for _, s := range []fixture.Step{
			ltreeComparisonStep(inferred,ltreeComparisonRow(-1),"a.b",[]byte("a-b")),
			ltreeComparisonStep(inferred,ltreeComparisonRow(0),[]byte("a.b"),"a.b"),
		} {
			ltreeComparisonRun(t,p,b,c,s)
			ltreeComparisonRun(t,p,b,c,fixture.Prepared(s))
		}
		for i,op := range []string{"=","<>","<","<=",">",">="} {
			s := fixture.Select("SELECT 'a.b'::ltree OPERATOR(lt_cmp_binding."+op+") 'a-b'::ltree AS result",[]string{"result"},[]string{"BOOL"},[][]any{{ltreeComparisonRow(-1)[i]}})
			ltreeComparisonRun(t,p,b,c,s)
			ltreeComparisonRun(t,p,b,c,fixture.Prepared(s))
		}
		ltreeComparisonRun(t, p, b, c, ltreeComparisonStep("SELECT "+ltreeComparisonProjection("'a.b'::ltree", "'a-b'"), ltreeComparisonRow(-1)))
		ltreeComparisonRun(t, p, b, c, fixture.Select("SELECT 'a.b'::ltree OPERATOR(lt_cmp_binding.=) 'a.b'::ltree AS eq", []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
		for _, q := range []string{"CREATE TABLE lt_cmp_empty(id integer,v ltree)", "CREATE TABLE lt_cmp_null(id integer,v ltree)"} {
			ltreeComparisonRun(t, p, b, c, fixture.Exec(q, 0))
		}
		ltreeComparisonRun(t, p, b, c, fixture.Exec("INSERT INTO lt_cmp_null VALUES (1,NULL),(2,NULL)", 2))
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_empty ORDER BY v,id"))
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_null ORDER BY v,id", 1, 2))
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_null WHERE v='a'::ltree ORDER BY id"))
	})
}
