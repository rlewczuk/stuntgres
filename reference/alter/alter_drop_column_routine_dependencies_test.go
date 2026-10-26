package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutinePrecision(t *testing.T) {
	for _, tc := range []struct {
		name, args, body, signature string
		attrs                       []int64
	}{
		{"body", "", "BEGIN ATOMIC SELECT a FROM dcr_t; END", "", []int64{2}},
		{"default", "x integer DEFAULT (NULL::dcr_t).a", "AS 'SELECT x'", "integer", []int64{2}},
		{"standard_default", "x integer DEFAULT (NULL::dcr_t).a", "RETURN x", "integer", []int64{2}},
		{"repeated", "x integer DEFAULT (NULL::dcr_t).a,y integer DEFAULT (NULL::dcr_t).a", "RETURN x", "integer,integer", []int64{2}},
		{"body_same", "x integer DEFAULT (NULL::dcr_t).a", "RETURN (NULL::dcr_t).a", "integer", []int64{2, 2}},
		{"body_other", "x integer DEFAULT (NULL::dcr_t).a", "RETURN (NULL::dcr_t).b", "integer", []int64{2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				dcrExec(t, p, b, c, "CREATE TABLE dcr_t(unused integer,a integer,b integer,c integer)",
					"CREATE FUNCTION dcr_f("+tc.args+") RETURNS integer LANGUAGE SQL "+tc.body,
					"CREATE FUNCTION dcr_soft() RETURNS integer LANGUAGE SQL AS 'SELECT a FROM dcr_t'",
					"CREATE FUNCTION dcr_keep(x integer DEFAULT (NULL::dcr_t).b) RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT b+COALESCE(x,0) FROM dcr_t; END")
				dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_t VALUES(0,7,8,9)", 1))
				dcrAttributeEdges(t, p, b, c, "dcr_f("+tc.signature+")", "dcr_t", tc.attrs...)
				dcrAttributeEdges(t, p, b, c, "dcr_soft()", "dcr_t")
				dcrAttributeEdges(t, p, b, c, "dcr_keep(integer)", "dcr_t", 3, 3)
				const keepSQL = "SELECT dcr_keep() AS v"
				stmt, err := c.PrepareContext(p.Ctx, keepSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: keepSQL}
				dcrValue(t, p, b, held, keepSQL, int64(8))
				if tc.name != "body" {
					dcrValue(t, p, b, c, "SELECT dcr_f() AS v", nil)
					if tc.name == "default" || tc.name == "standard_default" {
						dcrValue(t, p, b, c, "SELECT dcr_f(17) AS v", int64(17))
					}
				}
				dcrExec(t, p, b, c, "ALTER TABLE dcr_t DROP unused", "ALTER TABLE dcr_t DROP c")
				dcrNotices(t, notices)
				dcrAttributeEdges(t, p, b, c, "dcr_f("+tc.signature+")", "dcr_t", tc.attrs...)
				dcrRun(t, p, b, c, fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_t'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"a", int64(2)}, {"b", int64(3)}}))
				dcrValue(t, p, b, c, "SELECT dcr_keep() AS v", int64(8))
				dcrValue(t, p, b, held, keepSQL, int64(8))
				if tc.name == "body" {
					dcrValue(t, p, b, c, "SELECT dcr_f() AS v", int64(7))
				} else {
					dcrValue(t, p, b, c, "SELECT dcr_f() AS v", nil)
				}
				want := dcrRestrict("dcr_t", "a", "function dcr_f("+tc.signature+") depends on column a of table dcr_t")
				dcrError(t, p, b, c, "ALTER TABLE dcr_t DROP a RESTRICT", want, false)
				dcrExec(t, p, b, c, "ALTER TABLE dcr_t DROP a CASCADE")
				dcrNotices(t, notices, "function dcr_f("+tc.signature+")")
				dcrInventory(t, p, b, c, "dcr_keep", "dcr_soft")
				dcrValue(t, p, b, c, "SELECT dcr_keep() AS v", int64(8))
				for range 2 {
					dcrValue(t, p, b, held, keepSQL, int64(8))
				}
				dcrValue(t, p, b, c, "SELECT dcr_keep(17) AS v", int64(25))
				dcrAttributeEdges(t, p, b, c, "dcr_keep(integer)", "dcr_t", 3, 3)
				dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_t", []string{"b"}, []string{"INT4"}, [][]any{{int64(8)}}))
			})
		})
	}
}

func dcrCascadeSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	dcrSetup(t, p, b, c)
	dcrExec(t, p, b, c,
		"CREATE FUNCTION dcr_default(x integer DEFAULT (NULL::dcr_t).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
		"CREATE FUNCTION dcr_both(x integer DEFAULT (NULL::dcr_t).a) RETURNS integer LANGUAGE SQL RETURN (NULL::dcr_t).a",
		"CREATE FUNCTION dcr_caller() RETURNS integer LANGUAGE SQL RETURN dcr_body()",
		"CREATE FUNCTION dcr_shared() RETURNS integer LANGUAGE SQL RETURN dcr_body()+dcr_caller()",
		"CREATE VIEW dcr_view AS SELECT dcr_shared() AS v",
		"CREATE FUNCTION dcr_keep(x integer DEFAULT (NULL::dcr_t).b) RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT b+COALESCE(x,0) FROM dcr_t; END",
		"CREATE FUNCTION dcr_unrelated() RETURNS integer LANGUAGE SQL RETURN 19",
		"CREATE FUNCTION dcr_soft() RETURNS integer LANGUAGE SQL AS 'SELECT a FROM dcr_t'")
}

func dcrCascadeObjects() []string {
	// Independently reasoned traversal order; phase-B oracle must confirm it.
	return []string{"function dcr_body()", "function dcr_caller()", "function dcr_shared()", "view dcr_view", "function dcr_default(integer)", "function dcr_both(integer)"}
}

func dcrRetiredIDs(t *testing.T, p *fixture.Pair, c *sql.Conn) []int64 {
	t.Helper()
	rows := dcrRows(t, p, c, "SELECT oid::bigint FROM pg_proc WHERE pronamespace='public'::regnamespace AND proname IN ('dcr_body','dcr_default','dcr_both','dcr_caller','dcr_shared') UNION ALL SELECT oid::bigint FROM pg_class WHERE oid='dcr_view'::regclass")
	var ids []int64
	for _, row := range rows {
		ids = append(ids, row[0].(int64))
	}
	return ids
}

func dcrGoneEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, ids []int64) {
	t.Helper()
	for _, id := range ids {
		q := fmt.Sprintf("SELECT count(*) AS n FROM pg_depend WHERE objid=%d OR refobjid=%d", id, id)
		dcrRun(t, p, b, x, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

func dcrCascadeSurvivors(t *testing.T, p *fixture.Pair, b fixture.Backend, x fixture.Executor, ids []int64) {
	t.Helper()
	dcrInventory(t, p, b, x, "dcr_keep", "dcr_soft", "dcr_unrelated")
	dcrRun(t, p, b, x, fixture.Select("SELECT * FROM dcr_t", []string{"b", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(8), int64(9)}}))
	dcrRun(t, p, b, x, fixture.Select("SELECT relname FROM pg_class WHERE relnamespace='public'::regnamespace AND relname='dcr_view'", []string{"relname"}, []string{"NAME"}, nil))
	dcrValue(t, p, b, x, "SELECT dcr_keep() AS v", int64(8))
	dcrValue(t, p, b, x, "SELECT dcr_keep(17) AS v", int64(25))
	dcrValue(t, p, b, x, "SELECT dcr_unrelated() AS v", int64(19))
	dcrAttributeEdges(t, p, b, x, "dcr_keep(integer)", "dcr_t", 2, 2)
	dcrGoneEdges(t, p, b, x, ids)
}

func TestReferenceAlterDropColumnRoutineCascade(t *testing.T) {
	dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		dcrCascadeSetup(t, p, b, c)
		ids := dcrRetiredIDs(t, p, c)
		dcrAttributeEdges(t, p, b, c, "dcr_both(integer)", "dcr_t", 1, 1)
		dcrValue(t, p, b, c, "SELECT dcr_shared() AS v", int64(14))
		dcrExec(t, p, b, c, "ALTER TABLE dcr_t DROP a CASCADE")
		dcrNotices(t, notices, dcrCascadeObjects()...)
		dcrCascadeSurvivors(t, p, b, c, ids)
	})
}
