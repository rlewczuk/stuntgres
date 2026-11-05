package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const fileFDWExternalBoundary = "file_fdw server filesystem and program execution are not supported by the embedded engine"

func fileFDWBoundary(table string) fixture.Step {
	s := fileFDWError("SELECT id FROM "+table, "0A000", fileFDWExternalBoundary)
	s.SelectRows = true
	return s
}

func TestReferenceFileFDWControls(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		for _, q := range []string{
			"CREATE ROLE ff_owner SUPERUSER",
			"CREATE SCHEMA ff_helpers",
			"CREATE SCHEMA ff_moved",
			"CREATE TABLE ff_local(id integer,txt text,__stuntgres_retired_deadbeef_2 integer DEFAULT 42)",
			"CREATE FOREIGN TABLE ff_table(id integer,discarded text,txt text OPTIONS(force_null 'true',force_not_null 'off'),retired_tail integer) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-source',format 'csv',header 'match',delimiter ';',null 'NIL')",
			"ALTER TABLE ONLY ff_table ALTER COLUMN discarded SET STORAGE EXTERNAL",
			"ALTER FOREIGN TABLE ff_table DROP COLUMN discarded",
			"ALTER FOREIGN TABLE ff_table DROP COLUMN retired_tail",
			"CREATE TABLE ff_parent(id integer,discarded integer,txt text)",
			"CREATE FOREIGN TABLE ff_child(extra text OPTIONS(force_null 'true'),retired_tail integer) INHERITS(ff_parent) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-child')",
			"ALTER TABLE ff_parent DROP COLUMN discarded",
			"ALTER FOREIGN TABLE ff_child DROP COLUMN retired_tail",
			"ALTER FOREIGN TABLE ff_child ALTER COLUMN txt OPTIONS(force_null 'true',force_not_null 'off')",
			"GRANT SELECT ON ff_child TO PUBLIC",
			"CREATE TABLE ff_partition_parent(id integer,txt text) PARTITION BY RANGE(id)",
			"CREATE FOREIGN TABLE ff_compact PARTITION OF ff_partition_parent FOR VALUES FROM(0) TO(10) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-compact')",
			"ALTER FOREIGN TABLE ff_compact ALTER COLUMN txt OPTIONS(force_null 'true',force_not_null 'off')",
			"CREATE FOREIGN TABLE ff_attached(id integer,discarded integer,txt text) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-attached')",
			"ALTER FOREIGN TABLE ff_attached DROP COLUMN discarded",
			"ALTER TABLE ff_partition_parent ATTACH PARTITION ff_attached FOR VALUES FROM(10) TO(20)",
			"ALTER FOREIGN TABLE ff_attached ALTER COLUMN txt OPTIONS(force_null 'true',force_not_null 'off')",
			"ALTER TABLE ONLY ff_parent ALTER COLUMN txt SET STORAGE EXTERNAL",
			"ALTER TABLE ONLY ff_parent ALTER COLUMN txt SET COMPRESSION pglz",
			"ALTER TABLE ONLY ff_child ALTER COLUMN txt SET STORAGE MAIN",
			"ALTER FOREIGN TABLE ff_table OWNER TO ff_owner",
			"ALTER SERVER ff_server OWNER TO ff_owner",
			"ALTER EXTENSION file_fdw SET SCHEMA ff_moved",
			"ALTER FUNCTION ff_moved.file_fdw_validator(text[],oid) RENAME TO ff_validator",
			"ALTER FUNCTION ff_moved.ff_validator(text[],oid) SET SCHEMA ff_helpers",
			"ALTER FUNCTION ff_helpers.ff_validator(text[],oid) OWNER TO ff_owner",
			"ALTER FUNCTION ff_moved.file_fdw_handler() OWNER TO ff_owner",
			"ALTER FOREIGN DATA WRAPPER file_fdw RENAME TO ff_wrapper",
			"ALTER SERVER ff_server RENAME TO ff_renamed_server",
			"ALTER FOREIGN TABLE ff_table RENAME TO ff_renamed_table",
			"ALTER FOREIGN TABLE ff_renamed_table OPTIONS(SET filename '/not-a-real-filefdw-renamed')",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fileFDWError("ALTER FOREIGN TABLE ff_partition_parent ALTER COLUMN txt OPTIONS(force_null 'true')", "42809", `"ff_partition_parent" is not a foreign table`))
		run(fixture.Exec("INSERT INTO ff_local(id,txt) VALUES(7,'kept'),(8,NULL)", 2))
		run(fixture.Exec("INSERT INTO ff_parent VALUES(9,'parent')", 1))
		hierarchy := func(execute func(fixture.Step), later bool) {
			parent := [][]any{{"id", int64(1)}, {"txt", int64(3)}}
			child := [][]any{{"id", int64(1), nil}, {"txt", int64(3), "{force_null=true,force_not_null=off}"}, {"extra", int64(4), "{force_null=true}"}}
			slots := [][]any{{int64(1), false}, {int64(2), true}, {int64(3), false}, {int64(4), false}, {int64(5), true}}
			if later {
				parent = append(parent, []any{"later", int64(4)})
				child = append(child, []any{"later", int64(6), nil})
				slots = append(slots, []any{int64(6), false})
			}
			execute(fixture.Select("SELECT attname,attnum FROM pg_attribute WHERE attrelid='ff_parent'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum"}, []string{"NAME", "INT2"}, parent))
			execute(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_child'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, child))
			execute(fixture.Select("SELECT attnum,attisdropped FROM pg_attribute WHERE attrelid='ff_child'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attisdropped"}, []string{"INT2", "BOOL"}, slots))
			execute(fixture.Select("SELECT count(*) FROM pg_inherits WHERE inhrelid='ff_child'::regclass AND inhparent='ff_parent'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
			execute(fixture.Select("SELECT c.relname,a.attname,a.attnum,a.attfdwoptions FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.oid IN('ff_compact'::regclass,'ff_attached'::regclass) AND a.attnum>0 AND NOT a.attisdropped ORDER BY c.relname,a.attnum", []string{"relname", "attname", "attnum", "attfdwoptions"}, []string{"NAME", "NAME", "INT2", "_TEXT"}, [][]any{{"ff_attached", "id", int64(1), nil}, {"ff_attached", "txt", int64(3), "{force_null=true,force_not_null=off}"}, {"ff_compact", "id", int64(1), nil}, {"ff_compact", "txt", int64(2), "{force_null=true,force_not_null=off}"}}))
			execute(fixture.Select("SELECT count(*) FROM pg_inherits WHERE inhparent='ff_partition_parent'::regclass AND inhrelid IN('ff_compact'::regclass,'ff_attached'::regclass)", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
			execute(fixture.Select("SELECT has_table_privilege('pg_monitor','ff_child','SELECT') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}))
			execute(fixture.Select("SELECT c.relname,a.attstorage::text,a.attcompression::text FROM pg_class c JOIN pg_attribute a ON a.attrelid=c.oid WHERE c.oid IN('ff_parent'::regclass,'ff_child'::regclass) AND a.attname='txt' ORDER BY c.relname", []string{"relname", "attstorage", "attcompression"}, []string{"NAME", "TEXT", "TEXT"}, [][]any{{"ff_child", "m", ""}, {"ff_parent", "e", "p"}}))
			if later {
				execute(fixture.Select("SELECT id,txt,later FROM ONLY ff_parent", []string{"id", "txt", "later"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(9), "parent", nil}}))
			} else {
				execute(fixture.Select("SELECT id,txt FROM ONLY ff_parent", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(9), "parent"}}))
			}
		}
		// Source-shaped inheritance must retire the shared slot only after
		// both genuine constructors, without inventing a foreign Rows phase.
		hierarchy(run, false)
		if !b.PG {
			var sourceShape bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &sourceShape); err != nil { t.Fatal(err) }
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(sourceShape.String())); err != nil { t.Fatal(err) }
			hierarchy(run, false)
		}
		// This inherited column follows the child's local slots4/5. Rebuilding
		// a simple parent-first list would incorrectly compact/reorder it.
		run(fixture.Exec("ALTER TABLE ff_parent ADD COLUMN later integer", 0))
		state := func(run func(fixture.Step)) {
			hierarchy(run, true)
			fileFDWGraph(run, "ff_wrapper", "ff_moved.file_fdw_handler()", "ff_helpers.ff_validator(text[],oid)")
			run(fileFDWOptions("ff_renamed_table", "filename=/not-a-real-filefdw-renamed", "format=csv", "header=match", "delimiter=;", "null=NIL"))
			run(fixture.Select("SELECT c.relname,r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid='ff_renamed_table'::regclass", []string{"relname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"ff_renamed_table", "ff_owner"}}))
			run(fixture.Select("SELECT s.srvname,r.rolname,w.fdwname FROM pg_foreign_server s JOIN pg_roles r ON r.oid=s.srvowner JOIN pg_foreign_data_wrapper w ON w.oid=s.srvfdw WHERE s.srvname='ff_renamed_server'", []string{"srvname", "rolname", "fdwname"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"ff_renamed_server", "ff_owner", "ff_wrapper"}}))
			run(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_renamed_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"id", int64(1), nil}, {"txt", int64(3), "{force_null=true,force_not_null=off}"}}))
			run(fixture.Select("SELECT attnum,attisdropped FROM pg_attribute WHERE attrelid='ff_renamed_table'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attisdropped"}, []string{"INT2", "BOOL"}, [][]any{{int64(1), false}, {int64(2), true}, {int64(3), false}, {int64(4), true}}))
			run(fixture.Select("SELECT attnum,attstorage::text,attcompression::text FROM pg_attribute WHERE attrelid='ff_renamed_table'::regclass AND attnum=2", []string{"attnum", "attstorage", "attcompression"}, []string{"INT2", "TEXT", "TEXT"}, [][]any{{int64(2), "e", ""}}))
			run(fixture.Select("SELECT count(*) FROM pg_attribute WHERE attrelid IN('ff_renamed_table'::regclass,'ff_parent'::regclass,'ff_child'::regclass,'ff_compact'::regclass,'ff_attached'::regclass) AND NOT attisdropped AND attname LIKE '__stuntgres_retired_%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			run(fixture.Select("SELECT p.proname,r.rolname,p.prosrc,p.probin FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid IN('ff_moved.file_fdw_handler()'::regprocedure,'ff_helpers.ff_validator(text[],oid)'::regprocedure) ORDER BY p.proname", []string{"proname", "rolname", "prosrc", "probin"}, []string{"NAME", "NAME", "TEXT", "TEXT"}, [][]any{{"ff_validator", "ff_owner", "file_fdw_validator", "$libdir/file_fdw"}, {"file_fdw_handler", "ff_owner", "file_fdw_handler", "$libdir/file_fdw"}}))
			run(fixture.Select("SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='file_fdw'", []string{"nspname"}, []string{"NAME"}, [][]any{{"ff_moved"}}))
			run(fixture.Select("SELECT id,txt FROM ff_local ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), nil}}))
			run(fixture.Select("SELECT __stuntgres_retired_deadbeef_2 FROM ff_local ORDER BY id", []string{"__stuntgres_retired_deadbeef_2"}, []string{"INT4"}, [][]any{{int64(42)}, {int64(42)}}))
			run(fileFDWVoid("SELECT ff_helpers.ff_validator(ARRAY['filename=/x'],3118::oid) AS validated"))
		}
		state(run)
		if b.PG {
			return // Only the explicit native control phase lacks a PG counterpart.
		}
		q := "SELECT ff_helpers.ff_validator(ARRAY[$1::text],3118::oid) AS validated"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		heldStatement := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, fileFDWVoid(q, "filename=/x"))
		rowsQuery := "SELECT id,txt FROM ff_local ORDER BY id"
		heldRows := fdwHeld(t, p, b, c, rowsQuery)
		defer heldRows.rows.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if dump.Len() == 0 {
			t.Fatal("empty file_fdw dump")
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='file_fdw'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		state(run)
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, fileFDWVoid(q, "filename=/x"))
		fixture.RunStep(t, p.Ctx, heldRows, heldRows.recorder, fixture.Select(rowsQuery, []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), nil}}))
		run(fileFDWBoundary("ff_renamed_table"))
		var repeated bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &repeated); err != nil { t.Fatal(err) }
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(repeated.String())); err != nil { t.Fatal(err) }
		state(run)
		fixture.RunStep(t, p.Ctx, heldStatement, b.Recorder, fileFDWVoid(q, "filename=/x"))
		invalid := dump.String() + "\nCREATE TABLE ff_staged(v integer); CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_renamed_server OPTIONS(format 'INVALID');"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid))
		fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("22023", `COPY format "INVALID" not recognized`))
		state(run)
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('ff_staged','ff_rejected')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		brokenPair := dump.String() + "\nCREATE TABLE ff_pair_staged(v integer); ALTER TABLE ONLY public.ff_renamed_table DROP COLUMN __stuntgres_retired_deadbeef_99;"
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(brokenPair))
		fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("0A000", "restore accepts only paired generated attribute constructors"))
		state(run)
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='ff_pair_staged'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, completion := range []string{
			"ALTER FOREIGN TABLE public.ff_child ALTER COLUMN txt OPTIONS(SET force_null 'false')",
			"ALTER FOREIGN TABLE public.ff_child ALTER COLUMN later OPTIONS(force_null 'false')",
		} {
			bad := dump.String() + "\nCREATE TABLE ff_options_staged(v integer); " + completion + ";"
			err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad))
			fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("0A000", "restore accepts only paired generated foreign column options"))
			state(run)
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='ff_options_staged'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		// A genuine fresh completion still executes the real validator and
		// real owner checks, rather than trusting a parsed option or role name.
		prefix := "CREATE EXTENSION file_fdw; CREATE SERVER ff_new_server FOREIGN DATA WRAPPER file_fdw; CREATE SCHEMA ff_owner_namespace AUTHORIZATION pg_monitor; CREATE TABLE ff_owner_namespace.ff_new_parent(id integer,txt text); CREATE FOREIGN TABLE ff_owner_namespace.ff_new_child() INHERITS(ff_owner_namespace.ff_new_parent) SERVER ff_new_server OPTIONS(filename '/not-a-real-completion-source'); "
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"ALTER FOREIGN TABLE ff_owner_namespace.ff_new_child ALTER COLUMN txt OPTIONS(force_null 'not-a-Boolean');"))
		fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("42601", "force_null requires a Boolean value"))
		state(run)
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(prefix+"SET SESSION AUTHORIZATION 'pg_monitor'; ALTER FOREIGN TABLE ff_owner_namespace.ff_new_child ALTER COLUMN txt OPTIONS(force_null 'true');"))
		fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("42501", "must be owner of foreign table ff_new_child"))
		state(run)
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN('ff_new_parent','ff_new_child')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='ff_owner_namespace'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='ff_new_server'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		// A fresh Driver has only its genuine stock registry. No source process
		// handles, custom registration aliases or serialized foreign rows exist.
		d := stuntgres.NewDriver()
		connector, err := d.OpenConnector("file-fdw-independent-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination, recorder := fixture.RecordedNativePool(t, connector)
		defer destination.Close()
		control := sql.OpenDB(connector)
		defer control.Close()
		targetRun := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, destination, recorder, s) }
		targetRun(fixture.Exec("CREATE ROLE ff_owner", 0))
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump.String())); err != nil {
			t.Fatal("independent stock provider replay:", err)
		}
		state(targetRun)
		targetRun(fileFDWBoundary("ff_renamed_table"))
		// A trailing retired slot must survive replay, not merely the visible
		// gap before txt. Both destinations allocate their next column at5.
		for _, execute := range []func(fixture.Step){run, targetRun} {
			execute(fixture.Exec("ALTER FOREIGN TABLE ff_renamed_table ADD COLUMN after_restore integer", 0))
			execute(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='ff_renamed_table'::regclass AND attname='after_restore'", []string{"attnum"}, []string{"INT2"}, [][]any{{int64(5)}}))
			execute(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_renamed_table'::regclass AND attnum IN(3,5) ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"txt", int64(3), "{force_null=true,force_not_null=off}"}, {"after_restore", int64(5), nil}}))
			execute(fixture.Exec("ALTER FOREIGN TABLE ff_child ADD COLUMN child_after_restore integer", 0))
			execute(fixture.Select("SELECT attnum FROM pg_attribute WHERE attrelid='ff_child'::regclass AND attname='child_after_restore'", []string{"attnum"}, []string{"INT2"}, [][]any{{int64(7)}}))
			execute(fixture.Select("SELECT count(*) FROM pg_class WHERE relname LIKE '__stuntgres_attribute_history_%'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
	})
}

func TestReferenceFileFDWBoundaries(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		run(fixture.Exec("CREATE SCHEMA ff_destination", 0))
		run(fileFDWError("IMPORT FOREIGN SCHEMA irrelevant_source FROM SERVER ff_server INTO ff_destination", "HV00P", `foreign-data wrapper "file_fdw" does not support IMPORT FOREIGN SCHEMA`))
		run(fixture.Select("SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='ff_destination'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_table(id integer) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-source')", 0))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_program(id integer) SERVER ff_server OPTIONS(program 'not-a-real-filefdw-program')", 0))
		run(fixture.Select("SELECT count(*) FROM pg_user_mapping m JOIN pg_foreign_server s ON s.oid=m.umserver WHERE s.srvname='ff_server'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if !b.PG {
			run(fileFDWBoundary("ff_table"))
			run(fileFDWBoundary("ff_program"))
		}
	})
}

func TestReferenceFileFDWFeatureSmoke(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		fileFDWGraph(run, "file_fdw", "public.file_fdw_handler()", "public.file_fdw_validator(text[],oid)")
		run(fixture.Exec("CREATE TABLE ff_local(id integer,txt text)", 0))
		run(fixture.Exec("INSERT INTO ff_local VALUES(7,'kept'),(8,NULL)", 2))
		run(fixture.Exec("CREATE FOREIGN TABLE ff_table(id integer,txt text OPTIONS(force_null 'true')) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-smoke',format 'csv',header 'match',delimiter ';',null 'NIL')", 0))
		options := func() { run(fileFDWOptions("ff_table", "filename=/not-a-real-filefdw-smoke", "format=csv", "header=match", "delimiter=;", "null=NIL")) }
		options()
		run(fileFDWError("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server", "HV002", "either filename or program is required for file_fdw foreign tables"))
		run(fileFDWError("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server OPTIONS(filename '/x',program 'not-a-real-filefdw-program')", "42601", "conflicting or redundant options"))
		fileFDWAbsent(run)
		run(fileFDWError("ALTER FOREIGN TABLE ff_table ADD COLUMN earlier integer,OPTIONS(ADD quote ';')", "22023", "COPY delimiter and quote must be different"))
		options()
		run(fixture.Exec("CREATE ROLE ff_actor", 0))
		run(fixture.Exec("GRANT CREATE,USAGE ON SCHEMA public TO ff_actor", 0))
		run(fixture.Exec("GRANT USAGE ON FOREIGN SERVER ff_server TO ff_actor", 0))
		run(fixture.Exec("SET ROLE ff_actor", 0))
		run(fileFDWDenied("CREATE FOREIGN TABLE ff_rejected(id integer) SERVER ff_server OPTIONS(filename '/x')", "filename"))
		run(fixture.Exec("RESET ROLE", 0))
		fileFDWAbsent(run)
		// Remove explicit ACLs before Dump; generic FDW ACL serialization is
		// outside this configuration feature's existing control contract.
		run(fixture.Exec("REVOKE USAGE ON FOREIGN SERVER ff_server FROM ff_actor", 0))
		run(fixture.Exec("REVOKE CREATE,USAGE ON SCHEMA public FROM ff_actor", 0))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_table OPTIONS(SET header 'true')", 0))
		run(fileFDWVoid("SELECT file_fdw_validator(ARRAY[$1::text],3118::oid) AS validated", "filename=/x"))
		run(fixture.Select("SELECT file_fdw_validator($1::text[],3118::oid) AS validated", []string{"validated"}, []string{"VOID"}, [][]any{{nil}}, nil))
		run(fixture.Exec("CREATE SCHEMA ff_destination", 0))
		run(fileFDWError("IMPORT FOREIGN SCHEMA irrelevant_source FROM SERVER ff_server INTO ff_destination", "HV00P", `foreign-data wrapper "file_fdw" does not support IMPORT FOREIGN SCHEMA`))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			fileFDWGraph(run, "file_fdw", "public.file_fdw_handler()", "public.file_fdw_validator(text[],oid)")
			run(fileFDWOptions("ff_table", "filename=/not-a-real-filefdw-smoke", "format=csv", "header=true", "delimiter=;", "null=NIL"))
			run(fileFDWVoid("SELECT file_fdw_validator(ARRAY['filename=/x'],3118::oid) AS validated"))
			run(fileFDWBoundary("ff_table"))
		}
		run(fixture.Select("SELECT attname,attfdwoptions FROM pg_attribute WHERE attrelid='ff_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attfdwoptions"}, []string{"NAME", "_TEXT"}, [][]any{{"id", nil}, {"txt", "{force_null=true}"}}))
		run(fixture.Select("SELECT id,txt FROM ff_local ORDER BY id", []string{"id", "txt"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}, {int64(8), nil}}))
	})
}
