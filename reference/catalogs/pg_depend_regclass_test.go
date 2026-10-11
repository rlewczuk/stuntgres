package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Full declarations reuse admitted owner creation; ALTER ADD CHECK/generated
// and REGCLASS column storage are not prerequisites of dependency metadata.
func pgDependOwnerStatements(owner, table, expr string) []string {
	switch owner {
	case "default":
		return []string{"CREATE TABLE " + table + "(a INT,c BOOL DEFAULT (" + expr + "))"}
	case "generated":
		return []string{"CREATE TABLE " + table + "(a INT,c BOOL GENERATED ALWAYS AS(" + expr + ") STORED)"}
	case "check":
		return []string{"CREATE TABLE " + table + "(a INT,CONSTRAINT ck CHECK(" + expr + "))"}
	case "index":
		return []string{"CREATE TABLE " + table + "(a INT)", "CREATE INDEX ix ON " + table + "((" + expr + "))"}
	case "view":
		return []string{"CREATE TABLE " + table + "(a INT)", "CREATE VIEW v AS SELECT " + expr + " AS c"}
	}
	panic("unknown test owner")
}
func pgDependOwnerIdentity(t *testing.T, b fixture.Backend, owner string, table int64) (int64, int64) {
	t.Helper()
	switch owner {
	case "default", "generated":
		return 2604, fixture.PgDependDefinition(t, b, table, 2)
	case "check":
		return 2606, fixture.PgDependConstraint(t, b, table, "ck")
	case "index":
		return 1259, fixture.PgDependRelation(t, b, "ix")
	case "view":
		return 2618, fixture.PgDependRule(t, b, fixture.PgDependRelation(t, b, "v"))
	}
	panic("unknown test owner")
}
func pgDependOwnerAuto(t *testing.T, b fixture.Backend, owner string, class, id, table int64) []any {
	t.Helper()
	switch owner {
	case "default":
		return fixture.PgDependEdge(class, id, 0, 1259, table, 2, "a")
	case "generated":
		return fixture.PgDependEdge(class, id, 0, 1259, table, 2, "i")
	case "check", "index":
		return fixture.PgDependEdge(class, id, 0, 1259, table, 0, "a")
	case "view":
		return fixture.PgDependEdge(class, id, 0, 1259, fixture.PgDependRelation(t, b, "v"), 0, "i")
	}
	panic("unknown test owner")
}
func TestReferencePGDependRegclassOwners(t *testing.T) {
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		for _, probe := range []string{"named", "numeric", "numeric_const", "absent", "absent_const", "null", "oid", "array", "domain"} {
			t.Run(owner+"/"+probe, func(t *testing.T) {
				fixture.SchemaReferenceCleanup(t, "pgdep_regclass")
				p := fixture.NewScenario(t)
				fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
					fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_regclass", "SET search_path TO pgdep_regclass,public", "CREATE TABLE p(a INT)", "CREATE DOMAIN d AS REGCLASS")
					provider := fixture.PgDependRelation(t, b, "p")
					expr := "'p'::regclass"
					present := true
					switch probe {
					case "numeric":
						expr = fmt.Sprintf("%d::regclass", provider)
						present = false
					case "numeric_const":
						expr = fmt.Sprintf("'%d'::regclass", provider)
					case "absent":
						expr = "4294967295::regclass"
						present = false
					case "absent_const":
						expr = "'4294967295'::regclass"
						present = false
					case "null":
						expr = "NULL::regclass"
						present = false
					case "oid":
						expr = fmt.Sprintf("%d::oid", provider)
						present = false
					case "array":
						expr = "'{p}'::regclass[]"
						present = false
					case "domain":
						expr = "'p'::regclass::d"
					}
					statements := pgDependOwnerStatements(owner, "t", "("+expr+") IS NOT NULL")
					fixture.PgDependExec(t, p, b, statements...)
					table := fixture.PgDependRelation(t, b, "t")
					class, id := pgDependOwnerIdentity(t, b, owner, table)
					want := [][]any{pgDependOwnerAuto(t, b, owner, class, id, table)}
					if present {
						want = append(want, fixture.PgDependEdge(class, id, 0, 1259, provider, 0, "n"))
					}
					if probe == "domain" {
						domain := fixture.PgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1", fixture.PgDependNamespace(t, b, provider))
						want = append(want, fixture.PgDependEdge(class, id, 0, 1247, domain, 0, "n"))
					}
					fixture.PgDependBag(t, p, b, class, id, want...)
					if probe == "numeric" || probe == "absent" || probe == "domain" {
						spelling := "p"
						if probe == "absent" {
							spelling = "4294967295"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.RegclassRows("SELECT "+expr+" AS r", "r", spelling))
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO t(a) VALUES(7)", 1))
						switch owner {
						case "default", "generated":
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a,c FROM t", []string{"a", "c"}, []string{"INT4", "BOOL"}, [][]any{{int64(7), true}}))
						case "check", "index":
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
						case "view":
							fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT c FROM v", []string{"c"}, []string{"BOOL"}, [][]any{{true}}))
						}
					}
				})
			})
		}
	}
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		t.Run("cascade/"+owner, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "pgdep_owner")
			p := fixture.NewScenario(t)
			fixture.PgDependBackends(t, p, func(t *testing.T, b fixture.Backend) {
				fixture.PgDependExec(t, p, b, "CREATE SCHEMA pgdep_owner", "SET search_path TO pgdep_owner,public", "CREATE TABLE p(a INT)")
				provider := fixture.PgDependRelation(t, b, "p")
				fixture.PgDependExec(t, p, b, pgDependOwnerStatements(owner, "t", "'p'::regclass IS NOT NULL")...)
				table := fixture.PgDependRelation(t, b, "t")
				class, id := pgDependOwnerIdentity(t, b, owner, table)
				details := map[string]string{"default": "default value for column c of table t depends on table p", "check": "constraint ck on table t depends on table p", "generated": "column c of table t depends on table p", "index": "index ix depends on table p", "view": "view v depends on table p"}
				fixture.PgDependError(t, p, b, "DROP TABLE p", fixture.PgDependRestrict("table p", details[owner]))
				fixture.PgDependExec(t, p, b, "ALTER TABLE p RENAME TO renamed", "ALTER TABLE renamed RENAME TO p")
				fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.PgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1259 AND refobjid=$3", [][]any{fixture.PgDependEdge(class, id, 0, 1259, provider, 0, "n")}, class, id, provider))
				fixture.PgDependExec(t, p, b, "DROP TABLE p CASCADE")
				fixture.PgDependBag(t, p, b, class, id)
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{}))
				fixture.PgDependExec(t, p, b, "CREATE TABLE p(a INT)")
				if fixture.PgDependRelation(t, b, "p") == provider {
					t.Fatal("replacement reused identity")
				}
				fixture.PgDependBag(t, p, b, class, id)
			})
		})
	}
}
