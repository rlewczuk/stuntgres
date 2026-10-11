package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTypedTableNotices(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		var notices []fixture.Diagnostic
		_, noticeConn, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nrun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, noticeConn, recorder, s) }
		nrun(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		nrun(fixture.Exec("CREATE TABLE t OF pair", 0))
		notices = nil
		nrun(fixture.Exec("CREATE TABLE IF NOT EXISTS t OF missing", 0))
		fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}})
		notices = nil
		nrun(fixture.Exec("DROP TYPE pair CASCADE", 0))
		fixture.DomainReferenceNotice(t, &notices, "drop cascades to table t", "")
		notices = nil
		nrun(fixture.Exec("DROP TYPE IF EXISTS missing", 0))
		fixture.RegtypeAssertNotices(t, notices, []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `type "missing" does not exist, skipping`}})
		nrun(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		nrun(fixture.Exec("CREATE TABLE a OF pair", 0))
		nrun(fixture.Exec("CREATE TABLE z OF pair", 0))
		d := typedTableError("2BP01", "cannot drop type pair because other objects depend on it")
		d.Detail, d.Hint = "table a depends on type pair\ntable z depends on type pair", "Use DROP ... CASCADE to drop the dependent objects too."
		fixture.CompositeReferenceError(t, p, noticeConn, "DROP TYPE pair RESTRICT", "execute", d)
		notices = nil
		nrun(fixture.Exec("DROP TYPE pair CASCADE", 0))
		fixture.DomainReferenceNotice(t, &notices, "drop cascades to 2 other objects", "drop cascades to table a\ndrop cascades to table z")
	})
}

func TestReferenceTypedTableDependencyIdentity(t *testing.T) {
	typedTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE pair AS (id integer)", 0))
		run(fixture.Exec("CREATE TABLE t OF pair", 0))
		// The fixed pg_type catalog class is1247; it is not a native queryable
		// relation. The selected NORMAL edge and distinct own-row identity are
		// shared, while PostgreSQL's full reciprocal catalog bag supplements them.
		run(fixture.Select("SELECT deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='t'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjid='pair'::regtype::oid AND refobjsubid=0", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
		run(fixture.Select("SELECT 't'::regtype::oid<>'pair'::regtype::oid AS distinct_row", []string{"distinct_row"}, []string{"BOOL"}, [][]any{{true}}))
		if !b.PG {
			return
		}
		// Complete selected type/relation edge bag. Namespace and ordinary scalar
		// column dependencies are outside this explicitly selected address set.
		q := "SELECT classid='pg_class'::regclass::oid AS relation_source,(classid='pg_class'::regclass::oid AND objid='t'::regclass::oid) AS table_source,refclassid='pg_type'::regclass::oid AS type_target,(refclassid='pg_type'::regclass::oid AND refobjid='pair'::regtype::oid) AS pair_target,deptype FROM pg_depend WHERE objsubid=0 AND refobjsubid=0 AND ((classid='pg_class'::regclass::oid AND (objid='t'::regclass::oid OR objid='pair'::regclass::oid) AND refclassid='pg_type'::regclass::oid) OR (classid='pg_type'::regclass::oid AND (objid='t'::regtype::oid OR objid='pair[]'::regtype::oid OR objid='t[]'::regtype::oid) AND (refclassid='pg_class'::regclass::oid OR refclassid='pg_type'::regclass::oid)))"
		s := fixture.Select(q, []string{"relation_source", "table_source", "type_target", "pair_target", "deptype"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "CHAR"}, [][]any{{true, true, true, true, "n"}, {true, false, true, true, "i"}, {false, false, false, false, "i"}, {false, false, true, true, "i"}, {false, false, true, false, "i"}})
		s.Bag = true
		run(s)
	})
}
