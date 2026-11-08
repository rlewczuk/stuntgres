package reference_test

import (
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func groupDependencyCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func groupDependencyExec(t *testing.T, p *fixture.Pair, b fixture.Backend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		if _, err := b.DB.ExecContext(p.Ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

func groupDependencyCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	s.ErrorStage = "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	if s.Diagnostic != nil && s.SelectRows {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func groupDependencyError(q, column string) fixture.Step {
	token := column
	if !strings.Contains(q, token) {
		token = strings.Split(column, ".")[1]
	}
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42803", Message: `column "` + column + `" must appear in the GROUP BY clause or be used in an aggregate function`, Position: strconv.Itoa(strings.Index(q, token) + 1)})
	s.SelectRows = true
	return s
}

func groupDependencyBase(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	groupDependencyExec(t, p, b, "CREATE TABLE grin_gb_t(id int PRIMARY KEY,s text)", "CREATE VIEW grin_gb_v AS SELECT id,s,count(*) FROM grin_gb_t GROUP BY id", "INSERT INTO grin_gb_t VALUES(1,'one'),(2,'two')")
}

func groupDependencyRows(q string) fixture.Step {
	return fixture.Select(q, []string{"id", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(1)}, {int64(2), "two", int64(1)}})
}

func TestReferenceQueryGroupDependencyPrimaryKey(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyBase(t, p, b)
		for _, q := range []string{"SELECT * FROM grin_gb_v ORDER BY id", "SELECT id,s,count(*) FROM grin_gb_t GROUP BY id ORDER BY id", "SELECT id,s,count(*) FROM grin_gb_t GROUP BY 1 ORDER BY id", "SELECT id,s,count(*) FROM grin_gb_t GROUP BY id,s ORDER BY id", "SELECT id,s,count(*) FROM grin_gb_t GROUP BY id::integer ORDER BY id"} {
			groupDependencyCheck(t, p, b, groupDependencyRows(q))
		}
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id AS k,s,count(*) FROM grin_gb_t GROUP BY k HAVING s='one' ORDER BY s", []string{"k", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(1)}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id FROM grin_gb_t GROUP BY id ORDER BY s DESC", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}))
		for _, q := range []string{"SELECT s,count(*) FROM grin_gb_t", "SELECT s,count(*) FROM grin_gb_t GROUP BY id+0", "SELECT s,count(*) FROM grin_gb_t GROUP BY id::bigint"} {
			groupDependencyCheck(t, p, b, groupDependencyError(q, "grin_gb_t.s"))
		}
		for _, spec := range []struct{ name, ddl string }{{"gb_un", "id int UNIQUE NOT NULL,s text"}, {"gb_nullable", "id int UNIQUE,s text"}, {"gb_def", "id int PRIMARY KEY DEFERRABLE,s text"}, {"gb_none", "id int,s text"}} {
			groupDependencyExec(t, p, b, "CREATE TABLE "+spec.name+"("+spec.ddl+")")
			groupDependencyCheck(t, p, b, groupDependencyError("SELECT s,count(*) FROM "+spec.name+" GROUP BY id", spec.name+".s"))
		}
		groupDependencyExec(t, p, b, "CREATE TABLE gb_composite(a int,b int,s text,CONSTRAINT gb_composite_pk PRIMARY KEY(a,b))", "INSERT INTO gb_composite VALUES(1,2,'pair'),(1,3,NULL)")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT a,b,s,count(*) FROM gb_composite GROUP BY b,a,a ORDER BY b", []string{"a", "b", "s", "count"}, []string{"INT4", "INT4", "TEXT", "INT8"}, [][]any{{int64(1), int64(2), "pair", int64(1)}, {int64(1), int64(3), nil, int64(1)}}))
		groupDependencyCheck(t, p, b, groupDependencyError("SELECT a,s,count(*) FROM gb_composite GROUP BY a", "gb_composite.s"))
		groupDependencyExec(t, p, b, "CREATE TABLE gb_empty(id int PRIMARY KEY,s text)")
		groupDependencyCheck(t, p, b, groupDependencyError("SELECT s,count(*) FROM gb_empty HAVING FALSE", "gb_empty.s"))
		bad := groupDependencyError("CREATE VIEW gb_bad AS SELECT s,count(*) FROM gb_empty", "gb_empty.s")
		bad.SelectRows = false
		groupDependencyCheck(t, p, b, bad)
		groupDependencyCheck(t, p, b, fixture.Select("SELECT to_regclass('gb_bad') IS NULL AS absent", []string{"absent"}, []string{"BOOL"}, [][]any{{true}}))
	})
}

func TestReferenceQueryGroupDependencyParenthesizedList(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyExec(t, p, b, "CREATE TABLE gb_list(a int,b int,s text,x int,CONSTRAINT gb_list_pk PRIMARY KEY(a,b))", "INSERT INTO gb_list VALUES(1,2,'pair',9),(1,3,'other',8)")
		columns := []string{"a", "b", "s", "count"}
		types := []string{"INT4", "INT4", "TEXT", "INT8"}
		rows := [][]any{{int64(1), int64(3), "other", int64(1)}, {int64(1), int64(2), "pair", int64(1)}}
		for _, keys := range []string{"(a,b)", "(b,a)", "(b,a,x)", "((a,b))", "(((b)),((a)),x)"} {
			t.Run(keys, func(t *testing.T) {
				q := "SELECT a,b,s,count(*) FROM gb_list GROUP BY " + keys + " HAVING s IS NOT NULL ORDER BY s"
				groupDependencyCheck(t, p, b, fixture.Select(q, columns, types, rows))
				groupDependencyCheck(t, p, b, fixture.Select("SELECT a,b FROM gb_list GROUP BY "+keys+" HAVING s='pair' ORDER BY s", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
				groupDependencyExec(t, p, b, "CREATE VIEW gb_list_view AS "+q)
				groupDependencyCheck(t, p, b, fixture.Select("SELECT * FROM gb_list_view ORDER BY s", columns, types, rows))
				groupDependencyEdge(t, p, b, "gb_list_view", "gb_list", "gb_list_pk")
				groupDependencyRestrict(t, p, b, "gb_list", "gb_list_pk", "gb_list_view")
				groupDependencyExec(t, p, b, "DROP VIEW gb_list_view")
			})
		}
		for _, keys := range []string{"(l.a)", "((l.a))", "(l.a,l.x)"} {
			for _, q := range []string{
				"SELECT l.a,l.s,count(*) FROM gb_list l GROUP BY " + keys,
				"SELECT l.a FROM gb_list l GROUP BY " + keys + " HAVING l.s IS NOT NULL",
				"SELECT l.a FROM gb_list l GROUP BY " + keys + " ORDER BY l.s",
			} {
				groupDependencyCheck(t, p, b, groupDependencyError(q, "l.s"))
			}
		}
	})
}

func TestReferenceQueryGroupDependencyJoins(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyExec(t, p, b, "CREATE TABLE gb_p(id int PRIMARY KEY,s text)", "CREATE TABLE gb_q(id int PRIMARY KEY,s text)", "INSERT INTO gb_p VALUES(1,'one'),(2,'two')", "INSERT INTO gb_q VALUES(1,'first'),(3,'third')", "CREATE TABLE gb_child(id int)", "INSERT INTO gb_child VALUES(1),(1),(2)")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT p.id,p.s,count(*) FROM gb_p p JOIN gb_child c ON c.id=p.id GROUP BY p.id ORDER BY p.id", []string{"id", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(2)}, {int64(2), "two", int64(1)}}))
		for _, join := range []string{"JOIN", "LEFT JOIN"} {
			q := "SELECT p.id,q.s,count(*) FROM gb_p p " + join + " gb_p q ON p.id=q.id GROUP BY p.id"
			groupDependencyCheck(t, p, b, groupDependencyError(q, "q.s"))
		}
		for _, tc := range []struct {
			join string
			rows [][]any
		}{{"LEFT JOIN", [][]any{{int64(1), "one", "first", int64(1)}, {int64(2), "two", nil, int64(1)}}}, {"RIGHT JOIN", [][]any{{int64(1), "one", "first", int64(1)}, {nil, nil, "third", int64(1)}}}, {"FULL JOIN", [][]any{{int64(1), "one", "first", int64(1)}, {int64(2), "two", nil, int64(1)}, {nil, nil, "third", int64(1)}}}} {
			groupDependencyCheck(t, p, b, fixture.Select("SELECT p.id,p.s,q.s,count(*) FROM gb_p p "+tc.join+" gb_q q ON p.id=q.id GROUP BY p.id,q.id ORDER BY p.id NULLS LAST", []string{"id", "s", "s", "count"}, []string{"INT4", "TEXT", "TEXT", "INT8"}, tc.rows))
		}
		groupDependencyCheck(t, p, b, fixture.Select("SELECT p.id,q IS NULL AS absent,q::text AS whole FROM gb_p p LEFT JOIN gb_q q ON p.id=q.id GROUP BY p.id,q.id ORDER BY p.id", []string{"id", "absent", "whole"}, []string{"INT4", "BOOL", "TEXT"}, [][]any{{int64(1), false, "(1,first)"}, {int64(2), true, nil}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT p.s,count(*) FROM gb_p p JOIN gb_q q USING(id) GROUP BY id", []string{"s", "count"}, []string{"TEXT", "INT8"}, [][]any{{"one", int64(1)}}))
		for _, q := range []string{"SELECT q.s,count(*) FROM gb_p p JOIN gb_q q USING(id) GROUP BY id", "SELECT p.s,count(*) FROM gb_p p FULL JOIN gb_q q USING(id) GROUP BY id"} {
			col := "q.s"
			if strings.HasPrefix(q, "SELECT p.s") {
				col = "p.s"
			}
			groupDependencyCheck(t, p, b, groupDependencyError(q, col))
		}
	})
}

func TestReferenceQueryGroupDependencySubqueries(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyBase(t, p, b)
		groupDependencyExec(t, p, b, "CREATE VIEW gb_plain AS SELECT * FROM grin_gb_t")
		for _, tc := range []struct{ q, col string }{{"SELECT d.s,count(*) FROM (SELECT * FROM grin_gb_t) d GROUP BY d.id", "d.s"}, {"SELECT s,count(*) FROM gb_plain GROUP BY id", "gb_plain.s"}, {"WITH d AS (SELECT * FROM grin_gb_t) SELECT d.s,count(*) FROM d GROUP BY d.id", "d.s"}} {
			groupDependencyCheck(t, p, b, groupDependencyError(tc.q, tc.col))
		}
		for _, q := range []string{"SELECT * FROM (SELECT id,s,count(*) FROM grin_gb_t GROUP BY id) d ORDER BY id", "SELECT id,(SELECT s) AS s,count(*) FROM grin_gb_t GROUP BY id ORDER BY id"} {
			groupDependencyCheck(t, p, b, groupDependencyRows(q))
		}
		q := "SELECT (SELECT p.s),count(*) FROM grin_gb_t p"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42803", Message: `subquery uses ungrouped column "p.s" from outer query`, Position: strconv.Itoa(strings.Index(q, "p.s") + 1)})
		s.SelectRows = true
		groupDependencyCheck(t, p, b, s)
	})
}

func TestReferenceQueryGroupDependencyPayload(t *testing.T) {
	groupDependencyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		groupDependencyExec(t, p, b, "CREATE TABLE gb_payload(id int PRIMARY KEY,j json,s text)", `INSERT INTO gb_payload VALUES(1,'{"v":1}',NULL),(2,'[2,3]','two')`)
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,j,s,count(*) FROM gb_payload GROUP BY id ORDER BY id", []string{"id", "j", "s", "count"}, []string{"INT4", "JSON", "TEXT", "INT8"}, [][]any{{int64(1), `{"v":1}`, nil, int64(1)}, {int64(2), `[2,3]`, "two", int64(1)}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,tableoid='gb_payload'::regclass AS owner,ctid IS NOT NULL AS located FROM gb_payload GROUP BY id ORDER BY id", []string{"id", "owner", "located"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, true}}))
		groupDependencyExec(t, p, b, "CREATE TABLE gb_parent(id int PRIMARY KEY,s text)", "CREATE TABLE gb_inherited() INHERITS(gb_parent)", "INSERT INTO gb_parent VALUES(1,'one'),(2,'two')", "INSERT INTO gb_inherited VALUES(1,'one')")
		groupDependencyCheck(t, p, b, fixture.Select("SELECT p.id,p::text AS whole,count(*) FROM ONLY gb_parent p GROUP BY p.id ORDER BY p.id", []string{"id", "whole", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "(1,one)", int64(1)}, {int64(2), "(2,two)", int64(1)}}))
		groupDependencyCheck(t, p, b, fixture.Select("SELECT id,s,count(*) FROM gb_parent GROUP BY id ORDER BY id", []string{"id", "s", "count"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "one", int64(2)}, {int64(2), "two", int64(1)}}))
		groupDependencyCheck(t, p, b, groupDependencyRows("SELECT id,s,count(*) FROM ONLY gb_parent GROUP BY id ORDER BY id"))
	})
}
