package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUserOperatorCatalogRows(t *testing.T) {
	userOperatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE OPERATOR public.#~# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4pl)", "CREATE OPERATOR public.##~ (RIGHTARG=text,FUNCTION=lower)", "CREATE OPERATOR public.#=# (LEFTARG=int4,RIGHTARG=int4,FUNCTION=int4eq,COMMUTATOR=OPERATOR(public.#=#),NEGATOR=OPERATOR(public.#!#),RESTRICT=eqsel,JOIN=eqjoinsel,HASHES,MERGES)")
		ids := make(map[string]int64)
		for _, signature := range []string{"#~#(int4,int4)", "##~(NONE,text)", "#=#(int4,int4)", "#!#(int4,int4)"} {
			var id int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regoperator::oid", "public."+signature).Scan(&id); err != nil || id == 0 {
				t.Fatal("allocated exact identity", signature, id, err)
			}
			ids[signature] = id
		}
		var owner int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=current_user").Scan(&owner); err != nil || owner == 0 {
			t.Fatal("live creator identity", owner, err)
		}
		var namespace int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil || namespace == 0 {
			t.Fatal("live public namespace identity", namespace, err)
		}
		columns := []string{"oid", "oprname", "oprnamespace", "oprowner", "oprkind", "oprcanmerge", "oprcanhash", "oprleft", "oprright", "oprresult", "oprcom", "oprnegate", "oprcode", "oprrest", "oprjoin"}
		types := []string{"OID", "NAME", "OID", "OID", "CHAR", "BOOL", "BOOL", "OID", "OID", "OID", "OID", "OID", "OID", "OID", "OID"}
		rows := [][]any{
			{ids["#!#(int4,int4)"], "#!#", namespace, owner, "b", false, false, int64(23), int64(23), int64(0), int64(0), ids["#=#(int4,int4)"], int64(0), int64(0), int64(0)},
			{ids["##~(NONE,text)"], "##~", namespace, owner, "l", false, false, int64(0), int64(25), int64(25), int64(0), int64(0), int64(870), int64(0), int64(0)},
			{ids["#=#(int4,int4)"], "#=#", namespace, owner, "b", true, true, int64(23), int64(23), int64(16), ids["#=#(int4,int4)"], ids["#!#(int4,int4)"], int64(65), int64(101), int64(105)},
			{ids["#~#(int4,int4)"], "#~#", namespace, owner, "b", false, false, int64(23), int64(23), int64(23), int64(0), int64(0), int64(177), int64(0), int64(0)},
		}
		query := "SELECT oid,oprname,oprnamespace,oprowner,oprkind,oprcanmerge,oprcanhash,oprleft,oprright,oprresult,oprcom,oprnegate,oprcode::oid AS oprcode,oprrest::oid AS oprrest,oprjoin::oid AS oprjoin FROM pg_operator WHERE oprnamespace='public'::regnamespace AND oprname IN ('#!#','##~','#=#','#~#') ORDER BY oprname"
		fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Select(query, columns, types, rows))
		fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(fixture.Select(query, columns, types, rows)))
		// Identity-only estimator metadata does not fabricate native pg_proc rows.
		routineRows := int64(0)
		if b.PG {
			routineRows = 2
		}
		userOperatorValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE oid=101 OR oid=105", "INT8", routineRows)
	})
}
