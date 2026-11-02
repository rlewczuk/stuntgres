package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgFreespaceIdentity(t *testing.T) {
	fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE SCHEMA fs_moved", "CREATE TABLE fs_moved.heap(k integer)",
			"CREATE TABLE fs_part.\"Quoted Heap\"(k integer)", "CREATE TABLE fs_part.\"123\"(k integer)", "CREATE TABLE fs_part.\"-\"(k integer)",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1)", 1))
		original := fsOID(t, p, c, "fs_part.heap")
		other := fsOID(t, p, c, "fs_moved.heap")
		if original == other {
			t.Fatal("different relations share identity", original)
		}
		for _, name := range []string{"fs_part.\"Quoted Heap\"", "fs_part.\"123\"", "fs_part.\"-\""} {
			fsRun(t, p, b, c, fixture.Exec("INSERT INTO "+name+" VALUES(1)", 1))
			fsCheck(t, p, b, c, name, []int64{8096})
		}
		for _, typ := range []string{"text::regclass", "regclass", "oid::regclass"} {
			arg := any("fs_part.heap")
			if typ == "oid::regclass" {
				arg = original
			}
			fsBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM fs_part.pg_freespace($1::"+typ+")", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, arg))
		}
		q := "SELECT count(*) AS n FROM fs_part.pg_freespace($1::oid::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		read := func(oid, n int64) {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{n}}, oid))
		}
		read(original, 1)
		read(other, 0)
		read(original, 1)
		for _, q := range []string{
			"CREATE VIEW fs_part.bound_scalar AS SELECT fs_part.pg_freespace('fs_part.heap'::regclass,0) AS avail",
			"CREATE VIEW fs_part.bound_source AS SELECT * FROM fs_part.pg_freespace('fs_part.heap'::regclass)",
			"CREATE VIEW fs_part.dynamic_source AS SELECT * FROM fs_part.pg_freespace('fs_part.heap'::text::regclass)",
			"CREATE TABLE fs_part.consumer(v smallint DEFAULT fs_part.pg_freespace('fs_part.heap'::regclass,0),CHECK(v>=0 AND fs_part.pg_freespace('fs_part.heap'::regclass,0)>=0))",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		// Fresh PostgreSQL heap pages without VACUUM have no FSM entry; the
		// native compact population instead has literal 8096-byte slack.
		avail := int64(8096)
		if b.PG {
			avail = 0
		}
		post := func() {
			fsBoth(t, p, b, c, fixture.Select("SELECT avail FROM fs_part.bound_scalar", []string{"avail"}, []string{"INT2"}, [][]any{{avail}}))
			fsBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.bound_source ORDER BY blkno", fsNames, fsTypes, [][]any{{int64(0), avail}}))
			fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.consumer DEFAULT VALUES", 1))
			fsBoth(t, p, b, c, fixture.Select("SELECT DISTINCT v FROM fs_part.consumer", []string{"v"}, []string{"INT2"}, [][]any{{avail}}))
		}
		post()
		for _, q := range []string{"ALTER TABLE fs_part.heap RENAME TO kept", "CREATE TABLE fs_part.heap(k integer)", "ALTER TABLE fs_part.kept SET SCHEMA fs_moved"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		if got := fsOID(t, p, c, "fs_moved.kept"); got != original {
			t.Fatal("rename changed relation identity", got, original)
		}
		post()
		read(original, 1)
		read(fsOID(t, p, c, "fs_part.heap"), 0)
		fsBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.dynamic_source ORDER BY blkno", fsNames, fsTypes, nil))
		fsRun(t, p, b, c, fixture.Exec("SET search_path=fs_part,pg_catalog", 0))
		defer c.ExecContext(p.Ctx, "RESET search_path")
		for _, name := range []string{"\"123\"", "\"-\"", "\"Quoted Heap\""} {
			fsBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_freespace($1::regclass)", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}, name))
		}
		for _, q := range []string{
			"ALTER FUNCTION fs_part.pg_freespace(regclass,bigint) RENAME TO fs_scalar",
			"ALTER FUNCTION fs_part.fs_scalar(regclass,bigint) SET SCHEMA fs_moved",
			"ALTER FUNCTION fs_part.pg_freespace(regclass) RENAME TO fs_wrapper",
			"ALTER FUNCTION fs_part.fs_wrapper(regclass) SET SCHEMA fs_moved",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
			post()
		}
		fsDependency(t, p, b, c, "fs_moved.fs_scalar(regclass,bigint)", "fs_moved.fs_wrapper(regclass)")
		// A new ordinary same-spelled routine must not capture the authenticated
		// wrapper body or stored scalar reference.
		fsRun(t, p, b, c, fixture.Exec("CREATE FUNCTION fs_part.pg_freespace(regclass,bigint) RETURNS smallint LANGUAGE SQL AS 'SELECT 77::smallint'", 0))
		fsBoth(t, p, b, c, fixture.Select("SELECT fs_part.pg_freespace('fs_part.heap',0) AS avail", []string{"avail"}, []string{"INT2"}, [][]any{{int64(77)}}))
		fsBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_moved.fs_wrapper($1::oid::regclass) ORDER BY blkno", fsNames, fsTypes, [][]any{{int64(0), avail}}, original))
		post()
		fsRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_freespacemap DROP FUNCTION fs_moved.fs_scalar(regclass,bigint)", 0))
		post()
		fsDependency(t, p, b, c, "fs_moved.fs_scalar(regclass,bigint)", "fs_moved.fs_wrapper(regclass)")
		fsRun(t, p, b, c, fixture.Exec("ALTER EXTENSION pg_freespacemap ADD FUNCTION fs_moved.fs_scalar(regclass,bigint)", 0))
		fsRun(t, p, b, c, fixture.Exec("DROP FUNCTION fs_part.pg_freespace(regclass,bigint)", 0))
		for _, q := range []string{
			"ALTER FUNCTION fs_moved.fs_scalar(regclass,bigint) RENAME TO pg_freespace",
			"ALTER FUNCTION fs_moved.fs_wrapper(regclass) RENAME TO pg_freespace",
			"ALTER FUNCTION fs_moved.pg_freespace(regclass,bigint) SET SCHEMA fs_part",
			"ALTER FUNCTION fs_moved.pg_freespace(regclass) SET SCHEMA fs_part",
			"ALTER EXTENSION pg_freespacemap SET SCHEMA fs_part",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
			post()
		}
		fsDependency(t, p, b, c, "fs_part.pg_freespace(regclass,bigint)", "fs_part.pg_freespace(regclass)")
	})
}
