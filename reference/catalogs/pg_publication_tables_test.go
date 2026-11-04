package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const publicationTablesSQL = "SELECT pubname,schemaname,tablename,attnames,rowfilter FROM pg_catalog.pg_publication_tables WHERE pubname LIKE 'publication_rt_%' ORDER BY pubname,schemaname,tablename"

func publicationTablesMetadata(t *testing.T, columns []*sql.ColumnType) {
	t.Helper()
	names := []string{"pubname", "schemaname", "tablename", "attnames", "rowfilter"}
	types := []string{"NAME", "NAME", "NAME", "_NAME", "TEXT"}
	if len(columns) != len(names) {
		t.Fatalf("view descriptor has %d columns, want five", len(columns))
	}
	for i, c := range columns {
		if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
			t.Fatalf("column %d: %s/%s, want %s/%s", i, c.Name(), c.DatabaseTypeName(), names[i], types[i])
		}
	}
}

// Inspect each driver's actual textual carriers before normalizing owned values.
// Do not infer ScanType or nullable-descriptor parity between database/sql drivers.
func publicationTablesConsume(t *testing.T, b fixture.Backend, rows *sql.Rows, want [][]any) {
	t.Helper()
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	publicationTablesMetadata(t, columns)
	got := make([][]any, 0)
	for rows.Next() {
		row := make([]any, 5)
		if err := rows.Scan(&row[0], &row[1], &row[2], &row[3], &row[4]); err != nil {
			t.Fatal(err)
		}
		for i, cell := range row {
			if cell == nil {
				continue
			}
			if b.PG && i < 4 {
				v, ok := cell.([]byte)
				if !ok {
					t.Fatalf("raw column %d: %T, want []byte", i, cell)
				}
				row[i] = string(v)
			} else if _, ok := cell.(string); !ok {
				t.Fatalf("raw column %d: %T, want string", i, cell)
			}
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want == nil {
		want = make([][]any, 0)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("publication view rows got %#v, want %#v", got, want)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	publicationTablesMetadata(t, columns)
}

func publicationTablesRead(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, prepared bool, want ...[]any) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if prepared {
		stmt, prepareErr := e.PrepareContext(p.Ctx, query)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(p.Ctx)
	} else {
		rows, err = e.QueryContext(p.Ctx, query)
	}
	if err != nil {
		t.Fatal(err)
	}
	publicationTablesConsume(t, b, rows, want)
}

func publicationTablesState(rows ...[]any) fixture.Step {
	return fixture.Select(publicationTablesSQL, []string{"pubname", "schemaname", "tablename", "attnames", "rowfilter"}, []string{"NAME", "NAME", "NAME", "_NAME", "TEXT"}, rows)
}

func TestReferencePGPublicationTablesShapeProjection(t *testing.T) {
	publicationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := publicationConn(t, p, b)
		run := func(steps ...fixture.Step) { publicationRun(t, p, b, c, steps...) }
		probe := func(rows ...[]any) {
			for _, prepared := range []bool{false, true} {
				publicationTablesRead(t, p, b, c, publicationTablesSQL, prepared, rows...)
				publicationTablesRead(t, p, b, c, "SELECT pubname,schemaname,tablename,attnames,rowfilter FROM pg_catalog.pg_publication_tables WHERE false", prepared)
				publicationTablesRead(t, p, b, c, "SELECT pubname,schemaname,tablename,attnames,rowfilter FROM pg_catalog.pg_publication_tables LIMIT 0", prepared)
			}
			run(publicationTablesState(rows...))
		}
		probe()
		run(fixture.Select("SELECT attname,atttypid::bigint AS atttypid,attnotnull FROM pg_catalog.pg_attribute WHERE attrelid='pg_catalog.pg_publication_tables'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "atttypid", "attnotnull"}, []string{"NAME", "INT8", "BOOL"}, [][]any{{"pubname", int64(19), false}, {"schemaname", int64(19), false}, {"tablename", int64(19), false}, {"attnames", int64(1003), false}, {"rowfilter", int64(25), false}}),
			fixture.Exec("CREATE SCHEMA publication_rt_s", 0),
			fixture.Exec("CREATE TABLE publication_rt_s.t(id integer,v integer,b integer)", 0),
			fixture.Exec("INSERT INTO publication_rt_s.t VALUES(1,-2,3),(2,4,5)", 2))
		// Ordinary DDL and local rows do not invent explicit publication membership.
		probe()
		run(fixture.Exec("CREATE PUBLICATION publication_rt_p FOR TABLE publication_rt_s.t(b,id) WHERE(v>0) WITH(publish='insert')", 0),
			fixture.Exec("CREATE PUBLICATION publication_rt_q FOR TABLE publication_rt_s.t", 0))
		probe([]any{"publication_rt_p", "publication_rt_s", "t", "{id,b}", "(v > 0)"}, []any{"publication_rt_q", "publication_rt_s", "t", "{id,v,b}", nil})
		run(fixture.Exec("ALTER TABLE publication_rt_s.t ADD COLUMN extra integer", 0))
		probe([]any{"publication_rt_p", "publication_rt_s", "t", "{id,b}", "(v > 0)"}, []any{"publication_rt_q", "publication_rt_s", "t", "{id,v,b,extra}", nil})
		run(fixture.Exec("ALTER TABLE publication_rt_s.t RENAME COLUMN b TO bee", 0))
		probe([]any{"publication_rt_p", "publication_rt_s", "t", "{id,bee}", "(v > 0)"}, []any{"publication_rt_q", "publication_rt_s", "t", "{id,v,bee,extra}", nil})
		run(fixture.Exec("ALTER TABLE publication_rt_s.t RENAME COLUMN v TO value", 0))
		probe([]any{"publication_rt_p", "publication_rt_s", "t", "{id,bee}", "(value > 0)"}, []any{"publication_rt_q", "publication_rt_s", "t", "{id,value,bee,extra}", nil})
		array := fixture.Select("SELECT array_lower(attnames,1),array_length(attnames,1),attnames[1],attnames[2] FROM pg_catalog.pg_publication_tables WHERE pubname='publication_rt_p'", []string{"array_lower", "array_length", "attnames", "attnames"}, []string{"INT4", "INT4", "NAME", "NAME"}, [][]any{{int64(1), int64(2), "id", "bee"}})
		run(array, fixture.Prepared(array))
		// Schema coverage cannot coexist with an explicit column list in one
		// publication. Replace that mapping before exercising broad overlap.
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p SET TABLE publication_rt_s.t WHERE(value>0)", 0))
		filtered := []any{"publication_rt_p", "publication_rt_s", "t", "{id,value,bee,extra}", "(value > 0)"}
		q := []any{"publication_rt_q", "publication_rt_s", "t", "{id,value,bee,extra}", nil}
		probe(filtered, q)
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p ADD TABLES IN SCHEMA publication_rt_s", 0))
		probe([]any{"publication_rt_p", "publication_rt_s", "t", "{id,value,bee,extra}", nil}, q)
		run(fixture.Exec("ALTER PUBLICATION publication_rt_p DROP TABLES IN SCHEMA publication_rt_s", 0), fixture.Exec("ALTER PUBLICATION publication_rt_p SET(publish='')", 0))
		probe(filtered, q)
		run(fixture.Select("SELECT id,value,bee,extra FROM publication_rt_s.t ORDER BY id", []string{"id", "value", "bee", "extra"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(-2), int64(3), nil}, {int64(2), int64(4), int64(5), nil}}))
	})
}
