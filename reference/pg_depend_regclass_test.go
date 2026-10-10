package reference_test

import (
	"fmt"
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
func pgDependOwnerIdentity(t *testing.T, b referenceBackend, owner string, table int64) (int64, int64) {
	t.Helper()
	switch owner {
	case "default", "generated":
		return 2604, pgDependDefinition(t, b, table, 2)
	case "check":
		return 2606, pgDependConstraint(t, b, table, "ck")
	case "index":
		return 1259, pgDependRelation(t, b, "ix")
	case "view":
		return 2618, pgDependRule(t, b, pgDependRelation(t, b, "v"))
	}
	panic("unknown test owner")
}
func pgDependOwnerAuto(t *testing.T, b referenceBackend, owner string, class, id, table int64) []any {
	t.Helper()
	switch owner {
	case "default":
		return pgDependEdge(class, id, 0, 1259, table, 2, "a")
	case "generated":
		return pgDependEdge(class, id, 0, 1259, table, 2, "i")
	case "check", "index":
		return pgDependEdge(class, id, 0, 1259, table, 0, "a")
	case "view":
		return pgDependEdge(class, id, 0, 1259, pgDependRelation(t, b, "v"), 0, "i")
	}
	panic("unknown test owner")
}
func TestReferencePGDependRegclassOwners(t *testing.T) {
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		for _, probe := range []string{"named", "numeric", "numeric_const", "absent", "absent_const", "null", "oid", "array", "domain"} {
			t.Run(owner+"/"+probe, func(t *testing.T) {
				schemaReferenceCleanup(t, "pgdep_regclass")
				p := newReferenceScenario(t)
				pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
					pgDependExec(t, p, b, "CREATE SCHEMA pgdep_regclass", "SET search_path TO pgdep_regclass,public", "CREATE TABLE p(a INT)", "CREATE DOMAIN d AS REGCLASS")
					provider := pgDependRelation(t, b, "p")
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
					if !b.pg && (probe == "numeric" || probe == "absent" || probe == "domain") {
						for _, q := range statements[:len(statements)-1] {
							pgDependExec(t, p, b, q)
						}
						want := controlsReferenceDiagnostic("42804", "incompatible source and destination types")
						if probe == "domain" {
							want = controlsReferenceDiagnostic("0A000", "explicit casts are supported only for int4, int8, text and bool")
							want.Position = map[string]string{"default": "52", "check": "56", "generated": "63", "index": "37", "view": "39"}[owner]
						}
						pgDependError(t, p, b, statements[len(statements)-1], want)
						return
					}
					pgDependExec(t, p, b, statements...)
					table := pgDependRelation(t, b, "t")
					class, id := pgDependOwnerIdentity(t, b, owner, table)
					want := [][]any{pgDependOwnerAuto(t, b, owner, class, id, table)}
					if present {
						want = append(want, pgDependEdge(class, id, 0, 1259, provider, 0, "n"))
					}
					if probe == "domain" {
						domain := pgDependID(t, b, "SELECT objid FROM pg_catalog.pg_depend WHERE classid=1247 AND refclassid=2615 AND refobjid=$1", pgDependNamespace(t, b, provider))
						want = append(want, pgDependEdge(class, id, 0, 1247, domain, 0, "n"))
					}
					pgDependBag(t, p, b, class, id, want...)
				})
			})
		}
	}
	for _, owner := range []string{"default", "check", "generated", "index", "view"} {
		t.Run("cascade/"+owner, func(t *testing.T) {
			schemaReferenceCleanup(t, "pgdep_owner")
			p := newReferenceScenario(t)
			pgDependBackends(t, p, func(t *testing.T, b referenceBackend) {
				pgDependExec(t, p, b, "CREATE SCHEMA pgdep_owner", "SET search_path TO pgdep_owner,public", "CREATE TABLE p(a INT)")
				provider := pgDependRelation(t, b, "p")
				pgDependExec(t, p, b, pgDependOwnerStatements(owner, "t", "'p'::regclass IS NOT NULL")...)
				table := pgDependRelation(t, b, "t")
				class, id := pgDependOwnerIdentity(t, b, owner, table)
				details := map[string]string{"default": "default value for column c of table t depends on table p", "check": "constraint ck on table t depends on table p", "generated": "column c of table t depends on table p", "index": "index ix depends on table p", "view": "view v depends on table p"}
				pgDependError(t, p, b, "DROP TABLE p", pgDependRestrict("table p", details[owner]))
				pgDependExec(t, p, b, "ALTER TABLE p RENAME TO renamed", "ALTER TABLE renamed RENAME TO p")
				pgDescriptionExecutorRows(t, p, b, b.db, pgDependSelect("SELECT * FROM pg_catalog.pg_depend WHERE classid=$1 AND objid=$2 AND refclassid=1259 AND refobjid=$3", [][]any{pgDependEdge(class, id, 0, 1259, provider, 0, "n")}, class, id, provider))
				pgDependExec(t, p, b, "DROP TABLE p CASCADE")
				pgDependBag(t, p, b, class, id)
				referenceRunStep(t, p.ctx, b.db, b.recorder, referenceSelect("SELECT a FROM t", []string{"a"}, []string{"INT4"}, [][]any{}))
				pgDependExec(t, p, b, "CREATE TABLE p(a INT)")
				if pgDependRelation(t, b, "p") == provider {
					t.Fatal("replacement reused identity")
				}
				pgDependBag(t, p, b, class, id)
			})
		})
	}
}
