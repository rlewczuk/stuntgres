package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

var pgRulesColumns = []string{"schemaname", "tablename", "rulename", "definition"}
var pgRulesTypes = []string{"NAME", "NAME", "NAME", "TEXT"}

const pgRulesProbe = "SELECT * FROM pg_catalog.pg_rules WHERE schemaname=$1 AND tablename=$2 AND rulename=$3"

func pgRulesCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, query string, want [][]any, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.PGTableViewSelect(b, query, pgRulesColumns, pgRulesTypes, want, args...)
		if prepared {
			s = fixture.Prepared(s)
		}
		fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
	}
}

func pgRulesRetained(t *testing.T, rows *sql.Rows, types []*sql.ColumnType, want []any) {
	t.Helper()
	defer rows.Close()
	fixture.PGTableViewMetadata(t, types, pgRulesColumns, pgRulesTypes, false)
	current, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	fixture.PGTableViewMetadata(t, current, pgRulesColumns, pgRulesTypes, false)
	if !rows.Next() {
		t.Fatal("retained rule missing", rows.Err())
	}
	var schema, table, rule, definition string
	if err := rows.Scan(&schema, &table, &rule, &definition); err != nil {
		t.Fatal(err)
	}
	if got := []any{schema, table, rule, definition}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retained rule: got %#v, want %#v", got, want)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained extra row/error", rows.Err())
	}
}

func TestReferencePGRulesShape(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			for _, q := range []string{"SELECT * FROM pg_catalog.pg_rules WHERE FALSE", "SELECT * FROM pg_catalog.pg_rules LIMIT 0"} {
				pgRulesCheck(t, p, b, b.DB, q, nil)
			}
			pgRulesCheck(t, p, b, b.DB, pgRulesProbe, nil, "public", "pgr_shape", "absent")
			pgRulesCheck(t, p, b, b.DB, pgRulesProbe, nil, nil, "pgr_shape", "r")
			run(fixture.Exec("CREATE TABLE pgr_shape(id INT)", 0))
			run(fixture.Exec("CREATE RULE r AS ON INSERT TO pgr_shape DO INSTEAD NOTHING", 0))
			pgRulesCheck(t, p, b, b.DB, pgRulesProbe, [][]any{{"public", "pgr_shape", "r", "CREATE RULE r AS\n    ON INSERT TO public.pgr_shape DO INSTEAD NOTHING;"}}, "public", "pgr_shape", "r")
			for _, prepared := range []bool{false, true} {
				steps := []fixture.Step{
					fixture.Select("SELECT attname,attnum,atttypid='name'::regtype AS is_name,atttypid='text'::regtype AS is_text FROM pg_attribute WHERE attrelid='pg_catalog.pg_rules'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "attnum", "is_name", "is_text"}, []string{"NAME", "INT2", "BOOL", "BOOL"}, [][]any{{"schemaname", int64(1), true, false}, {"tablename", int64(2), true, false}, {"rulename", int64(3), true, false}, {"definition", int64(4), false, true}}),
					fixture.Select("SELECT a.attname,c.collname FROM pg_attribute a JOIN pg_collation c ON c.oid=a.attcollation WHERE a.attrelid='pg_catalog.pg_rules'::regclass AND a.attnum>0 ORDER BY a.attnum", []string{"attname", "collname"}, []string{"NAME", "NAME"}, [][]any{{"schemaname", "C"}, {"tablename", "C"}, {"rulename", "C"}, {"definition", "default"}}),
					fixture.Select("SELECT c.relkind,t.typrelid=c.oid AS relation_link,a.typelem=t.oid AS element_link,t.typarray=a.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_rules'::regclass", []string{"relkind", "relation_link", "element_link", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", true, true, true}}),
					fixture.Select("SELECT viewname FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_rules'", []string{"viewname"}, []string{"NAME"}, [][]any{{"pg_rules"}}),
					fixture.Select("SELECT tablename FROM pg_tables WHERE schemaname='pg_catalog' AND tablename='pg_rules'", []string{"tablename"}, []string{"NAME"}, nil),
				}
				for _, s := range steps {
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
			}
		})
	}
}
