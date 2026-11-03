package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var statioNames = []string{"relid", "indexrelid", "schemaname", "relname", "indexrelname", "idx_blks_read", "idx_blks_hit"}
var statioTypes = []string{"OID", "OID", "NAME", "NAME", "NAME", "INT8", "INT8"}

func statioExec(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, queries ...string) {
	t.Helper()
	for _, q := range queries {
		vacuumRun(t, p, b, c, fixture.Exec(q, 0))
	}
}

func statioMetadata(t *testing.T, rows *sql.Rows, pg bool) []*sql.ColumnType {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, statioNames) {
		t.Fatal("seven columns", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, column := range columns {
		scan := reflect.TypeFor[int64]()
		if i >= 2 && i <= 4 {
			scan = reflect.TypeFor[string]()
		}
		if pg && i < 5 {
			scan = reflect.TypeFor[any]()
		}
		nullable, known := column.Nullable()
		length, hasLength := column.Length()
		precision, scale, hasDecimal := column.DecimalSize()
		if column.Name() != statioNames[i] || column.DatabaseTypeName() != statioTypes[i] || column.ScanType() != scan || nullable != !pg || known != !pg || length != 0 || hasLength || precision != 0 || scale != 0 || hasDecimal {
			t.Fatalf("column%d: name=%s type=%s scan=%v nullable=%v/%v length=%d/%v decimal=%d/%d/%v", i, column.Name(), column.DatabaseTypeName(), column.ScanType(), nullable, known, length, hasLength, precision, scale, hasDecimal)
		}
	}
	return columns
}

// Read before normalization: pq's NAME/OID byte carriers are not native values.
func statioRows(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, query string) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return statioConsumeRows(t, rows, b.PG)
}

func statioConsumeRows(t *testing.T, rows *sql.Rows, pg bool) [][]any {
	t.Helper()
	defer rows.Close()
	saved := statioMetadata(t, rows, pg)
	var result [][]any
	for rows.Next() {
		values := make([]any, 7)
		targets := make([]any, 7)
		for i := range targets {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if pg && i < 5 {
				if _, ok := value.([]byte); !ok {
					t.Fatalf("pq raw%d=%T", i, value)
				}
			} else if i >= 2 && i <= 4 {
				if _, ok := value.(string); !ok {
					t.Fatalf("native NAME%d=%T", i, value)
				}
			} else if _, ok := value.(int64); !ok {
				t.Fatalf("raw integer%d=%T", i, value)
			}
			values[i] = fixture.Normalize(value)
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if saved[0].DatabaseTypeName() != "OID" || saved[6].DatabaseTypeName() != "INT8" {
		t.Fatal("retained metadata changed")
	}
	return result
}

func statioNamesFor(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, predicate string, names ...string) {
	t.Helper()
	var want [][]any
	for _, name := range names {
		want = append(want, []any{name})
	}
	s := fixture.Select("SELECT indexrelname FROM pg_statio_all_indexes WHERE "+predicate+" ORDER BY indexrelname", []string{"indexrelname"}, []string{"NAME"}, want)
	vacuumRun(t, p, b, c, s)
	vacuumRun(t, p, b, c, fixture.Prepared(s))
}

func statioIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn, name string) int64 {
	t.Helper()
	var id int64
	if err := c.QueryRowContext(p.Ctx, "SELECT '"+name+"'::regclass::oid::bigint").Scan(&id); err != nil || id == 0 {
		t.Fatal("real identity", name, id, err)
	}
	return id
}

func TestReferenceStatioAllIndexesShape(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c, "CREATE TABLE statio_shape(k integer)", "CREATE INDEX statio_shape_i ON statio_shape(k)")
		for _, view := range []string{"pg_statio_all_indexes", "pg_catalog.pg_statio_all_indexes"} {
			for _, predicate := range []string{"false", "relname='statio_shape'"} {
				q := "SELECT * FROM " + view + " WHERE " + predicate
				got := statioRows(t, p, b, c, q)
				want := 0
				if predicate != "false" {
					want = 1
				}
				if len(got) != want {
					t.Fatal("cardinality", got)
				}
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				rows, err := stmt.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				prepared := statioConsumeRows(t, rows, b.PG)
				if !reflect.DeepEqual(prepared, got) {
					t.Fatal("prepared values", prepared, got)
				}
				stmt.Close()
			}
		}
		var attrs [][]any
		for i, name := range statioNames {
			collation := int64(0)
			if i >= 2 && i <= 4 {
				collation = 950
			}
			attrs = append(attrs, []any{name, int64(i + 1), []int64{26, 26, 19, 19, 19, 20, 20}[i], int64(-1), collation, false, false})
		}
		vacuumRun(t, p, b, c, fixture.Select("SELECT attname,attnum,atttypid::bigint,atttypmod,attcollation::bigint,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_statio_all_indexes'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "atttypid", "atttypmod", "attcollation", "attnotnull", "attisdropped"}, []string{"NAME", "INT2", "INT8", "INT4", "INT8", "BOOL", "BOOL"}, attrs))
		for _, s := range []fixture.Step{
			fixture.Select("SELECT relkind,pg_get_userbyid(relowner) AS owner,relhasindex FROM pg_class WHERE oid='pg_statio_all_indexes'::regclass", []string{"relkind", "owner", "relhasindex"}, []string{"CHAR", "NAME", "BOOL"}, [][]any{{"v", fixture.PGTableViewsOwner(b), false}}),
			fixture.Select("SELECT t.typname,t.typtype,t.typrelid=c.oid AS linked,t.typowner=c.relowner AS owned,a.typelem=t.oid AS element_link,a.typowner=c.relowner AS array_owned FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_statio_all_indexes'::regclass", []string{"typname", "typtype", "linked", "owned", "element_link", "array_owned"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pg_statio_all_indexes", "c", true, true, true, true}}),
		} {
			vacuumRun(t, p, b, c, s)
			vacuumRun(t, p, b, c, fixture.Prepared(s))
		}
	})
}

func TestReferenceStatioAllIndexesIdentity(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c, "CREATE TABLE statio_identity(k integer PRIMARY KEY,v integer)", "CREATE INDEX statio_hash ON statio_identity USING hash(k)", "CREATE INDEX statio_unused ON statio_identity(v)")
		vacuumRun(t, p, b, c, fixture.Exec("INSERT INTO statio_identity VALUES(1,10),(2,20)", 2))
		statioNamesFor(t, p, b, c, "relname='statio_identity'", "statio_hash", "statio_identity_pkey", "statio_unused")
		s := fixture.Select("SELECT s.indexrelname,s.relid=c.oid AS parent_link,s.indexrelid=i.oid AS index_link,s.relid=x.indrelid AS indexed_parent,s.indexrelid=x.indexrelid AS indexed_id FROM pg_statio_all_indexes s JOIN pg_class c ON c.oid=s.relid JOIN pg_class i ON i.oid=s.indexrelid JOIN pg_index x ON x.indexrelid=s.indexrelid WHERE s.relname='statio_identity' ORDER BY s.indexrelname", []string{"indexrelname", "parent_link", "index_link", "indexed_parent", "indexed_id"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"statio_hash", true, true, true, true}, {"statio_identity_pkey", true, true, true, true}, {"statio_unused", true, true, true, true}})
		vacuumRun(t, p, b, c, s)
		vacuumRun(t, p, b, c, fixture.Prepared(s))
		old := statioIdentity(t, p, c, "statio_hash")
		parent := statioIdentity(t, p, c, "statio_identity")
		statioExec(t, p, b, c, "ALTER INDEX statio_hash RENAME TO statio_renamed", "ALTER TABLE statio_identity RENAME TO statio_parent", "CREATE SCHEMA statio_moved", "ALTER TABLE statio_parent SET SCHEMA statio_moved")
		if statioIdentity(t, p, c, "statio_moved.statio_renamed") != old || statioIdentity(t, p, c, "statio_moved.statio_parent") != parent {
			t.Fatal("rename/move replaced identity")
		}
		statioNamesFor(t, p, b, c, "relname='statio_parent' AND schemaname='statio_moved'", "statio_identity_pkey", "statio_renamed", "statio_unused")
		statioExec(t, p, b, c, "DROP INDEX statio_moved.statio_renamed")
		statioNamesFor(t, p, b, c, "relname='statio_parent'", "statio_identity_pkey", "statio_unused")
		statioExec(t, p, b, c, "CREATE INDEX statio_renamed ON statio_moved.statio_parent USING hash(k)")
		if statioIdentity(t, p, c, "statio_moved.statio_renamed") == old {
			t.Fatal("same-name successor reused identity")
		}
		statioExec(t, p, b, c, "DROP TABLE statio_moved.statio_parent", "DROP SCHEMA statio_moved")
		statioNamesFor(t, p, b, c, "relname='statio_parent'")
		statioExec(t, p, b, c, "CREATE TABLE statio_partition(k integer) PARTITION BY RANGE(k)", "CREATE TABLE statio_leaf PARTITION OF statio_partition FOR VALUES FROM(0) TO(10)", "CREATE INDEX statio_leaf_i ON statio_leaf(k)", "CREATE MATERIALIZED VIEW statio_mat AS SELECT k FROM statio_leaf", "CREATE INDEX statio_mat_i ON statio_mat(k)", "CREATE VIEW statio_plain AS SELECT k FROM statio_leaf", "CREATE SEQUENCE statio_sequence")
		statioNamesFor(t, p, b, c, "relname='statio_partition'")
		statioNamesFor(t, p, b, c, "relname IN ('statio_leaf','statio_mat')", "statio_leaf_i", "statio_mat_i")
		s = fixture.Select("SELECT s.relname,s.indexrelname,s.relid=c.oid AS parent_link,s.indexrelid=i.oid AS index_link,s.relid=x.indrelid AS indexed_parent,s.indexrelid=x.indexrelid AS indexed_id FROM pg_statio_all_indexes s JOIN pg_class c ON c.oid=s.relid JOIN pg_class i ON i.oid=s.indexrelid JOIN pg_index x ON x.indexrelid=s.indexrelid WHERE s.relname IN ('statio_leaf','statio_mat') ORDER BY s.relname", []string{"relname", "indexrelname", "parent_link", "index_link", "indexed_parent", "indexed_id"}, []string{"NAME", "NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"statio_leaf", "statio_leaf_i", true, true, true, true}, {"statio_mat", "statio_mat_i", true, true, true, true}})
		vacuumRun(t, p, b, c, s)
		vacuumRun(t, p, b, c, fixture.Prepared(s))
		statioNamesFor(t, p, b, c, "relname IN ('statio_plain','statio_sequence')")
	})
}

func TestReferenceStatioAllIndexesMembership(t *testing.T) {
	copyProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		statioExec(t, p, b, c, "CREATE TABLE statio_user(k integer)", "CREATE INDEX statio_user_i ON statio_user(k)", "CREATE TABLE information_schema.statio_system(k integer)", "CREATE INDEX statio_system_i ON information_schema.statio_system(k)")
		t.Cleanup(func() { copyProgressExec(t, p.Ctx, c, "DROP TABLE information_schema.statio_system") })
		statioExec(t, p, b, c, "SET stats_fetch_consistency='snapshot'", "BEGIN")
		predicate := "relname IN ('statio_user','statio_system')"
		system := "(schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast')"
		statioNamesFor(t, p, b, c, predicate, "statio_system_i", "statio_user_i")
		statioNamesFor(t, p, b, c, predicate+" AND "+system, "statio_system_i")
		statioNamesFor(t, p, b, c, predicate+" AND NOT "+system, "statio_user_i")
		all := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE "+predicate+" ORDER BY indexrelname")
		for _, part := range []string{system, "NOT " + system} {
			rows := statioRows(t, p, b, c, "SELECT * FROM pg_statio_all_indexes WHERE "+predicate+" AND "+part)
			if len(rows) != 1 {
				t.Fatal("partition", rows)
			}
			matched := false
			for _, row := range all {
				if reflect.DeepEqual(row, rows[0]) {
					matched = true
				}
			}
			if !matched {
				t.Fatal("category changed shared fact", rows, all)
			}
		}
		user := statioRows(t, p, b, c, "SELECT * FROM pg_statio_user_indexes WHERE "+predicate+" ORDER BY indexrelname")
		if len(user) != 1 || !reflect.DeepEqual(user[0], all[1]) {
			t.Fatal("named USER must match ALL in both modes", user, all)
		}
		if b.PG {
			for _, tc := range []struct {
				view, name string
				n          int64
			}{{"pg_statio_sys_indexes", "statio_system", 1}, {"pg_statio_sys_indexes", "statio_user", 0}} {
				vacuumRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM "+tc.view+" WHERE relname='"+tc.name+"'", []string{"n"}, []string{"INT8"}, [][]any{{tc.n}}))
			}
			for _, view := range []string{"pg_statio_sys_indexes"} {
				var mismatch int64
				q := "SELECT count(*) FROM " + view + " s JOIN pg_statio_all_indexes a ON a.indexrelid=s.indexrelid WHERE a.relname IN ('statio_user','statio_system') AND (a.idx_blks_read<>s.idx_blks_read OR a.idx_blks_hit<>s.idx_blks_hit)"
				if err := c.QueryRowContext(p.Ctx, q).Scan(&mismatch); err != nil || mismatch != 0 {
					t.Fatal("oracle shared counters", mismatch, err)
				}
			}
		}
		statioExec(t, p, b, c, "ROLLBACK")
	})
}
