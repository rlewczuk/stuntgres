package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompositeDependencies(t *testing.T) {
	for _, kind := range []string{"table", "view", "named", "domain"} {
		t.Run(kind, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
				var notices []fixture.Diagnostic
				_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
				b.Recorder = r
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TABLE base(id integer,note text)`)
				source, drop, typeName := "base", "DROP TABLE base", "base"
				if kind == "view" {
					source = "source"
					drop = "DROP VIEW source"
					typeName = "source"
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE VIEW source AS SELECT id,note FROM base`)
				}
				detail := ""
				initial := "(7,x)"
				if kind == "named" {
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE wrapped AS (r base)`)
					typeName = "wrapped"
					initial = `("(7,x)")`
					detail = "column r of composite type wrapped depends on type base"
				} else if kind == "domain" {
					fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS base`)
					typeName = "wrapped"
					detail = "type wrapped depends on type base\ncolumn v of table holder depends on type wrapped"
				} else {
					detail = "column v of table holder depends on type " + source
				}
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(k integer,v "+typeName+")")
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(`INSERT INTO holder VALUES (1,$1)`, 1, initial))
				want := fixture.CompositeReferenceDiagnostic("2BP01", "cannot drop "+map[bool]string{true: "view", false: "table"}[kind == "view"]+" "+source+" because other objects depend on it")
				want.Detail = detail
				want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
				fixture.CompositeReferenceError(t, p, c, drop+" RESTRICT", "execute", want)
				fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{initial}}, false)
				notices = nil
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(drop+" CASCADE", 0))
				message, cascade := "drop cascades to column v of table holder", ""
				if kind == "domain" {
					message = "drop cascades to 2 other objects"
					cascade = "drop cascades to type wrapped\ndrop cascades to column v of table holder"
				}
				if kind == "named" {
					message = "drop cascades to column r of composite type wrapped"
				}
				fixture.DomainReferenceNotice(t, &notices, message, cascade)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select(`SELECT k FROM holder`, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				if kind == "named" {
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"()"}}, false)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(fixture.Exec(`INSERT INTO holder VALUES (2,$1)`, 1, "()")))
					wantInput := fixture.CompositeReferenceDiagnostic("22P02", `malformed record literal: "(old)"`)
					wantInput.Detail = "Too many columns."
					wantInput.Context = `unnamed portal parameter $1 = '...'`
					fixture.CompositeReferenceError(t, p, c, `INSERT INTO holder VALUES (3,$1)`, "execute", wantInput, "(old)")
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder ORDER BY k`, [][]any{{"()"}, {"()"}}, true)
				}
			})
		})
	}
}

func TestReferenceCompositeCatalogRelationships(t *testing.T) {
	fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN member_integer AS integer`, `CREATE DOMAIN member_text AS text`, `CREATE TYPE catalog_record AS (i member_integer,t member_text COLLATE "C")`)
		relation := fixture.PgDependRelation(t, b, "catalog_record")
		namespace := fixture.PgDependNamespace(t, b, relation)
		rowType := fixture.PgDependID(t, b, "SELECT 'catalog_record'::regtype::oid")
		arrayType := fixture.PgDependID(t, b, "SELECT 'catalog_record[]'::regtype::oid")
		memberInteger := fixture.PgDependID(t, b, "SELECT 'member_integer'::regtype::oid")
		memberText := fixture.PgDependID(t, b, "SELECT 'member_text'::regtype::oid")
		fixture.PgDependBag(t, p, b, 1259, relation, fixture.PgDependEdge(1259, relation, 0, 1247, rowType, 0, "i"), fixture.PgDependEdge(1259, relation, 1, 1247, memberInteger, 0, "n"), fixture.PgDependEdge(1259, relation, 2, 1247, memberText, 0, "n"))
		fixture.PgDependBag(t, p, b, 1247, rowType, fixture.PgDependEdge(1247, rowType, 0, 2615, namespace, 0, "n"))
		fixture.PgDependBag(t, p, b, 1247, arrayType, fixture.PgDependEdge(1247, arrayType, 0, 1247, rowType, 0, "i"))
		fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Select(`SELECT c.relkind,t.typtype,t.typrelid=c.oid AS reciprocal,a.typelem=t.oid AS element FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.relname='catalog_record'`, []string{"relkind", "typtype", "reciprocal", "element"}, []string{"CHAR", "CHAR", "BOOL", "BOOL"}, [][]any{{"c", "c", true, true}}))
	})
}

func TestReferenceCompositeSchemaDependencies(t *testing.T) {
	for _, cascade := range []bool{false, true} {
		t.Run(fmt.Sprint(cascade), func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE SCHEMA composite_schema`, `CREATE TYPE composite_schema.named AS (i integer)`, `CREATE TABLE holder(k integer,v composite_schema.named)`)
				defer c.ExecContext(p.Ctx, `DROP SCHEMA IF EXISTS composite_schema CASCADE`)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES (1,'(7)')`, 1))
				if cascade {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`DROP SCHEMA composite_schema CASCADE`, 0))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT k FROM holder`, []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
				} else {
					want := fixture.CompositeReferenceDiagnostic("2BP01", `cannot drop schema composite_schema because other objects depend on it`)
					want.Detail = "type composite_schema.named depends on schema composite_schema\ncolumn v of table holder depends on type composite_schema.named"
					want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					fixture.CompositeReferenceError(t, p, c, `DROP SCHEMA composite_schema RESTRICT`, "execute", want)
					fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, [][]any{{"(7)"}}, false)
				}
			})
		})
	}
}
