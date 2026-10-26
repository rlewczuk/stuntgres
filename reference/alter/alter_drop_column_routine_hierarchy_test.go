package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterDropColumnRoutineComposite(t *testing.T) {
	dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
		dcrExec(t, p, b, c, "CREATE TYPE dcr_composite AS (a integer,b integer)",
			"CREATE FUNCTION dcr_default(x integer DEFAULT (NULL::dcr_composite).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
			"CREATE FUNCTION dcr_keep(x integer DEFAULT (NULL::dcr_composite).b) RETURNS integer LANGUAGE SQL RETURN x")
		dcrAttributeEdges(t, p, b, c, "dcr_default(integer)", "dcr_composite", 1)
		dcrValue(t, p, b, c, "SELECT dcr_default() AS v", nil)
		want := dcrRestrict("dcr_composite", "a", "function dcr_default(integer) depends on column a of composite type dcr_composite")
		want.Message = "cannot drop column a of composite type dcr_composite because other objects depend on it"
		dcrError(t, p, b, c, "ALTER TYPE dcr_composite DROP ATTRIBUTE a RESTRICT", want, false)
		dcrExec(t, p, b, c, "ALTER TYPE dcr_composite DROP ATTRIBUTE a CASCADE")
		dcrNotices(t, notices, "function dcr_default(integer)")
		dcrInventory(t, p, b, c, "dcr_keep")
		dcrAttributeEdges(t, p, b, c, "dcr_keep(integer)", "dcr_composite", 2)
		dcrRun(t, p, b, c, fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='dcr_composite'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, [][]any{{"b", int64(2)}}))
		dcrValue(t, p, b, c, "SELECT dcr_keep(17) AS v", int64(17))

		// One complete preflight must report both a separately removable CHECK
		// and a routine; neither the legacy composite pass nor a routine pass
		// may short-circuit the other or report a duplicate cascade group.
		dcrExec(t, p, b, c,
			"CREATE TYPE dcr_mixed AS (a integer,b integer)",
			"CREATE TABLE dcr_checked(v dcr_mixed,CONSTRAINT dcr_field CHECK((v).a>0),CONSTRAINT dcr_keep_check CHECK((v).b>0))",
			"CREATE FUNCTION dcr_mixed_use(x integer DEFAULT (NULL::dcr_mixed).a) RETURNS integer LANGUAGE SQL RETURN x")
		dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_checked VALUES('(7,8)')", 1))
		dcrAttributeEdges(t, p, b, c, "dcr_mixed_use(integer)", "dcr_mixed", 1)
		mixed := dcrRestrict("dcr_mixed", "a",
			"constraint dcr_field on table dcr_checked depends on column a of composite type dcr_mixed\nfunction dcr_mixed_use(integer) depends on column a of composite type dcr_mixed")
		mixed.Message = "cannot drop column a of composite type dcr_mixed because other objects depend on it"
		for _, held := range []bool{false, true} {
			dcrError(t, p, b, c, "ALTER TYPE dcr_mixed DROP ATTRIBUTE a RESTRICT", mixed, held)
			dcrNotices(t, notices)
			dcrValue(t, p, b, c, "SELECT dcr_mixed_use(17) AS v", int64(17))
			dcrRun(t, p, b, c, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='dcr_checked'::regclass ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"dcr_field"}, {"dcr_keep_check"}}))
		}
		dcrExec(t, p, b, c, "ALTER TYPE dcr_mixed DROP ATTRIBUTE a CASCADE")
		dcrNotices(t, notices, "constraint dcr_field on table dcr_checked", "function dcr_mixed_use(integer)")
		dcrInventory(t, p, b, c, "dcr_keep")
		dcrRun(t, p, b, c, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='dcr_checked'::regclass ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"dcr_keep_check"}}))
		dcrRun(t, p, b, c, fixture.Select("SELECT (v).b AS b FROM dcr_checked", []string{"b"}, []string{"INT4"}, [][]any{{int64(8)}}))
	})
}

func TestReferenceAlterDropColumnRoutineInheritance(t *testing.T) {
	for _, mode := range []string{"recursive", "only", "local", "multiple"} {
		t.Run(mode, func(t *testing.T) {
			dcrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, notices *[]fixture.Diagnostic) {
				dcrExec(t, p, b, c, "CREATE TABLE dcr_parent(a integer,b integer)")
				child := "CREATE TABLE dcr_child() INHERITS(dcr_parent)"
				if mode == "local" {
					child = "CREATE TABLE dcr_child(a integer) INHERITS(dcr_parent)"
				}
				if mode == "multiple" {
					dcrExec(t, p, b, c, "CREATE TABLE dcr_parent2(a integer)")
					child = "CREATE TABLE dcr_child() INHERITS(dcr_parent,dcr_parent2)"
				}
				dcrExec(t, p, b, c, child, "CREATE FUNCTION dcr_child_use() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT a FROM ONLY dcr_child; END")
				// Inheritance merging emits setup notices, separate from retirement.
				if mode == "local" || mode == "multiple" {
					message := `merging column "a" with inherited definition`
					if mode == "multiple" {
						message = `merging multiple inherited definitions of column "a"`
					}
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
					if len(*notices) != 1 || (*notices)[0] != want {
						t.Errorf("inheritance setup notices = %#v; want %#v", *notices, want)
					}
					*notices = nil
				} else {
					dcrNotices(t, notices)
				}
				dcrRun(t, p, b, c, fixture.Exec("INSERT INTO dcr_child VALUES(7,8)", 1))
				dcrAttributeEdges(t, p, b, c, "dcr_child_use()", "dcr_child", 1)
				q := "ALTER TABLE dcr_parent DROP a"
				if mode == "only" {
					q = "ALTER TABLE ONLY dcr_parent DROP a"
				}
				if mode == "recursive" {
					want := dcrRestrict("dcr_parent", "a", "function dcr_child_use() depends on column a of table dcr_child")
					want.Message = "cannot drop desired object(s) because other objects depend on them"
					dcrError(t, p, b, c, q+" RESTRICT", want, false)
					dcrValue(t, p, b, c, "SELECT dcr_child_use() AS v", int64(7))
					dcrExec(t, p, b, c, q+" CASCADE")
					dcrNotices(t, notices, "function dcr_child_use()")
					dcrInventory(t, p, b, c)
					dcrRun(t, p, b, c, fixture.Select("SELECT * FROM dcr_child", []string{"b"}, []string{"INT4"}, [][]any{{int64(8)}}))
				} else {
					dcrExec(t, p, b, c, q+" RESTRICT")
					dcrNotices(t, notices)
					dcrValue(t, p, b, c, "SELECT dcr_child_use() AS v", int64(7))
					dcrAttributeEdges(t, p, b, c, "dcr_child_use()", "dcr_child", 1)
					local, count := true, int64(0)
					if mode == "multiple" {
						local, count = false, 1
					}
					dcrRun(t, p, b, c, fixture.Select("SELECT attnum,attislocal,attinhcount FROM pg_attribute WHERE attrelid='dcr_child'::regclass AND attname='a'", []string{"attnum", "attislocal", "attinhcount"}, []string{"INT2", "BOOL", "INT2"}, [][]any{{int64(1), local, count}}))
					if mode == "multiple" {
						want := dcrRestrict("dcr_parent2", "a", "function dcr_child_use() depends on column a of table dcr_child")
						want.Message = "cannot drop desired object(s) because other objects depend on them"
						dcrError(t, p, b, c, "ALTER TABLE dcr_parent2 DROP a RESTRICT", want, false)
					}
				}
			})
		})
	}
}
