package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// REL_18_6 pg_proc.dat fixes all nine OUT types. pseudotypes.c fixes the
// non-displayable/non-inputtable diagnostics; no expectation is learned natively.
func TestReferenceEventTriggerDDLCommandsOpaque(t *testing.T) {
	t.Run("metadata", func(t *testing.T) {
		eventTriggerScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			catalog := fixture.Select("SELECT oid,prorettype AS result,proallargtypes::text AS types,proargnames::text AS names FROM pg_proc WHERE oid=4568",
				[]string{"oid", "result", "types", "names"}, []string{"OID", "OID", "TEXT", "TEXT"},
				[][]any{{int64(4568), int64(2249), "{26,26,23,25,25,25,25,16,32}", "{classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command}"}})
			fixture.OidReferenceRows(t, p.Ctx, b, c, catalog)
			fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(catalog))
			// A constant-false source exposes RowDescription without invoking either
			// the context-dependent SRF or the opaque output function.
			for _, prepared := range []bool{false, true} {
				q := "SELECT * FROM pg_event_trigger_ddl_commands() WHERE false"
				var rows *sql.Rows
				var err error
				if prepared {
					stmt, e := c.PrepareContext(p.Ctx, q)
					if e != nil {
						t.Fatalf("metadata Prepare: %v", e)
					}
					defer stmt.Close()
					rows, err = stmt.QueryContext(p.Ctx)
				} else {
					rows, err = c.QueryContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatalf("metadata Execute prepared=%v: %v", prepared, err)
				}
				func() {
					defer rows.Close()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					names := []string{"classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "command"}
					types := []string{"OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "PG_DDL_COMMAND"}
					if len(columns) != len(names) {
						t.Fatalf("nine-column metadata: got %v", columns)
					}
					actual := make([]string, len(columns))
					for i, col := range columns {
						actual[i] = col.DatabaseTypeName()
					}
					for i, col := range columns {
						if col.Name() != names[i] || actual[i] != types[i] {
							t.Errorf("prepared=%v column %d name=%q type=%q; want %q/%q; complete driver type inventory=%q (OID32 spelling requires pinned-oracle resolution, never native inference)", prepared, i+1, col.Name(), actual[i], names[i], types[i], actual)
						}
					}
					if rows.Next() {
						t.Error("metadata-only source emitted a row")
					}
					if err := rows.Err(); err != nil {
						t.Fatalf("metadata iteration: %v", err)
					}
				}()
			}
		})
	})
	t.Run("input", func(t *testing.T) {
		eventTriggerScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, prepared := range []bool{false, true} {
				d := eventTriggerDiagnostic("0A000", "cannot accept a value of type pg_ddl_command")
				d.Position = fixture.Position(8)
				s := fixture.ErrorDiagnostic("SELECT 'x'::pg_ddl_command", d)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("values/prepared=%v", prepared), func(t *testing.T) {
			eventTriggerScenario(t, []string{"et_dc_opaque"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE et_dc_values(classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
					"CREATE FUNCTION et_dc_capture() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_values SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END$$",
				} {
					run(fixture.Exec(q, 0))
				}
				eventTriggerCreate(t, p, b, c, "et_dc_opaque", "ddl_command_end", "'CREATE TABLE','ALTER TABLE'", "et_dc_capture")
				for _, q := range []string{"CREATE TABLE et_dc_value_target(i integer)", "ALTER TABLE et_dc_value_target ADD COLUMN label text"} {
					s := fixture.Exec(q, 0)
					s.Prepared = prepared
					run(s)
				}
				s := fixture.Select("SELECT v.classid,v.objid=c.oid AS same_object,v.objsubid,v.command_tag,v.object_type,v.schema_name,v.object_identity,v.in_extension,v.present FROM et_dc_values v JOIN pg_class c ON c.relname='et_dc_value_target' AND c.relnamespace='public'::regnamespace ORDER BY v.command_tag",
					[]string{"classid", "same_object", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"},
					[]string{"OID", "BOOL", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"},
					[][]any{{int64(1259), true, int64(0), "ALTER TABLE", "table", "public", "public.et_dc_value_target", false, true}, {int64(1259), true, int64(0), "CREATE TABLE", "table", "public", "public.et_dc_value_target", false, true}})
				fixture.OidReferenceRows(t, p.Ctx, b, c, s)
				fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(s))
			})
		})
		for _, expression := range []string{"command::text", "d::text", "pg_event_trigger_ddl_commands()::text"} {
			t.Run(fmt.Sprintf("output/%s/prepared=%v", expression, prepared), func(t *testing.T) {
				eventTriggerScenario(t, []string{"et_dc_opaque"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					query := "SELECT " + expression + " FROM pg_event_trigger_ddl_commands() AS d"
					if expression == "pg_event_trigger_ddl_commands()::text" {
						query = "SELECT " + expression
					}
					body := "CREATE FUNCTION et_dc_output() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN PERFORM " + query[len("SELECT "):] + "; END$$"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(body, 0))
					eventTriggerCreate(t, p, b, c, "et_dc_opaque", "ddl_command_end", "'CREATE TABLE'", "et_dc_output")
					d := eventTriggerDiagnostic("0A000", "cannot display a value of type pg_ddl_command")
					d.Context = "SQL statement \"" + query + "\"\nPL/pgSQL function et_dc_output() line 1 at PERFORM"
					s := fixture.ErrorDiagnostic("CREATE TABLE et_dc_output_target(i integer)", d)
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_dc_output_target'", 0))
				})
			})
		}
	}
	t.Run("bundled_extension", ddlCommandsExtensionOracle)
}

// Bundled provider installation and ordinary member DDL are shared operations;
// arbitrary filesystem SQL installation scripts are an external mechanism.
func ddlCommandsExtensionOracle(t *testing.T) {
	eventTriggerScenario(t, []string{"et_dc_extension"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := c.ExecContext(ctx, "DROP EXTENSION IF EXISTS unaccent CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE et_dc_extension_audit(tag text,kind text,identity text,in_extension boolean,present boolean,classid oid,objid oid,subid integer,schema_name text)",
			"CREATE FUNCTION et_dc_extension_capture() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_extension_audit SELECT command_tag,object_type,object_identity,in_extension,command IS NOT NULL,classid,objid,objsubid,schema_name FROM pg_event_trigger_ddl_commands(); END$$",
		} {
			run(fixture.Exec(q, 0))
		}
		eventTriggerCreate(t, p, b, c, "et_dc_extension", "ddl_command_end", "'CREATE EXTENSION','ALTER FUNCTION'", "et_dc_extension_capture")
		run(fixture.Exec("CREATE EXTENSION unaccent VERSION '1.1'", 0))
		var memberOID string
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::text FROM pg_proc WHERE oid='public.unaccent(text)'::regprocedure").Scan(&memberOID); err != nil {
			t.Fatal(err)
		}
		run(fixture.Prepared(fixture.Exec("ALTER FUNCTION public.unaccent(text) RENAME TO et_member_renamed", 0)))
		s := fixture.Select("SELECT tag,kind,identity,in_extension,present,classid,subid,schema_name FROM et_dc_extension_audit ORDER BY tag",
			[]string{"tag", "kind", "identity", "in_extension", "present", "classid", "subid", "schema_name"}, []string{"TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "OID", "INT4", "TEXT"},
			[][]any{{"ALTER FUNCTION", "function", "public.et_member_renamed(pg_catalog.text)", false, true, int64(1255), int64(0), "public"}, {"CREATE EXTENSION", "extension", "unaccent", false, true, int64(3079), int64(0), nil}})
		fixture.OidReferenceRows(t, p.Ctx, b, c, s)
		fixture.OidReferenceRows(t, p.Ctx, b, c, fixture.Prepared(s))
		run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_extension_audit a JOIN pg_extension e ON a.objid=e.oid WHERE a.tag='CREATE EXTENSION' AND e.extname='unaccent'", 1))
		run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_extension_audit a JOIN pg_proc p ON a.objid=p.oid JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass JOIN pg_extension e ON d.refobjid=e.oid AND d.refclassid='pg_extension'::regclass WHERE a.tag='ALTER FUNCTION' AND p.proname='et_member_renamed' AND d.deptype='e' AND e.extname='unaccent'", 1))
		run(fixture.Select("SELECT objid::text AS oid FROM et_dc_extension_audit WHERE tag='ALTER FUNCTION'", []string{"oid"}, []string{"TEXT"}, [][]any{{memberOID}}))
	})
}
