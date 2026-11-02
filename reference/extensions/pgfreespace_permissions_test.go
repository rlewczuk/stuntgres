package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgFreespacePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"fs_reader", "fs_owner"})
	fsCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Fix the SQL-body planning route explicitly; auto may retain a generic
		// body after earlier non-NULL calls even through a fresh outer statement.
		fsRun(t, p, b, c, fixture.Exec("SET plan_cache_mode=force_custom_plan", 0))
		defer c.ExecContext(p.Ctx, "RESET plan_cache_mode")
		for _, q := range []string{
			"CREATE ROLE fs_reader", "CREATE ROLE fs_owner",
			"GRANT USAGE ON SCHEMA fs_part TO fs_reader,fs_owner",
			"ALTER TABLE fs_part.heap OWNER TO fs_owner",
			"CREATE TABLE fs_part.empty(k integer)", "CREATE VIEW fs_part.v AS SELECT k FROM fs_part.heap",
			"CREATE TABLE fs_part.inputs(n text,z integer)", "GRANT SELECT ON fs_part.inputs TO fs_reader,fs_owner",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.inputs VALUES(NULL,0)", 1))
		fsRun(t, p, b, c, fixture.Exec("INSERT INTO fs_part.heap VALUES(1)", 1))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		queries := []string{
			"SELECT fs_part.pg_freespace($1::regclass,0) AS avail",
			"SELECT * FROM fs_part.pg_freespace($1::regclass) ORDER BY blkno",
		}
		held := make([]fixture.HeldStatement, len(queries))
		for i, q := range queries {
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		}
		nullSQL := "SELECT fs_part.pg_freespace(i.n::regclass,0) AS avail FROM fs_part.inputs i"
		nullStatement, err := c.PrepareContext(p.Ctx, nullSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer nullStatement.Close()
		runtimeNull := fixture.HeldStatement{Executor: c, Statement: nullStatement, SQL: nullSQL}
		var badHeld []fixture.HeldStatement
		for _, suffix := range []string{",0", ""} {
			q := "SELECT s.* FROM fs_part.inputs i CROSS JOIN LATERAL fs_part.pg_freespace(CASE WHEN i.z=0 THEN (1/i.z)::oid::regclass ELSE NULL::regclass END" + suffix + ") s"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			badHeld = append(badHeld, fixture.HeldStatement{Executor: c, Statement: st, SQL: q})
		}
		denied := func() {
			// Known NULL scalar operands use PostgreSQL's shared STRICT simplification;
			// this is not runtime permission to skip a row-dependent invocation.
			fsBoth(t, p, b, c, fixture.Select(queries[0], []string{"avail"}, []string{"INT2"}, [][]any{{nil}}, nil))
			for i, q := range queries {
				for _, target := range []any{nil, "fs_part.heap"} {
					if i == 0 && target == nil {
						continue
					}
					s := fsError(q, "42501", "permission denied for function pg_freespace", target)
					fsBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held[i], b.Recorder, s)
				}
			}
			s := fsError(runtimeNull.SQL, "42501", "permission denied for function pg_freespace")
			fsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, runtimeNull, b.Recorder, s)
			for _, h := range badHeld {
				s := fsError(h.SQL, "42501", "permission denied for function pg_freespace")
				fsBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			}
		}
		for _, role := range []string{"fs_owner", "fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			denied()
			fsRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		// The public default EXECUTE grant is revoked for both declarations.
		// Wrapper-only authority does not confer authority on its genuine inner
		// scalar, even when initialization will produce an empty range.
		fsRun(t, p, b, c, fixture.Exec("GRANT EXECUTE ON FUNCTION fs_part.pg_freespace(regclass) TO fs_reader", 0))
		fsRun(t, p, b, c, fixture.Exec("SET SESSION AUTHORIZATION fs_reader", 0))
		fsEmpty(t, p, b, c, nil)
		fixture.RunStep(t, p.Ctx, held[1], b.Recorder, fixture.Select(queries[1], fsNames, fsTypes, nil, nil))
		for _, target := range []any{"fs_part.empty", "fs_part.heap", "fs_part.v", int64(4294967294)} {
			s := fsError(queries[1], "42501", "permission denied for function pg_freespace", target)
			s.Diagnostic.Context = "SQL function \"pg_freespace\" statement 1"
			fsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[1], b.Recorder, s)
		}
		fsBoth(t, p, b, c, fixture.Select(queries[0], []string{"avail"}, []string{"INT2"}, [][]any{{nil}}, nil))
		fsRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		for _, q := range []string{
			"ALTER FUNCTION fs_part.pg_freespace(regclass,bigint) RENAME TO fs_scalar",
			"ALTER FUNCTION fs_part.pg_freespace(regclass) RENAME TO fs_wrapper",
			"SET SESSION AUTHORIZATION fs_reader",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, target := range []any{"fs_part.empty", "fs_part.heap", "fs_part.v", int64(4294967294)} {
			s := fsError("SELECT * FROM fs_part.fs_wrapper($1::regclass)", "42501", "permission denied for function fs_scalar", target)
			s.Diagnostic.Context = "SQL function \"fs_wrapper\" statement 1"
			fsBoth(t, p, b, c, s)
		}
		fsBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.fs_wrapper(NULL::regclass)", fsNames, fsTypes, nil))
		fsBoth(t, p, b, c, fixture.Select("SELECT fs_part.fs_scalar(NULL::regclass,0)", []string{"fs_scalar"}, []string{"INT2"}, [][]any{{nil}}))
		genericSQL := "SELECT * FROM fs_part.fs_wrapper($1::regclass) ORDER BY blkno"
		genericStatement, err := c.PrepareContext(p.Ctx, genericSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer genericStatement.Close()
		genericHeld := fixture.HeldStatement{Executor: c, Statement: genericStatement, SQL: genericSQL}
		fsRun(t, p, b, c, fixture.Exec("SET plan_cache_mode=force_generic_plan", 0))
		innerNull := fsError(genericSQL, "42501", "permission denied for function fs_scalar", nil)
		innerNull.Diagnostic.Context = "SQL function \"fs_wrapper\" statement 1"
		fsBoth(t, p, b, c, innerNull)
		fixture.RunStep(t, p.Ctx, genericHeld, b.Recorder, innerNull)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT EXECUTE ON FUNCTION fs_part.fs_scalar(regclass,bigint) TO fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		genericEmpty := fixture.Select(genericSQL, fsNames, fsTypes, nil, nil)
		fsBoth(t, p, b, c, genericEmpty)
		fixture.RunStep(t, p.Ctx, genericHeld, b.Recorder, genericEmpty)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION fs_part.fs_scalar(regclass,bigint) FROM fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION fs_part.fs_wrapper(regclass) FROM fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsBoth(t, p, b, c, fsError("SELECT * FROM fs_part.fs_wrapper(NULL::regclass)", "42501", "permission denied for function fs_wrapper"))
		fsBoth(t, p, b, c, fsError("SELECT * FROM fs_part.fs_wrapper('fs_part.empty')", "42501", "permission denied for function fs_wrapper"))
		outerNull := fsError(genericSQL, "42501", "permission denied for function fs_wrapper", nil)
		fsBoth(t, p, b, c, outerNull)
		fixture.RunStep(t, p.Ctx, genericHeld, b.Recorder, outerNull)
		fsRun(t, p, b, c, fixture.Exec("SET plan_cache_mode=force_custom_plan", 0))
		for _, q := range []string{
			"RESET SESSION AUTHORIZATION",
			"ALTER FUNCTION fs_part.fs_scalar(regclass,bigint) RENAME TO pg_freespace",
			"ALTER FUNCTION fs_part.fs_wrapper(regclass) RENAME TO pg_freespace",
			"REVOKE EXECUTE ON FUNCTION fs_part.pg_freespace(regclass) FROM fs_reader",
			"GRANT EXECUTE ON FUNCTION fs_part.pg_freespace(regclass,bigint) TO fs_reader",
			"SET SESSION AUTHORIZATION fs_reader",
		} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsProbe(t, p, b, c, "fs_part.empty", 0, int64(0))
		fsProbe(t, p, b, c, nil, -1, nil)
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, fixture.Select(queries[0], []string{"avail"}, []string{"INT2"}, [][]any{{int64(0)}}, "fs_part.empty"))
		fsBoth(t, p, b, c, fsError(queries[1], "42501", "permission denied for function pg_freespace", nil))
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE EXECUTE ON FUNCTION fs_part.pg_freespace(regclass,bigint) FROM fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		denied()
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT pg_stat_scan_tables TO fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsBoth(t, p, b, c, fsError("SELECT k FROM fs_part.heap", "42501", "permission denied for table heap"))
		fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
		fsProbe(t, p, b, c, nil, -1, nil)
		fsEmpty(t, p, b, c, nil)
		fsBoth(t, p, b, c, fixture.Select(runtimeNull.SQL, []string{"avail"}, []string{"INT2"}, [][]any{{nil}}))
		fixture.RunStep(t, p.Ctx, runtimeNull, b.Recorder, fixture.Select(runtimeNull.SQL, []string{"avail"}, []string{"INT2"}, [][]any{{nil}}))
		for _, h := range badHeld {
			s := fsError(h.SQL, "22012", "division by zero")
			fsBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, held[0], b.Recorder, fixture.Select(queries[0], []string{"avail"}, []string{"INT2"}, [][]any{{int64(0)}}, "fs_part.empty"))
		fixture.RunStep(t, p.Ctx, held[1], b.Recorder, fixture.Select(queries[1], fsNames, fsTypes, nil, "fs_part.empty"))
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "REVOKE pg_stat_scan_tables FROM fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		denied()
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "ALTER ROLE fs_reader NOINHERIT", "GRANT pg_stat_scan_tables TO fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		denied()
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "GRANT USAGE ON SCHEMA fs_part TO pg_stat_scan_tables", "SET SESSION AUTHORIZATION fs_reader", "SET ROLE pg_stat_scan_tables"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsCheck(t, p, b, c, "fs_part.heap", []int64{8096})
		for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE USAGE ON SCHEMA fs_part FROM pg_stat_scan_tables", "ALTER ROLE fs_reader INHERIT", "REVOKE pg_stat_scan_tables FROM fs_reader", "GRANT pg_stat_scan_tables TO fs_reader", "REVOKE USAGE ON SCHEMA fs_part FROM fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		s := fsError("SELECT * FROM fs_part.pg_freespace(NULL::regclass)", "42501", "permission denied for schema fs_part")
		s.Diagnostic.Position = "15"
		fsBoth(t, p, b, c, s)
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP OWNED BY fs_reader", "DROP ROLE fs_reader", "CREATE ROLE fs_reader", "GRANT USAGE ON SCHEMA fs_part TO fs_reader", "GRANT SELECT ON fs_part.inputs TO fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		denied()
		for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP EXTENSION pg_freespacemap", "GRANT CREATE ON SCHEMA fs_part TO fs_reader", "SET SESSION AUTHORIZATION fs_reader"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		s = fsError("CREATE EXTENSION pg_freespacemap SCHEMA fs_part", "42501", "permission denied to create extension \"pg_freespacemap\"")
		s.SelectRows = false
		s.Diagnostic.Hint = "Must be superuser to create this extension."
		fsRun(t, p, b, c, s)
		fsRun(t, p, b, c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
