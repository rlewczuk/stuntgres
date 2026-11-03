package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

const statioSysSequenceProbe = "SELECT * FROM pg_catalog.pg_statio_sys_sequences WHERE relid=$1::oid"

// The five projections and case-sensitive predicate are specified by REL_18_6
// system_views.sql; allocator-owned oracle relation/type OIDs are not constants.
const statioSysSequenceDefinition = ` SELECT relid,
    schemaname,
    relname,
    blks_read,
    blks_hit
   FROM pg_statio_all_sequences
  WHERE ((schemaname = ANY (ARRAY['pg_catalog'::name, 'information_schema'::name])) OR (schemaname ~ '^pg_toast'::text));`

func TestReferencePGStatioSysSequencesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{
				"SELECT * FROM pg_catalog.pg_statio_sys_sequences",
				"SELECT * FROM pg_statio_sys_sequences",
				"SELECT * FROM pg_catalog.pg_statio_sys_sequences WHERE false",
			} {
				t.Run(q, func(t *testing.T) {
					for _, prepared := range []bool{false, true} {
						name := "direct"
						if prepared {
							name = "prepared"
						}
						t.Run(name, func(t *testing.T) {
							var e fixture.Executor = b.DB
							if prepared {
								stmt, err := b.DB.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								e = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
							}
							if got := statioSequenceRows(t, p, b, e, q); len(got) != 0 {
								t.Fatal("stock system sequence inventory must be empty", got)
							}
						})
					}
				})
			}
			steps := []fixture.Step{
				fixture.Select("SELECT attnum,attname,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_catalog.pg_statio_sys_sequences'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull"}, []string{"INT2", "NAME", "OID", "BOOL"}, [][]any{
					{int64(1), "relid", int64(26), false},
					{int64(2), "schemaname", int64(19), false},
					{int64(3), "relname", int64(19), false},
					{int64(4), "blks_read", int64(20), false},
					{int64(5), "blks_hit", int64(20), false},
				}),
				fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_sys_sequences'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(5), true, true, true}}),
				fixture.Select("SELECT t.typname,a.typname AS array_name FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_sys_sequences'::regclass", []string{"typname", "array_name"}, []string{"NAME", "NAME"}, [][]any{{"pg_statio_sys_sequences", "_pg_statio_sys_sequences"}}),
				fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_sys_sequences'", []string{"definition"}, []string{"TEXT"}, [][]any{{statioSysSequenceDefinition}}),
				fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_statio_sys_sequences'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{statioSysSequenceDefinition}}),
			}
			if !b.PG {
				steps = append(steps, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_statio_sys_sequences'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12218), int64(12220), int64(12219)}}))
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
