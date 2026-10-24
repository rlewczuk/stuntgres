package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexMaintenanceOptions(t *testing.T) {
	for _, method := range []string{"btree", "hash", "gist"} {
		t.Run(method, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, a)
					run(fixture.Exec("CREATE TABLE t(n INT,p INT4RANGE)", 0))
					run(fixture.Exec("INSERT INTO t VALUES(1,'[1,2)'),(1,'[1,2)'),(NULL,NULL)", 3))
					key := "n"
					if method == "gist" {
						key = "p"
					}
					run(fixture.Exec("CREATE INDEX ix ON t USING "+method+"("+key+")", 0))
					var oid int64
					if err := a.QueryRowContext(p.Ctx, "SELECT 'ix'::regclass::oid::bigint").Scan(&oid); err != nil {
						t.Fatal(err)
					}
					check := func(options any, suffix string) {
						run(fixture.Select("SELECT oid=$1::oid AS same,reloptions::text,pg_get_indexdef(oid) FROM pg_class WHERE relname='ix'", []string{"same", "reloptions", "pg_get_indexdef"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, options, "CREATE INDEX ix ON public.t USING " + method + " (" + key + ")" + suffix}}, oid))
						run(fixture.Select("SELECT indexdef FROM pg_indexes WHERE schemaname='public' AND indexname='ix'", []string{"indexdef"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ix ON public.t USING " + method + " (" + key + ")" + suffix}}))
					}
					check(nil, "")
					run(fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					q := "ALTER INDEX ix SET(fillfactor=80.0)"
					if method == "btree" {
						q = "ALTER INDEX ix SET(fillfactor=80.0,deduplicate_items=off)"
					}
					stmt, err := a.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
					options, suffix := "{fillfactor=80.0}", " WITH (fillfactor='80.0')"
					if method == "btree" {
						options, suffix = "{fillfactor=80.0,deduplicate_items=off}", " WITH (fillfactor='80.0', deduplicate_items=off)"
					}
					check(options, suffix)
					transactionalIndexRun(t, p, b, peer)(fixture.Select("SELECT reloptions::text FROM pg_class WHERE relname='ix'", []string{"reloptions"}, []string{"TEXT"}, [][]any{{nil}}))
					run(fixture.Exec("ALTER INDEX ix SET(fillfactor=75)", 0))
					options, suffix = "{fillfactor=75}", " WITH (fillfactor='75')"
					if method == "btree" {
						options, suffix = "{deduplicate_items=off,fillfactor=75}", " WITH (deduplicate_items=off, fillfactor='75')"
					}
					check(options, suffix)
					run(fixture.Exec("SAVEPOINT valid", 0))
					run(fixture.ImplicitError("ALTER INDEX ix SET(fillfactor=70,unknown=1)", fixture.Diagnostic{Code: "22023", Message: `unrecognized parameter "unknown"`}))
					run(fixture.ImplicitError("ALTER INDEX ix RESET(fillfactor)", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
					run(fixture.Exec("ROLLBACK TO valid", 0))
					check(options, suffix)
					for _, q := range []string{"ALTER INDEX ix RESET(unknown,unknown)", "ALTER INDEX ix SET(toast.fillfactor=80)", "ALTER INDEX ix RESET(toast.fillfactor)"} {
						run(fixture.Exec(q, 0))
						check(options, suffix)
					}
					run(fixture.Exec("ALTER INDEX ix RESET(fillfactor)", 0))
					if method == "btree" {
						check("{deduplicate_items=off}", " WITH (deduplicate_items=off)")
						run(fixture.Exec("ALTER INDEX ix RESET(deduplicate_items)", 0))
					}
					check(nil, "")
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
					run(fixture.Exec("INSERT INTO t VALUES(1,'[2,3)'),(NULL,NULL)", 2))
				})
			}
		})
	}
}

func TestReferenceTransactionalIndexMaintenanceIdentityDependencies(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
			run := transactionalIndexRun(t, p, b, a)
			for _, q := range []string{"CREATE TABLE t(n INT CONSTRAINT u UNIQUE NULLS NOT DISTINCT,s TEXT, active BOOLEAN)", "CREATE UNIQUE INDEX partial ON t(s) WHERE active", "CREATE INDEX decorated ON t(lower(s) text_pattern_ops,n DESC NULLS LAST) INCLUDE(active)", "INSERT INTO t VALUES(1,'a',true),(2,'a',false),(NULL,NULL,false)"} {
				count := int64(0)
				if q == "INSERT INTO t VALUES(1,'a',true),(2,'a',false),(NULL,NULL,false)" {
					count = 3
				}
				run(fixture.Exec(q, count))
			}
			var indexOID, constraintOID int64
			if err := a.QueryRowContext(p.Ctx, "SELECT conindid::bigint,oid::bigint FROM pg_constraint WHERE conname='u' AND conrelid='t'::regclass").Scan(&indexOID, &constraintOID); err != nil {
				t.Fatal(err)
			}
			var decorated string
			if err := a.QueryRowContext(p.Ctx, "SELECT pg_get_indexdef('decorated'::regclass)").Scan(&decorated); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("BEGIN", 0))
			defer a.ExecContext(p.Ctx, "ROLLBACK")
			for _, q := range []string{"ALTER INDEX u RENAME TO renamed_u", "ALTER INDEX renamed_u SET(fillfactor=80,deduplicate_items=off)", "ALTER INDEX renamed_u RESET(fillfactor,deduplicate_items)", "ALTER INDEX decorated RENAME TO decorated_new", "ALTER INDEX decorated_new SET(fillfactor=70)", "ALTER INDEX decorated_new RESET(fillfactor)", "DROP INDEX partial"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Select("SELECT oid=$1::oid AS same,conindid=$2::oid AS same_index,conname FROM pg_constraint WHERE conrelid='t'::regclass AND conname='renamed_u'", []string{"same", "same_index", "conname"}, []string{"BOOL", "BOOL", "NAME"}, [][]any{{true, true, "renamed_u"}}, constraintOID, indexOID))
			run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid=$1::oid", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, indexOID))
			run(fixture.Exec("INSERT INTO t VALUES(3,'a',true)", 1))
			run(fixture.Exec("SAVEPOINT enforce", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(1,'other',false)", fixture.UniqueReferenceDuplicate("t", "renamed_u", "Key (n)=(1) already exists.")))
			run(fixture.Exec("ROLLBACK TO enforce", 0))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(NULL,'other',false)", fixture.UniqueReferenceDuplicate("t", "renamed_u", "Key (n)=(null) already exists.")))
			run(fixture.Exec("ROLLBACK TO enforce", 0))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT pg_get_indexdef('decorated'::regclass)", []string{"pg_get_indexdef"}, []string{"TEXT"}, [][]any{{decorated}}))
			run(fixture.Select("SELECT oid=$1::oid AS same,conindid=$2::oid AS same_index FROM pg_constraint WHERE conname='u' AND conrelid='t'::regclass", []string{"same", "same_index"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, constraintOID, indexOID))
			run(fixture.ErrorDiagnostic("INSERT INTO t VALUES(3,'a',true)", fixture.UniqueReferenceDuplicate("t", "partial", "Key (s)=(a) already exists.")))
			// Existing partition exception remains transactional and dependent children remain protected.
			for _, q := range []string{"CREATE TABLE p(n INT) PARTITION BY RANGE(n)", "CREATE TABLE p1 PARTITION OF p FOR VALUES FROM(0) TO(10)", "CREATE UNIQUE INDEX pi ON p(n)"} {
				run(fixture.Exec(q, 0))
			}
			var child string
			if err := a.QueryRowContext(p.Ctx, "SELECT c.relname FROM pg_class c JOIN pg_index i ON i.indexrelid=c.oid WHERE i.indrelid='p1'::regclass").Scan(&child); err != nil {
				t.Fatal(err)
			}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Error("DROP INDEX "+child, "2BP01"))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP INDEX pi", 0))
			run(transactionalIndexName("pi", false))
			run(fixture.Exec("ROLLBACK", 0))
			run(transactionalIndexName("pi", true))
			run(transactionalIndexName(child, true))
		})
	}
}

func TestReferenceTransactionalIndexMaintenanceAtomicity(t *testing.T) {
	cases := []struct {
		sql        string
		diagnostic fixture.Diagnostic
	}{
		{"DROP INDEX ix,missing", fixture.Diagnostic{Code: "42704", Message: `index "missing" does not exist`}},
		{"DROP INDEX ix,t", fixture.Diagnostic{Code: "42809", Message: `"t" is not an index`, Hint: "Use DROP TABLE to remove a table."}},
		{"DROP INDEX ix,u CASCADE", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop index u because constraint u on table q requires it", Hint: "You can drop constraint u on table q instead."}},
		{"ALTER INDEX ix RENAME TO t", fixture.Diagnostic{Code: "42P07", Message: `relation "t" already exists`}},
		{"ALTER INDEX u RENAME TO check_name", fixture.Diagnostic{Code: "42710", Message: `constraint "check_name" for relation "q" already exists`}},
		{"ALTER INDEX ix SET(fillfactor=80,deduplicate_items='bad')", fixture.Diagnostic{Code: "22023", Message: `invalid value for boolean option "deduplicate_items": bad`}},
	}
	for _, c := range cases {
		t.Run(c.sql, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, peer := fixture.LockConnections(t, p.Ctx, b.DB)
					run := transactionalIndexRun(t, p, b, a)
					indexTxMaintenanceSeed(run, "set")
					run(fixture.Exec("CREATE TABLE q(n INT CONSTRAINT u UNIQUE,CONSTRAINT check_name CHECK(n>0))", 0))
					run(fixture.Exec("CREATE INDEX sibling ON t(s)", 0))
					run(fixture.Exec("BEGIN", 0))
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec("INSERT INTO t VALUES(3,'before')", 1))
					run(fixture.Exec("SAVEPOINT keep", 0))
					stmt, err := a.PrepareContext(p.Ctx, c.sql)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					s := fixture.ImplicitError(c.sql, c.diagnostic)
					s.ErrorStage = "execute"
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: a, Statement: stmt, SQL: c.sql}, b.Recorder, s)
					run(fixture.Exec("ROLLBACK TO keep", 0))
					run(indexTxMaintenanceState("set", false))
					run(transactionalIndexName("sibling", true))
					run(transactionalIndexName("u", true))
					run(fixture.Select("SELECT n FROM t ORDER BY n NULLS LAST", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {nil}}))
					transactionalIndexRun(t, p, b, peer)(indexTxMaintenanceState("set", false))
					run(fixture.Exec("COMMIT", 0))
					run(fixture.Exec("DROP INDEX ix,ix", 0))
					run(transactionalIndexName("ix", false))
					run(transactionalIndexName("sibling", true))
				})
			}
		})
	}
}
