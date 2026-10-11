package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceExtensionMemberNullability(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// Independent REL_18_0 catalog-prefix expectations. pq does not expose
		// attnotnull through ColumnTypes; its result capability remains unknown.
		specs := []struct {
			table        string
			oid          string
			names, types []string
			filter       string
			populated    int
			nullableLast bool
		}{
			{"pg_proc", "1255", []string{"oid", "proname", "pronamespace", "proowner", "pronargs", "prorettype", "provolatile", "proisstrict", "proparallel"}, []string{"OID", "NAME", "OID", "OID", "INT2", "OID", "CHAR", "BOOL", "CHAR"}, "proname='unaccent'", 2, false},
			{"pg_ts_template", "3764", []string{"oid", "tmplname", "tmplnamespace"}, []string{"OID", "NAME", "OID"}, "tmplname='unaccent'", 1, false},
			{"pg_ts_dict", "3600", []string{"oid", "dictname", "dictnamespace", "dictowner", "dicttemplate", "dictinitoption"}, []string{"OID", "NAME", "OID", "OID", "OID", "TEXT"}, "dictname='unaccent'", 1, true},
		}
		for _, spec := range specs {
			if pg {
				var conditions []string
				var expected [][]any
				for i, name := range spec.names {
					conditions = append(conditions, "attname='"+name+"'")
					expected = append(expected, []any{name, !(spec.nullableLast && i == len(spec.names)-1)})
				}
				query := "SELECT attname,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid=" + spec.oid + " AND (" + strings.Join(conditions, " OR ") + ")"
				s := fixture.Select(query, []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, expected)
				s.Bag = true
				fixture.RunStep(t, p.Ctx, c, r, s)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
			}
			query := "SELECT " + strings.Join(spec.names, ",") + " FROM pg_catalog." + spec.table + " WHERE " + spec.filter
			for _, prepared := range []bool{false, true} {
				extensionMemberMetadata(t, p, c, query, prepared, pg, spec.names, spec.types, spec.nullableLast, 0)
			}
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent", 0))
		extensionInventory(t, p, c)
		for _, spec := range specs {
			query := "SELECT " + strings.Join(spec.names, ",") + " FROM pg_catalog." + spec.table + " WHERE " + spec.filter
			for _, prepared := range []bool{false, true} {
				extensionMemberMetadata(t, p, c, query, prepared, pg, spec.names, spec.types, spec.nullableLast, spec.populated)
				extensionMemberMetadata(t, p, c, query+" AND FALSE", prepared, pg, spec.names, spec.types, spec.nullableLast, 0)
			}
		}
	})
}

func extensionMemberMetadata(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, prepared, pg bool, names, types []string, nullableLast bool, expectedRows int) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := c.PrepareContext(p.Ctx, query)
		if prepareErr != nil {
			t.Fatal(query, prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = c.QueryContext(p.Ctx, query)
	}
	if err != nil {
		t.Fatal(query, err)
	}
	defer rows.Close()
	metadata, err := rows.ColumnTypes()
	if err != nil || len(metadata) != len(names) {
		t.Fatal(query, metadata, err)
	}
	for i, column := range metadata {
		if column.Name() != names[i] || column.DatabaseTypeName() != types[i] {
			t.Fatal("member logical column", query, i, column.Name(), column.DatabaseTypeName())
		}
		nullable, known := column.Nullable()
		if pg {
			if nullable || known {
				t.Fatal("pq nullability must remain unknown", query, i, nullable, known)
			}
		} else if !known || nullable != (nullableLast && i == len(names)-1) {
			t.Fatal("native member nullability", query, i, nullable, known)
		}
	}
	values, destinations := make([]any, len(names)), make([]any, len(names))
	for i := range values {
		destinations[i] = &values[i]
	}
	count := 0
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(query, err)
		}
		for i, value := range values {
			if value == nil {
				t.Fatal("registered member unexpectedly NULL", query, names[i])
			}
		}
		count++
	}
	if err := rows.Err(); err != nil || count != expectedRows {
		t.Fatal("member population", query, count, expectedRows, err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(query, err)
	}
}
