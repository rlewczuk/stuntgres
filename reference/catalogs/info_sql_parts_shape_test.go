package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func spCatalogShape(c *spCase) {
	c.t.Helper()
	var attributes, declared [][]any
	for i, name := range spColumns {
		domain, typmod := "character_data", int64(-1)
		var maximum any
		if i == 2 {
			domain, typmod, maximum = "yes_or_no", 7, int64(3)
		}
		attributes = append(attributes, []any{int64(i + 1), name, "information_schema", domain, "d", int64(1043), int64(-1), false, int64(950), int64(0), false, typmod, false})
		declared = append(declared, []any{name, int64(i + 1), "YES", "character varying", maximum, "information_schema", domain, nil})
	}
	fixture.PgDescriptionExecutorRows(c.t, c.p, c.b, c.conn, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull,t.typtypmod,a.atthasdef FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.sql_parts'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull", "typtypmod", "atthasdef"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL", "INT4", "BOOL"}, attributes))
	for _, s := range []fixture.Step{
		fixture.Select(`SELECT column_name,ordinal_position,is_nullable,data_type,character_maximum_length,domain_schema,domain_name,column_default FROM information_schema.columns WHERE table_schema='information_schema' AND table_name='sql_parts' ORDER BY ordinal_position`, []string{"column_name", "ordinal_position", "is_nullable", "data_type", "character_maximum_length", "domain_schema", "domain_name", "column_default"}, []string{"NAME", "INT4", "VARCHAR", "VARCHAR", "INT4", "NAME", "NAME", "VARCHAR"}, declared),
		fixture.Select(`SELECT c.relkind,c.relhasindex,c.reltype>0 AS positive_type,c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link,c.relacl IS NULL AS default_acl FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.sql_parts'::regclass`, []string{"relkind", "relhasindex", "positive_type", "same_type", "same_relation", "array_element", "array_link", "default_acl"}, []string{"CHAR", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"r", false, true, true, true, true, true, true}}),
		fixture.Select(`SELECT table_type FROM information_schema.tables WHERE table_schema='information_schema' AND table_name='sql_parts'`, []string{"table_type"}, []string{"VARCHAR"}, [][]any{{"BASE TABLE"}}),
		fixture.Select(`SELECT table_name FROM information_schema.views WHERE table_schema='information_schema' AND table_name='sql_parts'`, []string{"table_name"}, []string{"NAME"}, nil),
	} {
		c.run(c.conn, s)
		c.run(c.conn, fixture.Prepared(s))
	}
}

func TestReferenceInfoSQLPartsIdentity(t *testing.T) {
	spScenario(t, false, spCatalogShape)
}

// Retain the actual descriptors, including across iteration and explicit Close.
func spCursor(c *spCase, q string, names []string, want [][]any, prepared bool) {
	c.t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := c.conn.PrepareContext(c.p.Ctx, q)
		if prepareErr != nil {
			c.t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(c.p.Ctx)
	} else {
		rows, err = c.conn.QueryContext(c.p.Ctx, q)
	}
	if err != nil {
		c.t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		c.t.Fatal(err)
	}
	spMetadata(c.t, columns, names, c.b.PG)
	var got [][]any
	for rows.Next() {
		values, dest := make([]any, len(names)), make([]any, len(names))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			c.t.Fatal(err)
		}
		for i, value := range values {
			if names[i] == "is_verified_by" {
				if value != nil {
					c.t.Fatalf("verification raw=%T(%v)", value, value)
				}
			} else if _, ok := value.(string); !ok {
				c.t.Fatalf("%s raw=%T(%v), want string", names[i], value, value)
			}
		}
		got = append(got, values)
	}
	if err := rows.Err(); err != nil {
		c.t.Fatal(err)
	}
	if diff, ok := fixture.CompareRows(got, want, true); !ok {
		c.t.Fatal(diff)
	}
	spMetadata(c.t, columns, names, c.b.PG)
	if err := rows.Close(); err != nil {
		c.t.Fatal(err)
	}
	spMetadata(c.t, columns, names, c.b.PG)
}

func TestReferenceInfoSQLPartsShape(t *testing.T) {
	spScenario(t, false, func(c *spCase) {
		var nulls, empties [][]any
		for range 11 {
			nulls = append(nulls, []any{nil})
			empties = append(empties, []any{""})
		}
		for _, prepared := range []bool{false, true} {
			spCursor(c, spQuery, spColumns, spRows(), prepared)
			spCursor(c, spQuery+" WHERE FALSE", spColumns, nil, prepared)
			spCursor(c, "SELECT is_verified_by FROM "+spTable, []string{"is_verified_by"}, nulls, prepared)
			spCursor(c, "SELECT comments FROM "+spTable, []string{"comments"}, empties, prepared)
		}
		for _, predicate := range []string{"FALSE", "feature_id='999'", "feature_id=NULL", "feature_id=''", "is_verified_by IS NOT NULL", "is_verified_by=''", "comments IS NULL", "is_supported='YES'"} {
			c.check(spQuery+" WHERE "+predicate, nil)
		}
		c.check(spQuery+" WHERE feature_id=$1", nil, nil)
		for _, predicate := range []string{"is_verified_by IS NULL", "comments IS NOT NULL", "comments=''", "is_supported='NO'"} {
			c.check(spQuery+" WHERE "+predicate, spRows())
		}
	})
}
