package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func aggregateDirectScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { c.Close() })
			body(t, p, b, c)
		})
	}
}

func aggregateDirectExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, queries ...string) {
	t.Helper()
	for _, q := range queries {
		createAggregateRun(t, p, b, c, fixture.Exec(q, 0))
	}
}

func aggregateDirectSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	aggregateDirectExec(t, p, b, c,
		"CREATE FUNCTION ad_s(integer,integer) RETURNS integer LANGUAGE SQL AS $$SELECT coalesce($1,0)+coalesce($2,0)$$",
		"CREATE FUNCTION ad_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1*10$$",
		"CREATE AGGREGATE ad_a(integer)(SFUNC=ad_s,STYPE=integer,INITCOND='0',FINALFUNC=ad_f)",
		"CREATE TABLE ad_input(v integer)",
		"CREATE TABLE ad_sentinel(v integer)")
	createAggregateRun(t, p, b, c, fixture.Exec("INSERT INTO ad_input VALUES(1),(2),(NULL)", 3), fixture.Exec("INSERT INTO ad_sentinel VALUES(73)", 1))
}

func aggregateDirectCallers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	aggregateDirectExec(t, p, b, c,
		"CREATE FUNCTION ad_c() RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM ad_input; END",
		"CREATE FUNCTION ad_e() RETURNS integer RETURN ad_f(ad_s(1,2))",
		"CREATE FUNCTION ad_m() RETURNS integer BEGIN ATOMIC SELECT ad_a(v)+ad_f(ad_s(1,2)) FROM ad_input; END",
		"CREATE FUNCTION ad_reverse() RETURNS integer BEGIN ATOMIC SELECT ad_f(ad_s(1,2))+ad_a(v) FROM ad_input; END",
		"CREATE FUNCTION ad_r() RETURNS integer BEGIN ATOMIC SELECT ad_a(v)+ad_a(v)+ad_f(ad_s(1,2))+ad_f(ad_s(1,2)) FROM ad_input; END",
		"CREATE FUNCTION ad_d(x integer DEFAULT ad_f(ad_s(1,2))) RETURNS integer BEGIN ATOMIC SELECT ad_a(v) FROM(VALUES(x)) t(v); END",
		"CREATE FUNCTION ad_both(x integer DEFAULT ad_f(1)) RETURNS integer RETURN ad_f(x)")
}

// Only dependent names are restricted. The referenced side is deliberately
// unrestricted: an unexpected helper, builtin or cross-schema edge is visible.
// Raw physical rows retain separate body/default recording-pass multiplicity.
func aggregateDirectBag(names []string, edges ...[2]string) fixture.Step {
	quoted := make([]string, len(names))
	for i, n := range names {
		quoted[i] = "'" + n + "'"
	}
	q := `SELECT p.proname::text AS dependent,r.proname::text AS referenced,
 d.classid::bigint AS classid,d.objsubid,d.refclassid::bigint AS refclassid,d.refobjsubid,d.deptype::text AS deptype,
 d.objid=p.oid AS owner_identity,d.refobjid=r.oid AS reference_identity
 FROM (SELECT oid,proname FROM pg_proc WHERE proname IN (` + strings.Join(quoted, ",") + `)) p
 JOIN pg_depend d ON d.objid=p.oid JOIN pg_proc r ON r.oid=d.refobjid
 WHERE d.classid=1255::oid AND d.refclassid=1255::oid AND d.deptype='n'`
	rows := make([][]any, 0, len(edges))
	for _, e := range edges {
		rows = append(rows, []any{e[0], e[1], int64(1255), int64(0), int64(1255), int64(0), "n", true, true})
	}
	s := fixture.Select(q, []string{"dependent", "referenced", "classid", "objsubid", "refclassid", "refobjsubid", "deptype", "owner_identity", "reference_identity"}, []string{"TEXT", "TEXT", "INT8", "INT4", "INT8", "INT4", "TEXT", "BOOL", "BOOL"}, rows)
	s.Bag = true
	return s
}

func aggregateDirectIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) map[string]int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT n.nspname||'.'||p.proname AS name,p.oid::bigint FROM (SELECT oid,proname,pronamespace FROM pg_proc WHERE proname LIKE 'ad_%') p JOIN pg_namespace n ON n.oid=p.pronamespace")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			t.Fatal(err)
		}
		if id <= 0 || ids[name] != 0 {
			t.Fatalf("invalid or ambiguous identity %s: %d", name, id)
		}
		ids[name] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

func aggregateDirectSameIDs(t *testing.T, p *fixture.Pair, c *sql.Conn, want map[string]int64) {
	t.Helper()
	if got := aggregateDirectIDs(t, p, c); !reflect.DeepEqual(got, want) {
		t.Fatalf("identities changed: got %v want %v", got, want)
	}
}

func aggregateDirectHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func aggregateDirectRestrict(q, target, detail string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop function " + target + " because other objects depend on it", Detail: detail, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
	s.ErrorStage = "execute"
	return s
}
