package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceCompositeDependencies(t *testing.T) {
	for _, kind := range []string{"table", "view", "named", "domain"} {
		t.Run(kind, func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, _ *sql.Conn) {
				var notices []referenceDiagnostic
				_, c, r := noticeReferencePool(t, p, b.pg, &notices)
				b.recorder = r
				compositeReferenceSetup(t, p, b, c, `CREATE TABLE base(id integer,note text)`)
				source, drop, typeName := "base", "DROP TABLE base", "base"
				if kind == "view" {
					source = "source"
					drop = "DROP VIEW source"
					typeName = "source"
					compositeReferenceSetup(t, p, b, c, `CREATE VIEW source AS SELECT id,note FROM base`)
				}
				detail := ""
				initial := "(7,x)"
				if kind == "named" {
					compositeReferenceSetup(t, p, b, c, `CREATE TYPE wrapped AS (r base)`)
					typeName = "wrapped"
					initial = `("(7,x)")`
					detail = "column r of composite type wrapped depends on type base"
				} else if kind == "domain" {
					compositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS base`)
					typeName = "wrapped"
					detail = "type wrapped depends on type base\ncolumn v of table holder depends on type wrapped"
				} else {
					detail = "column v of table holder depends on type " + source
				}
				compositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(k integer,v "+typeName+")")
				referenceRunStep(t, p.ctx, c, r, referenceExec(`INSERT INTO holder VALUES (1,$1)`, 1, initial))
				want := compositeReferenceDiagnostic("2BP01", "cannot drop "+map[bool]string{true: "view", false: "table"}[kind == "view"]+" "+source+" because other objects depend on it")
				want.Detail = detail
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				compositeReferenceError(t, p, c, drop+" RESTRICT", "execute", want)
				compositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{initial}}, false)
				notices = nil
				referenceRunStep(t, p.ctx, c, r, referenceExec(drop+" CASCADE", 0))
				message, cascade := "drop cascades to column v of table holder", ""
				if kind == "domain" {
					message = "drop cascades to 2 other objects"
					cascade = "drop cascades to type wrapped\ndrop cascades to column v of table holder"
				}
				if kind == "named" {
					message = "drop cascades to column r of composite type wrapped"
				}
				domainReferenceNotice(t, &notices, message, cascade)
				referenceRunStep(t, p.ctx, c, r, referenceSelect(`SELECT k FROM holder`, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if kind == "named" {
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"()"}}, false)
					referenceRunStep(t, p.ctx, c, r, referencePrepared(referenceExec(`INSERT INTO holder VALUES (2,$1)`, 1, "()")))
					wantInput := compositeReferenceDiagnostic("22P02", `malformed record literal: "(old)"`)
					wantInput.Detail = "Too many columns."
					wantInput.Context = `unnamed portal parameter $1 = '...'`
					compositeReferenceError(t, p, c, `INSERT INTO holder VALUES (3,$1)`, "execute", wantInput, "(old)")
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"()"}, {"()"}}, true)
				}
			})
		})
	}
}

func TestReferenceCompositeCatalogRelationships(t *testing.T) {
	compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		compositeReferenceSetup(t, p, b, c, `CREATE DOMAIN member_integer AS integer`, `CREATE DOMAIN member_text AS text`, `CREATE TYPE catalog_record AS (i member_integer,t member_text COLLATE "C")`)
		relation := pgDependRelation(t, b, "catalog_record")
		namespace := pgDependNamespace(t, b, relation)
		rowType := pgDependID(t, b, "SELECT 'catalog_record'::regtype::oid")
		arrayType := pgDependID(t, b, "SELECT 'catalog_record[]'::regtype::oid")
		memberInteger := pgDependID(t, b, "SELECT 'member_integer'::regtype::oid")
		memberText := pgDependID(t, b, "SELECT 'member_text'::regtype::oid")
		pgDependBag(t, p, b, 1259, relation, pgDependEdge(1259, relation, 0, 1247, rowType, 0, "i"), pgDependEdge(1259, relation, 1, 1247, memberInteger, 0, "n"), pgDependEdge(1259, relation, 2, 1247, memberText, 0, "n"))
		pgDependBag(t, p, b, 1247, rowType, pgDependEdge(1247, rowType, 0, 2615, namespace, 0, "n"))
		pgDependBag(t, p, b, 1247, arrayType, pgDependEdge(1247, arrayType, 0, 1247, rowType, 0, "i"))
		// pg_type is release-only observation, not a new native catalog source.
		if b.pg {
			pgDescriptionExecutorRows(t, p, b, c, referenceSelect(`SELECT c.relkind,t.typtype,t.typrelid=c.oid AS reciprocal,a.typelem=t.oid AS element FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.relname='catalog_record'`, []string{"relkind", "typtype", "reciprocal", "element"}, []string{"CHAR", "CHAR", "BOOL", "BOOL"}, [][]any{{"c", "c", true, true}}))
		}
	})
}

func TestReferenceCompositeSchemaDependencies(t *testing.T) {
	for _, cascade := range []bool{false, true} {
		t.Run(fmt.Sprint(cascade), func(t *testing.T) {
			compositeReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
				compositeReferenceSetup(t, p, b, c, `CREATE SCHEMA composite_schema`, `CREATE TYPE composite_schema.named AS (i integer)`, `CREATE TABLE holder(k integer,v composite_schema.named)`)
				defer c.ExecContext(p.ctx, `DROP SCHEMA IF EXISTS composite_schema CASCADE`)
				referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`INSERT INTO holder VALUES (1,'(7)')`, 1))
				if cascade {
					referenceRunStep(t, p.ctx, c, b.recorder, referenceExec(`DROP SCHEMA composite_schema CASCADE`, 0))
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect(`SELECT k FROM holder`, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				} else {
					want := compositeReferenceDiagnostic("2BP01", `cannot drop schema composite_schema because other objects depend on it`)
					want.Detail = "type composite_schema.named depends on schema composite_schema\ncolumn v of table holder depends on type composite_schema.named"
					want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					compositeReferenceError(t, p, c, `DROP SCHEMA composite_schema RESTRICT`, "execute", want)
					compositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{"(7)"}}, false)
				}
			})
		})
	}
}
