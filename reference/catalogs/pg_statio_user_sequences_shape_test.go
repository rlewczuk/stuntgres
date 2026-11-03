package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

const statioUserSequenceProbe = "SELECT * FROM pg_catalog.pg_statio_user_sequences WHERE relid=$1::oid"

// Released REL_18_6 definition, with presentation confirmed by the pinned oracle.
const statioUserSequenceDefinition = ` SELECT relid,
    schemaname,
    relname,
    blks_read,
    blks_hit
   FROM pg_statio_all_sequences
  WHERE ((schemaname <> ALL (ARRAY['pg_catalog'::name, 'information_schema'::name])) AND (schemaname !~ '^pg_toast'::text));`

func statioUserSequenceCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, oid int64, schema, name string, read, hit int64) {
	t.Helper()
	got := statioSequenceRows(t, p, b, e, statioUserSequenceProbe, oid)
	if name == "" {
		if len(got) != 0 {
			t.Fatal("dropped user identity retained", got)
		}
		return
	}
	want := statioSequenceRow{oid: oid, schema: schema, name: name, read: read, hit: hit}
	if len(got) != 1 || got[0].oid != oid || got[0].schema != schema || got[0].name != name {
		t.Fatal("actual named user identity", got, want)
	}
	if !b.PG && got[0] != want {
		t.Fatal("independent logical counters", got, want)
	}
}

// All three named views and a held user statement share one statistics-reader
// boundary. PostgreSQL physical values need not equal native logical estimates.
func statioUserSequenceWitness(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, held fixture.Executor, oid int64, schema, name string, read, hit int64) {
	t.Helper()
	statioSequenceExec(t, p, b, conn, "BEGIN")
	defer conn.ExecContext(context.Background(), "ROLLBACK")
	all := statioSequenceRows(t, p, b, conn, statioSequenceProbe, oid)
	user := statioSequenceRows(t, p, b, conn, statioUserSequenceProbe, oid)
	prepared := statioSequenceRows(t, p, b, held, statioUserSequenceProbe, oid)
	if !reflect.DeepEqual(all, user) || !reflect.DeepEqual(user, prepared) {
		t.Fatal("named all/user and held reader disagree", all, user, prepared)
	}
	statioUserSequenceCheck(t, p, b, conn, oid, schema, name, read, hit)
	statioUserSequenceCheck(t, p, b, held, oid, schema, name, read, hit)
	statioSysSequenceEmpty(t, p, b, conn, oid)
	statioSequenceExec(t, p, b, conn, "COMMIT")
}

func TestReferencePGStatioUserSequencesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			statioSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE public.statio_user_shape")
			oid := statioSequenceOID(t, p, b.DB, "public.statio_user_shape")
			for _, q := range []string{
				"SELECT * FROM pg_catalog.pg_statio_user_sequences WHERE false",
				"SELECT * FROM pg_statio_user_sequences WHERE false",
				"SELECT * FROM pg_statio_user_sequences WHERE relname='statio_user_shape'",
				statioUserSequenceProbe,
			} {
				for _, prepared := range []bool{false, true} {
					var e fixture.Executor = b.DB
					if prepared {
						stmt, err := b.DB.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						e = fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
					}
					args := []any{}
					if q == statioUserSequenceProbe {
						args = append(args, oid)
					}
					got := statioSequenceRows(t, p, b, e, q, args...)
					if q == statioUserSequenceProbe || q == "SELECT * FROM pg_statio_user_sequences WHERE relname='statio_user_shape'" {
						if len(got) != 1 || got[0].oid != oid || got[0].schema != "public" || got[0].name != "statio_user_shape" {
							t.Fatal("populated typed user row", got)
						}
					} else if len(got) != 0 {
						t.Fatal("empty typed user result", got)
					}
				}
			}
			steps := []fixture.Step{
				fixture.Select("SELECT attnum,attname,atttypid,attnotnull FROM pg_attribute WHERE attrelid='pg_catalog.pg_statio_user_sequences'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull"}, []string{"INT2", "NAME", "OID", "BOOL"}, [][]any{
					{int64(1), "relid", int64(26), false}, {int64(2), "schemaname", int64(19), false}, {int64(3), "relname", int64(19), false}, {int64(4), "blks_read", int64(20), false}, {int64(5), "blks_hit", int64(20), false},
				}),
				fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_user_sequences'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(5), true, true, true}}),
				fixture.Select("SELECT t.typname,a.typname AS array_name FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_statio_user_sequences'::regclass", []string{"typname", "array_name"}, []string{"NAME", "NAME"}, [][]any{{"pg_statio_user_sequences", "_pg_statio_user_sequences"}}),
				fixture.Select("SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_statio_user_sequences'", []string{"definition"}, []string{"TEXT"}, [][]any{{statioUserSequenceDefinition}}),
				fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_statio_user_sequences'::regclass)", []string{"pg_get_viewdef"}, []string{"TEXT"}, [][]any{{statioUserSequenceDefinition}}),
			}
			if !b.PG {
				steps = append(steps, fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_statio_user_sequences'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12222), int64(12224), int64(12223)}}))
			}
			for _, s := range steps {
				t.Run(s.Query, func(t *testing.T) {
					fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
					fixture.PgDescriptionExecutorRows(t, p, b, b.DB, fixture.Prepared(s))
				})
			}
		})
	}
}
