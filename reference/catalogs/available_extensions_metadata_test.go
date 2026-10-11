package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// Exact reconstruction observed from pinned PostgreSQL18.0, independent of
// native represented-view metadata. Neither string has a trailing newline.
var availableExtensionsDefinitions = []string{
	` SELECT e.name,
    e.default_version,
    x.extversion AS installed_version,
    e.comment
   FROM (pg_available_extensions() e(name, default_version, comment)
     LEFT JOIN pg_extension x ON ((e.name = x.extname)));`,
	` SELECT e.name,
    e.version,
    (x.extname IS NOT NULL) AS installed,
    e.superuser,
    e.trusted,
    e.relocatable,
    e.schema,
    e.requires,
    e.comment
   FROM (pg_available_extension_versions() e(name, version, superuser, trusted, relocatable, schema, requires, comment)
     LEFT JOIN pg_extension x ON (((e.name = x.extname) AND (e.version = x.extversion))));`,
}

func TestReferenceAvailableExtensionsMetadata(t *testing.T) {
	availableExtensionsCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		owner := "postgres"
		if pg {
			owner = "stuntgres"
		}
		for index, view := range []string{"pg_available_extensions", "pg_available_extension_versions"} {
			width := int64(4)
			if index == 1 {
				width = 9
			}
			availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT relname,relnamespace::bigint AS relnamespace,relkind,relnatts FROM pg_class WHERE relname=$1", []string{"relname", "relnamespace", "relkind", "relnatts"}, []string{"NAME", "INT8", "CHAR", "INT2"}, [][]any{{view, int64(11), "v", width}}, view))
			availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT schemaname,viewname,viewowner,definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname=$1", []string{"schemaname", "viewname", "viewowner", "definition"}, []string{"NAME", "NAME", "NAME", "TEXT"}, [][]any{{"pg_catalog", view, owner, availableExtensionsDefinitions[index]}}, view))
			availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT tablename FROM pg_tables WHERE schemaname='pg_catalog' AND tablename=$1", []string{"tablename"}, []string{"NAME"}, [][]any{}, view))
			if !pg {
				continue
			}
			// PostgreSQL-only grounding, not a request for new native namespace,
			// attribute or type producers. Dynamic OIDs are observed, not guessed.
			var oid, rowtype, array int64
			if err := c.QueryRowContext(p.Ctx, "SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.relnamespace=11 AND c.relname=$1", view).Scan(&oid, &rowtype, &array); err != nil || oid <= 0 || rowtype <= 0 || array <= 0 {
				t.Fatal("oracle relation/type identities", oid, rowtype, array, err)
			}
			columns := availableExtensionsColumns
			typeIDs := []int64{19, 25, 25, 25}
			collations := []int64{950, 100, 950, 100}
			if index == 1 {
				columns = availableVersionsColumns
				typeIDs = []int64{19, 25, 16, 16, 16, 16, 19, 1003, 25}
				collations = []int64{950, 100, 0, 0, 0, 0, 950, 950, 100}
			}
			want := make([][]any, len(columns))
			for i, name := range columns {
				want[i] = []any{int64(i + 1), name, typeIDs[i], int64(-1), false, collations[i]}
			}
			s := fixture.Select("SELECT attnum,attname,atttypid::bigint AS atttypid,atttypmod,attnotnull,attcollation::bigint AS attcollation FROM pg_attribute WHERE attrelid=$1 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attnotnull", "attcollation"}, []string{"INT2", "NAME", "INT8", "INT4", "BOOL", "INT8"}, want, oid)
			fixture.RunStep(t, p.Ctx, c, r, s)
			fixture.RunStep(t, p.Ctx, c, r, fixture.Prepared(s))
			availableExtensionsBoth(t, p, c, r, fixture.Select("SELECT t.typrelid::bigint AS typrelid,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=$1", []string{"typrelid", "element_link", "array_link"}, []string{"INT8", "BOOL", "BOOL"}, [][]any{{oid, true, true}}, oid))
		}
	})
}
