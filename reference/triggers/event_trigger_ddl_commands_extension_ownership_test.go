package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// REL_18_6 extension.c returns the extension address for membership changes and
// real relocation; utility.c collects that address, not a synthetic command for
// each member. A same-schema relocation returns InvalidObjectAddress, but the
// matching ddl_command_end callback still executes with an empty command bag.
// These source-specified expectations require the pinned PostgreSQL proof too.
func TestReferenceExtensionOwnershipCommandInspection(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "et_dc_eo_old", "et_dc_eo_new")
			eventTriggerScenario(t, []string{"et_dc_eo_end"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				// Retire the global callback before extension/schema teardown, also
				// on assertion failure. The scenario retains its fallback cleanup.
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					for _, q := range []string{
						"DROP EVENT TRIGGER IF EXISTS et_dc_eo_end",
						"DROP EXTENSION IF EXISTS unaccent CASCADE",
					} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(err)
							return
						}
					}
				}()
				d := ddlCommandCase{t, p, b, c}
				d.exec(
					"CREATE SCHEMA et_dc_eo_old",
					"CREATE SCHEMA et_dc_eo_new",
					"CREATE EXTENSION unaccent SCHEMA et_dc_eo_old VERSION '1.1'",
					"CREATE FUNCTION et_dc_eo_old.member(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
					"CREATE TABLE et_dc_eo_calls(phase text,tag text,n bigint)",
					"CREATE TABLE et_dc_eo_bag(classid oid,objid oid,objsubid integer,command_tag text,object_type text,schema_name text,object_identity text,in_extension boolean,present boolean)",
					"CREATE FUNCTION et_dc_eo_capture() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_dc_eo_calls SELECT TG_EVENT,TG_TAG,count(*) FROM pg_event_trigger_ddl_commands(); INSERT INTO public.et_dc_eo_bag SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,command IS NOT NULL FROM pg_event_trigger_ddl_commands(); END$$",
				)
				ext := ddlCommandsStateOID(d, "SELECT oid FROM pg_extension WHERE extname='unaccent'")
				extOwner := ddlCommandsStateOID(d, "SELECT extowner FROM pg_extension WHERE extname='unaccent'")
				oldNS := ddlCommandsStateOID(d, "SELECT oid FROM pg_namespace WHERE nspname='et_dc_eo_old'")
				newNS := ddlCommandsStateOID(d, "SELECT oid FROM pg_namespace WHERE nspname='et_dc_eo_new'")
				member := ddlCommandsStateOID(d, "SELECT 'et_dc_eo_old.member(integer)'::regprocedure::oid")
				memberOwner := ddlCommandsStateOID(d, "SELECT proowner FROM pg_proc WHERE oid='et_dc_eo_old.member(integer)'::regprocedure")
				unaccent := ddlCommandsStateOID(d, "SELECT 'et_dc_eo_old.unaccent(text)'::regprocedure::oid")
				unaccentOwner := ddlCommandsStateOID(d, "SELECT proowner FROM pg_proc WHERE oid='et_dc_eo_old.unaccent(text)'::regprocedure")
				both := func(s fixture.Step) {
					d.run(s)
					d.run(fixture.Prepared(s))
				}
				var calls, bag [][]any
				checkLog := func() {
					callStep := fixture.Select("SELECT phase,tag,n FROM et_dc_eo_calls", []string{"phase", "tag", "n"}, []string{"TEXT", "TEXT", "INT8"}, calls)
					callStep.Bag = true
					both(callStep)
					bagStep := fixture.Select("SELECT classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_eo_bag", []string{"classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"}, []string{"OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, bag)
					bagStep.Bag = true
					both(bagStep)
				}
				checkState := func(schema string, namespace int64, attached bool) {
					both(fixture.Select("SELECT oid,extnamespace,extowner,extversion,extrelocatable,extconfig,extcondition FROM pg_extension WHERE extname='unaccent'", []string{"oid", "extnamespace", "extowner", "extversion", "extrelocatable", "extconfig", "extcondition"}, []string{"OID", "OID", "OID", "TEXT", "BOOL", "_OID", "_TEXT"}, [][]any{{ext, namespace, extOwner, "1.1", true, nil, nil}}))
					both(fixture.Select("SELECT oid,proname,pronamespace,proowner,proargtypes::text,prorettype,prosrc,probin FROM pg_proc WHERE oid=$1", []string{"oid", "proname", "pronamespace", "proowner", "proargtypes", "prorettype", "prosrc", "probin"}, []string{"OID", "NAME", "OID", "OID", "TEXT", "OID", "TEXT", "TEXT"}, [][]any{{member, "member", namespace, memberOwner, "23", int64(23), "SELECT $1", nil}}, member))
					both(fixture.Select("SELECT oid,proname,pronamespace,proowner,proargtypes::text,prorettype,prosrc,probin FROM pg_proc WHERE oid=$1", []string{"oid", "proname", "pronamespace", "proowner", "proargtypes", "prorettype", "prosrc", "probin"}, []string{"OID", "NAME", "OID", "OID", "TEXT", "OID", "TEXT", "TEXT"}, [][]any{{unaccent, "unaccent", namespace, unaccentOwner, "25", int64(25), "unaccent_dict", "$libdir/unaccent"}}, unaccent))
					// Check the whole namespace-normal-edge bag for these identities;
					// membership neither removes nor duplicates ordinary dependencies.
					normal := fixture.Select("SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE ((classid=1255 AND objid IN ($1,$2)) OR (classid=3079 AND objid=$3)) AND refclassid=2615 AND deptype='n'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"OID", "OID", "INT4", "OID", "OID", "INT4", "CHAR"}, [][]any{
						{int64(1255), member, int64(0), int64(2615), namespace, int64(0), "n"},
						{int64(1255), unaccent, int64(0), int64(2615), namespace, int64(0), "n"},
						{int64(3079), ext, int64(0), int64(2615), namespace, int64(0), "n"},
					}, member, unaccent, ext)
					normal.Bag = true
					both(normal)
					edges := [][]any{{int64(1255), unaccent, int64(0), int64(3079), ext, int64(0), "e"}}
					if attached {
						edges = append(edges, []any{int64(1255), member, int64(0), int64(3079), ext, int64(0), "e"})
					}
					membership := fixture.Select("SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE classid=1255 AND objid IN ($1,$2) AND deptype='e'", []string{"classid", "objid", "objsubid", "refclassid", "refobjid", "refobjsubid", "deptype"}, []string{"OID", "OID", "INT4", "OID", "OID", "INT4", "CHAR"}, edges, member, unaccent)
					membership.Bag = true
					both(membership)
					both(fixture.Select("SELECT "+schema+".member(7) AS ordinary,"+schema+".unaccent('Hôtel') AS installed", []string{"ordinary", "installed"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "Hotel"}}))
					both(fixture.Select("SELECT "+schema+".member(NULL::integer) AS ordinary,"+schema+".unaccent(NULL::text) AS installed", []string{"ordinary", "installed"}, []string{"INT4", "TEXT"}, [][]any{{nil, nil}}))
				}
				execute := func(query string, rows int64) {
					s := fixture.Exec(query, 0)
					s.Prepared = prepared
					d.run(s)
					calls = append(calls, []any{"ddl_command_end", "ALTER EXTENSION", rows})
					if rows == 1 {
						bag = append(bag, []any{int64(3079), ext, int64(0), "ALTER EXTENSION", "extension", nil, "unaccent", false, true})
					}
					checkLog()
				}
				checkState("et_dc_eo_old", oldNS, false)
				eventTriggerCreate(t, p, b, c, "et_dc_eo_end", "ddl_command_end", "'ALTER EXTENSION'", "et_dc_eo_capture")
				checkLog()
				execute("ALTER EXTENSION unaccent ADD FUNCTION et_dc_eo_old.member(integer)", 1)
				checkState("et_dc_eo_old", oldNS, true)
				duplicate := fixture.ErrorDiagnostic("ALTER EXTENSION unaccent ADD FUNCTION et_dc_eo_old.member(integer)", eventTriggerDiagnostic("55000", "function et_dc_eo_old.member(integer) is already a member of extension \"unaccent\""))
				duplicate.Prepared = prepared
				d.run(duplicate)
				checkLog()
				checkState("et_dc_eo_old", oldNS, true)
				// This moves the ordinary member too, but must not invent an ALTER
				// FUNCTION receipt for it or for the installed unaccent routines.
				execute("ALTER EXTENSION unaccent SET SCHEMA et_dc_eo_new", 1)
				checkState("et_dc_eo_new", newNS, true)
				execute("ALTER EXTENSION unaccent SET SCHEMA et_dc_eo_new", 0)
				checkState("et_dc_eo_new", newNS, true)
				execute("ALTER EXTENSION unaccent DROP FUNCTION et_dc_eo_new.member(integer)", 1)
				checkState("et_dc_eo_new", newNS, false)
			})
		})
	}
}
