package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

var pgTriggerColumns = []string{"oid", "tgrelid", "tgparentid", "tgname", "tgfoid", "tgtype", "tgenabled", "tgisinternal", "tgconstrrelid", "tgconstrindid", "tgconstraint", "tgdeferrable", "tginitdeferred", "tgnargs", "tgattr", "tgargs", "tgqual", "tgoldtable", "tgnewtable"}
var pgTriggerTypes = []string{"OID", "OID", "OID", "NAME", "OID", "INT2", "CHAR", "BOOL", "OID", "OID", "OID", "BOOL", "BOOL", "INT2", "INT2VECTOR", "BYTEA", "PG_NODE_TREE", "NAME", "NAME"}

func pgTriggerExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(query, 0))
}

func pgTriggerBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionRows(t, p, b, s)
	fixture.PgDescriptionRows(t, p, b, fixture.Prepared(s))
}

func TestReferencePGTriggerShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			s := fixture.Select("SELECT * FROM pg_catalog.pg_trigger WHERE FALSE", pgTriggerColumns, pgTriggerTypes, [][]any{})
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(cols, pgTriggerColumns) {
					t.Fatal("trigger columns", cols, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 19 {
					t.Fatal("trigger descriptors", types, err)
				}
				for i, typ := range types {
					if typ.Name() != pgTriggerColumns[i] || typ.DatabaseTypeName() != pgTriggerTypes[i] {
						t.Fatal("trigger descriptor", i, typ)
					}
					nullable, known := typ.Nullable()
					if b.PG {
						if known {
							t.Fatal("pq nullability unexpectedly known", i)
						}
					} else if !known || nullable != (i >= 16) {
						t.Fatal("trigger nullability", i, nullable, known)
					}
				}
			}
			pgTriggerBoth(t, p, b, s)
			ids := []int64{26, 26, 26, 19, 26, 21, 18, 16, 26, 26, 26, 16, 16, 21, 22, 17, 194, 19, 19}
			attrs := make([][]any, len(ids))
			for i, id := range ids {
				collation, dimensions := int64(0), int64(0)
				if id == 19 || id == 194 {
					collation = 950
				}
				if i == 14 {
					dimensions = 1
				}
				attrs[i] = []any{int64(i + 1), pgTriggerColumns[i], id, i < 16, int64(-1), collation, dimensions}
			}
			pgTriggerBoth(t, p, b, fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid=2620 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attrs))
			pgTriggerBoth(t, p, b, fixture.Select("SELECT c.oid,c.relname,c.relnamespace,c.relkind,c.relhasindex,c.relnatts,t.typrelid=c.oid AS reciprocal FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid=2620", []string{"oid", "relname", "relnamespace", "relkind", "relhasindex", "relnatts", "reciprocal"}, []string{"OID", "NAME", "OID", "CHAR", "BOOL", "INT2", "BOOL"}, [][]any{{int64(2620), "pg_trigger", int64(11), "r", true, int64(19), true}}))
			pgTriggerExec(t, p, b, b.DB, "CREATE TABLE pgt_shape(a integer)")
			pgTriggerExec(t, p, b, b.DB, "CREATE FUNCTION pgt_shape_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RETURN NEW; END $$")
			probe := fixture.Select("SELECT tgname FROM pg_trigger WHERE tgrelid='pgt_shape'::regclass", []string{"tgname"}, []string{"NAME"}, [][]any{})
			pgTriggerBoth(t, p, b, probe)
			pgTriggerExec(t, p, b, b.DB, "CREATE RULE pgt_shape_rule AS ON DELETE TO pgt_shape DO NOTHING")
			pgTriggerBoth(t, p, b, probe)
			pgTriggerExec(t, p, b, b.DB, "CREATE TRIGGER pgt_shape_trigger BEFORE INSERT ON pgt_shape FOR EACH ROW EXECUTE FUNCTION pgt_shape_fn()")
			probe.Rows = [][]any{{"pgt_shape_trigger"}}
			pgTriggerBoth(t, p, b, probe)
			pgTriggerBoth(t, p, b, fixture.Select("SELECT relhastriggers FROM pg_class WHERE oid='pgt_shape'::regclass", []string{"relhastriggers"}, []string{"BOOL"}, [][]any{{true}}))
			pgTriggerExec(t, p, b, b.DB, "DROP TRIGGER pgt_shape_trigger ON pgt_shape")
			probe.Rows = [][]any{}
			pgTriggerBoth(t, p, b, probe)
		})
	}
}
