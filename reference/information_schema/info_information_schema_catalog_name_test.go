package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoInformationSchemaCatalogNameRows(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		for _, projection := range []string{"catalog_name", "*"} {
			s := iscnStep(projection, "", true)
			held := iscnHold(t, p, c, s.Query)
			iscnRoutes(t, p, c, r, held, s)
		}
		q := "SELECT catalog_name,current_database(),current_catalog FROM " + iscnRelation
		s := fixture.Select(q, []string{"catalog_name", "current_database", "current_catalog"}, []string{"NAME", "NAME", "NAME"}, [][]any{{iscnDatabase, iscnDatabase, iscnDatabase}})
		iscnRoutes(t, p, c, r, iscnHold(t, p, c, q), s)
		for _, q := range []string{"CREATE ROLE iscn_reader", "SET search_path=pg_catalog", "SET SESSION AUTHORIZATION iscn_reader"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		s = iscnStep("catalog_name", "", true)
		iscnRoutes(t, p, c, r, iscnHold(t, p, c, s.Query), s)
	})
}

func TestReferenceInfoInformationSchemaCatalogNameBoundaries(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		for _, projection := range []string{"catalog_name", "*"} {
			for _, predicate := range []string{"FALSE", "catalog_name IS NULL", "catalog_name='iscn_absent'", "NULL::boolean", "catalog_name IS NOT NULL"} {
				s := iscnStep(projection, predicate, predicate == "catalog_name IS NOT NULL")
				iscnRoutes(t, p, c, r, iscnHold(t, p, c, s.Query), s)
			}
			held := iscnHold(t, p, c, iscnStep(projection, "catalog_name=$1", false).Query)
			for _, arg := range []any{iscnDatabase, "iscn_absent", nil, iscnDatabase} {
				s := iscnStep(projection, "catalog_name=$1", arg == iscnDatabase, arg)
				iscnRoutes(t, p, c, r, held, s)
			}
		}
	})
}

func TestReferenceInfoInformationSchemaCatalogNameShape(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path=public", 0))
		steps := []fixture.Step{
			fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.information_schema_catalog_name'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, [][]any{{int64(1), "catalog_name", "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false}}),
			fixture.Select(`SELECT c.relkind,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.information_schema_catalog_name'::regclass`, []string{"relkind", "same_type", "same_relation", "array_element", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true, true}}),
			fixture.Select("SELECT pg_typeof(catalog_name)='information_schema.sql_identifier'::regtype AS same_type FROM "+iscnRelation, []string{"same_type"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT column_name,ordinal_position,is_nullable,domain_schema,domain_name,udt_schema,udt_name FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='information_schema_catalog_name' ORDER BY ordinal_position`, []string{"column_name", "ordinal_position", "is_nullable", "domain_schema", "domain_name", "udt_schema", "udt_name"}, []string{"NAME", "INT4", "VARCHAR", "NAME", "NAME", "NAME", "NAME"}, [][]any{{"catalog_name", int64(1), "YES", "information_schema", "sql_identifier", "pg_catalog", "name"}}),
		}
		// Keep numeric identity expectations and OID column metadata while the
		// shared helper checks pq's distinct raw OID encoding before normalizing.
		b.Recorder = r
		for _, s := range steps {
			held := iscnHold(t, p, c, s.Query)
			fixture.PgDescriptionExecutorRows(t, p, b, c, s)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(s))
			fixture.PgDescriptionExecutorRows(t, p, b, held, s)
		}
	})
}

func iscnDiagnostics(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder) {
	t.Helper()
	for _, x := range []struct {
		query, state, message string
		position              int
	}{
		{"SELECT missing FROM " + iscnRelation, "42703", `column "missing" does not exist`, 8},
		{"SELECT c.missing FROM " + iscnRelation + " AS c", "42703", "column c.missing does not exist", 8},
		{"SELECT catalog_name FROM information_schema.iscn_missing", "42P01", `relation "information_schema.iscn_missing" does not exist`, 26},
		{"SELECT /*é*/ missing FROM " + iscnRelation, "42703", `column "missing" does not exist`, 14},
	} {
		d := fixture.ControlsReferenceDiagnostic(x.state, x.message)
		d.Position = fixture.Position(x.position)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(x.query, d)
			s.SelectRows, s.Prepared = true, prepared
			s.ErrorStage = "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, r, s)
		}
	}
}

func TestReferenceInfoInformationSchemaCatalogNameDiagnostics(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		iscnDiagnostics(t, p, c, r)
	})
}
