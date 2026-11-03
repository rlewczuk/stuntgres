package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const statioSysTableProbe = "SELECT * FROM pg_catalog.pg_statio_sys_tables WHERE relid=$1::oid"

// REL_18_6 system_views.sql specifies the eleven projections and this exact
// case-sensitive system-namespace predicate, independently of native rendering.
const statioSysTableDefinition = ` SELECT relid,
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
  WHERE ((schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])) OR (schemaname ~ '^pg_toast'::text));`

func TestReferencePGStatioSysTablesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			oid := statioSequenceOID(t, p, b.DB, "pg_catalog.pg_class")
			if oid != 1259 {
				t.Fatal("released pg_class identity", oid)
			}
			for _, c := range []struct {
				name string
				q    string
				args []any
				rows int
			}{
				{"qualified_identity", statioSysTableProbe, []any{oid}, 1},
				{"unqualified_identity", "SELECT * FROM pg_statio_sys_tables WHERE relid=$1::oid", []any{oid}, 1},
				{"qualified_name", "SELECT * FROM pg_catalog.pg_statio_sys_tables WHERE schemaname='pg_catalog' AND relname=$1", []any{"pg_class"}, 1},
				{"qualified_empty", "SELECT * FROM pg_catalog.pg_statio_sys_tables WHERE false", nil, 0},
				{"unqualified_empty", "SELECT * FROM pg_statio_sys_tables WHERE false", nil, 0},
				{"missing_identity", statioSysTableProbe, []any{int64(0)}, 0},
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
							// Reuse the full eleven-column metadata and raw-carrier
							// assertions, including typed empty results and NULLs.
							got := statioTableRows(t, p, b, e, c.q, c.args...)
							if len(got) != c.rows {
								t.Fatalf("system witness rows=%d want=%d: %+v", len(got), c.rows, got)
							}
							if c.rows == 1 {
								if got[0].oid != oid || got[0].schema != "pg_catalog" || got[0].name != "pg_class" {
									t.Fatal("represented system table identity", got)
								}
								if !b.PG {
									statioTableAbsentToast(t, got[0])
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
				fixture.Select("SELECT attnum,attname,atttypid,atttypmod,attcollation,attnotnull,attisdropped FROM pg_attribute WHERE attrelid='pg_catalog.pg_statio_sys_tables'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "atttypmod", "attcollation", "attnotnull", "attisdropped"}, []string{"INT2", "NAME", "OID", "INT4", "OID", "BOOL", "BOOL"}, attrs),
				fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link,c.relowner=t.typowner AS owner_link,t.typowner=a.typowner AS array_owner_link,c.relnamespace=t.typnamespace AS namespace_link,t.typnamespace=a.typnamespace AS array_namespace_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_sys_tables'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link", "owner_link", "array_owner_link", "namespace_link", "array_namespace_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(11), true, true, true, true, true, true, true}}),
				fixture.Select("SELECT t.typname,t.typtype,a.typname AS array_name FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_sys_tables'::regclass", []string{"typname", "typtype", "array_name"}, []string{"NAME", "CHAR", "NAME"}, [][]any{{"pg_statio_sys_tables", "c", "_pg_statio_sys_tables"}}),
				fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_sys_tables'", []string{"definition"}, []string{"TEXT"}, [][]any{{statioSysTableDefinition}}),
				fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_statio_sys_tables'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{statioSysTableDefinition}}),
			}
			if !b.PG {
				steps = append(steps, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_statio_sys_tables'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12179), int64(12181), int64(12180)}}))
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
		})
	}
}
