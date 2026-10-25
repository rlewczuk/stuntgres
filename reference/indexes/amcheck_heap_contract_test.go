package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAmcheckHeapContract(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "heap_addon")
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		t.Run("absent", func(t *testing.T) {
			q := "SELECT * FROM verify_heapam('heap_t')"
			d := heapDiagnostic("42883", "function verify_heapam(unknown) does not exist")
			d.Hint, d.Position = heapMissingHint, heapPosition(q, "verify_heapam")
			heapBoth(t, p, b, c, heapError(q, d))
		})
		heapInstall(t, p, b, c)
		t.Run("declaration", func(t *testing.T) {
			heapBoth(t, p, b, c, fixture.Select(`SELECT e.extversion,e.extrelocatable,e.extowner=r.oid AS owned,n.nspname FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.rolname=current_user WHERE e.extname='amcheck'`,
				[]string{"extversion", "extrelocatable", "owned", "nspname"}, []string{"TEXT", "BOOL", "BOOL", "NAME"}, [][]any{{"1.5", true, true, "public"}}))
			s := fixture.Select(`SELECT p.proname,p.pronargs,p.prorettype::oid,p.proisstrict,p.provolatile,p.proparallel,p.proowner=e.extowner AS owned,p.pronamespace=e.extnamespace AS located FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE e.extname='amcheck' AND p.proname='verify_heapam'`,
				[]string{"proname", "pronargs", "prorettype", "proisstrict", "provolatile", "proparallel", "owned", "located"},
				[]string{"NAME", "INT2", "OID", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL"}, [][]any{{"verify_heapam", int64(6), int64(2249), false, "v", "u", true, true}})
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.OidReferenceRows(t, p.Ctx, b, c, s)
			}
			if b.PG {
				heapBoth(t, p, b, c, fixture.Select(`SELECT proargtypes::text,proallargtypes::text,proargmodes::text,proargnames::text,pronargdefaults,proretset,prosecdef FROM pg_proc WHERE oid='verify_heapam(regclass,boolean,boolean,text,bigint,bigint)'::regprocedure`,
					[]string{"proargtypes", "proallargtypes", "proargmodes", "proargnames", "pronargdefaults", "proretset", "prosecdef"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "INT2", "BOOL", "BOOL"},
					[][]any{{"2205 16 16 25 20 20", "{2205,16,16,25,20,20,20,23,23,25}", "{i,i,i,i,i,i,o,o,o,o}", "{relation,on_error_stop,check_toast,skip,startblock,endblock,blkno,offnum,attnum,msg}", int64(5), true, false}}))
			} else {
				heapBoth(t, p, b, c, fixture.Select(`SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.classid='pg_proc'::regclass AND d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid WHERE e.extname='amcheck'`, []string{"proname"}, []string{"NAME"}, [][]any{{"verify_heapam"}}))
				for _, name := range []string{"bt_index_check", "bt_index_parent_check", "bt_index_check_internal"} {
					heapBoth(t, p, b, c, fixture.Select("SELECT proname FROM pg_proc WHERE proname=$1", []string{"proname"}, []string{"NAME"}, nil, name))
				}
			}
		})
		for _, args := range []string{
			"'heap_t'", "'heap_t',false", "'heap_t',false,false", "'heap_t',false,false,'none'", "'heap_t',false,false,'NONE',NULL", "'heap_t',false,false,'NoNe',NULL,NULL",
			"relation=>'heap_t'", "skip=>'none',relation=>'heap_t',on_error_stop=>true", "'heap_t',check_toast=>false,endblock=>NULL,startblock=>NULL",
		} {
			t.Run("defaults/"+args, func(t *testing.T) { heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam("+args+")")) })
		}
		run(fixture.Exec("INSERT INTO heap_t VALUES(1,$1),(2,NULL)", 2, strings.Repeat("large text ", 2000)))
		run(fixture.Exec("UPDATE heap_t SET a=3 WHERE a=1", 1))
		run(fixture.Exec("DELETE FROM heap_t WHERE a=2", 1))
		for _, q := range []string{
			"SELECT * FROM verify_heapam('heap_t')", "SELECT r.* FROM verify_heapam('heap_t') AS r",
			"SELECT (verify_heapam('heap_t')).*",
			"SELECT (verify_heapam('heap_t')).blkno,(verify_heapam('heap_t')).offnum,(verify_heapam('heap_t')).attnum,(verify_heapam('heap_t')).msg",
			"SELECT r.* FROM (VALUES('heap_t'::regclass)) AS names(n) CROSS JOIN LATERAL verify_heapam(names.n) AS r",
			"SELECT a AS blkno,b AS offnum,c AS attnum,d AS msg FROM verify_heapam('heap_t') AS r(a,b,c,d)",
		} {
			t.Run("record/"+q, func(t *testing.T) { heapBoth(t, p, b, c, heapEmpty(q)) })
		}
		t.Run("target-and-ordinality", func(t *testing.T) {
			heapBoth(t, p, b, c, fixture.Select("SELECT verify_heapam('heap_t')", []string{"verify_heapam"}, []string{"RECORD"}, nil))
			heapBoth(t, p, b, c, fixture.Select("SELECT ordinality FROM verify_heapam('heap_t') WITH ORDINALITY", []string{"ordinality"}, []string{"INT8"}, nil))
		})
		for _, q := range []string{"CREATE SEQUENCE heap_seq", "CREATE TABLE heap_parent(a INT) PARTITION BY RANGE(a)", "CREATE TABLE heap_leaf PARTITION OF heap_parent FOR VALUES FROM(0) TO(10)", "CREATE MATERIALIZED VIEW heap_mv AS SELECT * FROM heap_t", "CREATE MATERIALIZED VIEW heap_empty_mv AS SELECT * FROM heap_t WITH NO DATA"} {
			if _, err := c.ExecContext(p.Ctx, q); err != nil { t.Fatal(q, err) }
		}
		for _, name := range []string{"heap_seq", "heap_leaf", "heap_mv", "heap_empty_mv"} {
			t.Run("target/"+name, func(t *testing.T) { heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam($1::regclass)", name)) })
		}
		t.Run("held-rebind", func(t *testing.T) {
			q := "SELECT * FROM verify_heapam($1::regclass)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, name := range []string{"heap_t", "heap_leaf", "heap_seq"} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, heapEmpty(q, name))
			}
			run(fixture.Exec("INSERT INTO heap_t VALUES(4,'new')", 1))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, heapEmpty(q, "heap_t"))
		})
		t.Run("own-version-roots", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO heap_t VALUES(8,NULL)", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("UPDATE heap_t SET b='own replacement' WHERE a=3", 1))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM heap_t WHERE a=4", 1))
			heapBoth(t, p, b, c, heapEmpty("SELECT * FROM verify_heapam('heap_t')"))
		})
		t.Run("volatile-named-order", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE SEQUENCE heap_evaluations", 0))
			for _, prepared := range []bool{false, true} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER SEQUENCE heap_evaluations RESTART WITH 1", 0))
				// Each volatile input must execute once in canonical parameter
				// order, even though the named arguments are written out of order.
				s := heapEmpty("SELECT * FROM verify_heapam(skip=>CASE WHEN nextval('heap_evaluations')=3 THEN 'none' ELSE 'invalid' END,relation=>CASE WHEN nextval('heap_evaluations')=1 THEN 'heap_t'::regclass ELSE NULL::regclass END,on_error_stop=>nextval('heap_evaluations')=2)")
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT currval('heap_evaluations') AS evaluations", []string{"evaluations"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
		})
		heapContractFailures(t, p, b, c)
		run(fixture.Exec("CREATE SCHEMA heap_addon", 0))
		run(fixture.Exec("DROP EXTENSION amcheck", 0))
		run(fixture.Exec("CREATE EXTENSION amcheck SCHEMA heap_addon", 0))
		heapBoth(t, p, b, c, heapEmpty("SELECT * FROM heap_addon.verify_heapam('heap_t')"))
	})
}

func heapContractFailures(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, tc := range []struct{ q, message string }{
		{"SELECT verify_heapam()", "function verify_heapam() does not exist"},
		{"SELECT verify_heapam(wrong=>'heap_t')", "function verify_heapam(wrong => unknown) does not exist"},
		{"SELECT verify_heapam('heap_t',false,false,'none',NULL,NULL,1)", "function verify_heapam(unknown, boolean, boolean, unknown, unknown, unknown, integer) does not exist"},
	} {
		t.Run("resolution/"+tc.q, func(t *testing.T) {
			d := heapDiagnostic("42883", tc.message)
			d.Hint, d.Position = heapMissingHint, heapPosition(tc.q, "verify_heapam")
			heapBoth(t, p, b, c, heapError(tc.q, d))
		})
	}
	for _, tc := range []struct{ q, code, message, token string }{
		{"SELECT * FROM verify_heapam('heap_missing'::regclass)", "42P01", `relation "heap_missing" does not exist`, "'heap_missing'"},
		{"SELECT * FROM verify_heapam(''::regclass)", "42602", "invalid name syntax", "''"},
		{"SELECT * FROM verify_heapam(0::oid::regclass)", "XX000", "could not open relation with OID 0", ""},
		{"SELECT verify_heapam(1::integer)", "XX000", "could not open relation with OID 1", ""},
		{"SELECT verify_heapam(relation=>'heap_t',relation=>'heap_t')", "42601", `argument name "relation" used more than once`, "relation=>'heap_t')"},
	} {
		t.Run("boundary/"+tc.q, func(t *testing.T) {
			d := heapDiagnostic(tc.code, tc.message)
			if tc.token != "" { d.Position = heapPosition(tc.q, tc.token) }
			heapBoth(t, p, b, c, heapError(tc.q, d))
		})
	}
}
