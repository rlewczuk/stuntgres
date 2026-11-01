package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func transitionMetadata(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, prefix string) {
	t.Helper()
	run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
	q := "SELECT tgname,tgoldtable,tgnewtable FROM pg_trigger WHERE tgrelid='" + prefix + "tt'::regclass ORDER BY tgname"
	s := fixture.Select(q, []string{"tgname", "tgoldtable", "tgnewtable"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"b", nil, nil}, {"d", "Old \" Rows", nil}, {"i", nil, "New ' Rows"}, {"u", "Old \" Rows", "New ' Rows"}})
	nameMetadata := func(names []string, nullable []bool) func(*testing.T, *sql.Rows) {
		return func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(t, b.PG, columns, names, []string{"NAME", "NAME", "NAME"}, nullable)
			for _, column := range columns {
				length, lengthOK := column.Length()
				precision, scale, decimalOK := column.DecimalSize()
				if length != 0 || lengthOK || precision != 0 || scale != 0 || decimalOK {
					t.Fatalf("NAME typmod: length=%d/%t decimal=%d,%d/%t", length, lengthOK, precision, scale, decimalOK)
				}
			}
		}
	}
	// Released pg_trigger.h: tgname is fixed/nonnullable; transition names
	// are nullable CATALOG_VARLEN attributes. pq exposes unknown Nullable.
	s.CheckMetadata = nameMetadata(s.Columns, []bool{false, true, true})
	run(s)
	run(fixture.Select("SELECT attname,attnotnull FROM pg_attribute WHERE attrelid='pg_trigger'::regclass AND attname IN('tgname','tgoldtable','tgnewtable') ORDER BY attname", []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, [][]any{{"tgname", true}, {"tgnewtable", false}, {"tgoldtable", false}}))
	schema := strings.TrimSuffix(prefix, ".")
	table := prefix + "tt"
	if schema == "" {
		schema, table = "public", "public.tt"
	}
	q = "SELECT trigger_name,action_reference_old_table,action_reference_new_table FROM information_schema.triggers WHERE event_object_schema='" + schema + "' AND event_object_table='tt' ORDER BY trigger_name"
	s.Query = q
	s.Columns = []string{"trigger_name", "action_reference_old_table", "action_reference_new_table"}
	// These are stored VIEW columns, which have no NOT NULL declaration.
	// Native publishes known nullable descriptors; pq still reports unknown.
	s.CheckMetadata = nameMetadata(s.Columns, []bool{true, true, true})
	run(s)
	run(fixture.Select("SELECT attname,attnotnull FROM pg_attribute WHERE attrelid='information_schema.triggers'::regclass AND attname IN('trigger_name','action_reference_old_table','action_reference_new_table') ORDER BY attname", []string{"attname", "attnotnull"}, []string{"NAME", "BOOL"}, [][]any{{"action_reference_new_table", false}, {"action_reference_old_table", false}, {"trigger_name", false}}))
	run(fixture.Select("SELECT pg_typeof(action_reference_old_table)::text AS old_type,pg_typeof(action_reference_new_table)::text AS new_type FROM information_schema.triggers WHERE event_object_schema='"+schema+"' AND event_object_table='tt' AND trigger_name='u'", []string{"old_type", "new_type"}, []string{"TEXT", "TEXT"}, [][]any{{"information_schema.sql_identifier", "information_schema.sql_identifier"}}))
	defs := [][]any{
		{"CREATE TRIGGER d AFTER DELETE ON " + table + " REFERENCING OLD TABLE AS \"Old \"\" Rows\" FOR EACH STATEMENT EXECUTE FUNCTION " + prefix + "tt_delete()"},
		{"CREATE TRIGGER i AFTER INSERT ON " + table + " REFERENCING NEW TABLE AS \"New ' Rows\" FOR EACH STATEMENT EXECUTE FUNCTION " + prefix + "tt_insert()"},
		{"CREATE TRIGGER u AFTER UPDATE ON " + table + " REFERENCING OLD TABLE AS \"Old \"\" Rows\" NEW TABLE AS \"New ' Rows\" FOR EACH STATEMENT EXECUTE FUNCTION " + prefix + "tt_update()"},
	}
	run(fixture.Select("SELECT pg_get_triggerdef(oid) AS definition FROM pg_trigger WHERE tgrelid='"+prefix+"tt'::regclass AND tgname IN('d','i','u') ORDER BY tgname", []string{"definition"}, []string{"TEXT"}, defs))
}

func TestReferenceTriggerTransitionMetadata(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"tt_metadata_reader"})
			triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				transitionSetup(t, p, b, c, "")
				run := func(s fixture.Step) { afterStep(t, p, b, c, prepared, s) }
				transitionMetadata(t, p, b, c, prepared, "")
				s := transitionRows("INSERT INTO tt(id,v) VALUES(1,NULL) RETURNING id,v,g", []any{int64(1), nil, nil})
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					fixture.DriverBasicMetadata(t, rows, b.PG, []string{"INT4", "INT4", "INT4"}, []bool{false, true, true})
				}
				run(s)
				s.Query, s.Rows = "UPDATE tt SET v=1 WHERE false RETURNING id,v,g", nil
				run(s)
				audit := transitionAudit("", []any{"INSERT", "NEW", int64(1), nil, nil})
				audit.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					fixture.DriverBasicMetadata(t, rows, b.PG, audit.Types, []bool{true, true, true, true, true})
				}
				run(audit)
				n := fixture.Select("SELECT count(*) AS n FROM tt_audit", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}})
				n.CheckMetadata = parametersMetadataNullability(b, "n", "INT8", false, !b.PG)
				run(n)
				userOperatorExec(t, p, b, c, "CREATE ROLE tt_metadata_reader", "GRANT USAGE ON SCHEMA public TO tt_metadata_reader", "GRANT SELECT ON tt TO tt_metadata_reader", "SET SESSION AUTHORIZATION tt_metadata_reader")
				defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
				visible := fixture.Select("SELECT trigger_name FROM information_schema.triggers WHERE event_object_schema='public' AND event_object_table='tt' ORDER BY trigger_name", []string{"trigger_name"}, []string{"NAME"}, nil)
				run(visible)
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT TRIGGER ON tt TO tt_metadata_reader", "SET SESSION AUTHORIZATION tt_metadata_reader")
				visible.Rows = [][]any{{"b"}, {"d"}, {"i"}, {"u"}}
				run(visible)
				userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
			})
		})
	}
}
