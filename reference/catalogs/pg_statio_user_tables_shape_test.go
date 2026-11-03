package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const statioUserTableProbe = "SELECT * FROM pg_catalog.pg_statio_user_tables WHERE relid=$1::oid"

// REL_18_6 system_views.sql expands all eleven columns and excludes exactly
// these case-sensitive namespace names and the lowercase pg_toast prefix.
const statioUserTableDefinition = ` SELECT relid,
    schemaname,
    relname,
    heap_blks_read,
    heap_blks_hit,
    idx_blks_read,
    idx_blks_hit,
    toast_blks_read,
    toast_blks_hit,
    tidx_blks_read,
    tidx_blks_hit
   FROM pg_statio_all_tables
  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));`

func TestReferencePGStatioUserTablesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			statioSequenceExec(t, p, b, b.DB, "CREATE TABLE statio_user_shape(k integer)")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO statio_user_shape VALUES(1),(2)", 2))
			oid := statioSequenceOID(t, p, b.DB, "public.statio_user_shape")
			for _, c := range []struct {
				name string
				q    string
				args []any
				rows int
			}{
				{"qualified_identity", statioUserTableProbe, []any{oid}, 1},
				{"unqualified_identity", "SELECT * FROM pg_statio_user_tables WHERE relid=$1::oid", []any{oid}, 1},
				{"qualified_name", "SELECT * FROM pg_catalog.pg_statio_user_tables WHERE schemaname='public' AND relname=$1", []any{"statio_user_shape"}, 1},
				{"unqualified_name", "SELECT * FROM pg_statio_user_tables WHERE schemaname='public' AND relname=$1", []any{"statio_user_shape"}, 1},
				{"qualified_empty", "SELECT * FROM pg_catalog.pg_statio_user_tables WHERE false", nil, 0},
				{"unqualified_empty", "SELECT * FROM pg_statio_user_tables WHERE false", nil, 0},
				{"missing_identity", statioUserTableProbe, []any{int64(0)}, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						name := "direct"
						if prepared {
							name = "prepared"
						}
						t.Run(name, func(t *testing.T) {
							var e fixture.Executor = b.DB
							if prepared {
								stmt, err := b.DB.PrepareContext(p.Ctx, c.q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								e = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: c.q}
							}
							// Assert raw OID/NAME/INT8 carriers and nullable scans
							// before fixture normalization, including empty metadata.
							got := statioTableRows(t, p, b, e, c.q, c.args...)
							if len(got) != c.rows {
								t.Fatalf("user witness rows=%d want=%d: %+v", len(got), c.rows, got)
							}
							if c.rows == 1 {
								if got[0].oid != oid || got[0].schema != "public" || got[0].name != "statio_user_shape" {
									t.Fatal("represented user table identity", got)
								}
								statioTableAbsentToast(t, got[0])
								if got[0].blocks[2].Valid || got[0].blocks[3].Valid {
									t.Fatal("empty index aggregates must be NULL", got)
								}
							}
						})
					}
				})
			}

			attrs := [][]any{}
			for i, name := range statioTableNames {
				typ, collation := int64(20), int64(0)
				if i == 0 {
					typ = 26
				}
				if i == 1 || i == 2 {
					typ, collation = 19, 950
				}
				attrs = append(attrs, []any{int64(i + 1), name, typ, int64(-1), collation, false, false})
			}
			steps := []fixture.Step{
				fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attcollation,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_statio_user_tables'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attcollation", "attnotnull", "attisdropped"}, []string{"INT2", "NAME", "OID", "INT4", "OID", "BOOL", "BOOL"}, attrs),
				fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link,c.relowner=t.typowner AS owner_link,t.typowner=a.typowner AS array_owner_link,c.relnamespace=t.typnamespace AS namespace_link,t.typnamespace=a.typnamespace AS array_namespace_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_user_tables'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link", "owner_link", "array_owner_link", "namespace_link", "array_namespace_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), true, true, true, true, true, true, true}}),
				fixture.Select("SELECT n.nspname,t.typname,t.typtype,a.typname AS array_name FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_user_tables'::regclass", []string{"nspname", "typname", "typtype", "array_name"}, []string{"NAME", "NAME", "CHAR", "NAME"}, [][]any{{"pg_catalog", "pg_statio_user_tables", "c", "_pg_statio_user_tables"}}),
				fixture.Select("SELECT reltoastrelid FROM pg_class WHERE oid=$1::oid", []string{"reltoastrelid"}, []string{"OID"}, [][]any{{int64(0)}}, oid),
				fixture.Select("SELECT heap_blks_read IS NOT NULL AS heap_read,heap_blks_hit IS NOT NULL AS heap_hit,idx_blks_read IS NULL AS idx_read,idx_blks_hit IS NULL AS idx_hit,toast_blks_read IS NULL AS toast_read,toast_blks_hit IS NULL AS toast_hit,tidx_blks_read IS NULL AS tidx_read,tidx_blks_hit IS NULL AS tidx_hit FROM pg_statio_user_tables WHERE relid=$1::oid", []string{"heap_read", "heap_hit", "idx_read", "idx_hit", "toast_read", "toast_hit", "tidx_read", "tidx_hit"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true, true, true}}, oid),
				fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_user_tables'", []string{"definition"}, []string{"TEXT"}, [][]any{{statioUserTableDefinition}}),
				fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_statio_user_tables'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{statioUserTableDefinition}}),
			}
			if !b.PG {
				// Allocated native initialization identities are not fixed upstream
				// bootstrap OIDs. The oracle checks reciprocal links above instead.
				steps = append(steps, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_statio_user_tables'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12183), int64(12185), int64(12184)}}))
			}
			for _, s := range steps {
				t.Run(s.Query, func(t *testing.T) {
					t.Run("direct", func(t *testing.T) {
						fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
					})
					t.Run("prepared", func(t *testing.T) {
						fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(s))
					})
				})
			}

			t.Run("retained_rows_and_held_metadata", func(t *testing.T) {
				stmt, err := b.DB.PrepareContext(p.Ctx, statioUserTableProbe)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				rows, err := stmt.QueryContext(p.Ctx, oid)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				statioTableMetadata(t, rows, b.PG)
				// Keep the actual result open while a separate execution produces
				// typed empty metadata; it must not overwrite retained descriptors.
				empty := statioTableRows(t, p, b, b.DB, statioUserTableProbe, int64(0))
				if len(empty) != 0 {
					t.Fatal("missing identity", empty)
				}
				statioTableMetadata(t, rows, b.PG)
				if !rows.Next() {
					t.Fatal("retained user identity", rows.Err())
				}
				got := statioTableScan(t, rows, b.PG)
				if got.oid != oid || got.schema != "public" || got.name != "statio_user_shape" {
					t.Fatal("retained identity", got)
				}
				statioTableAbsentToast(t, got)
				if got.blocks[2].Valid || got.blocks[3].Valid {
					t.Fatal("retained empty index aggregate", got)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("retained identity multiplicity", rows.Err())
				}
			})
		})
	}
}
