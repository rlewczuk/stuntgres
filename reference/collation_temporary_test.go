package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCollationTemporaryLifecycle(t *testing.T) {
	collationScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		owner, peer := referenceLockConnections(t, p.ctx, b.db)
		run := func(conn *sql.Conn, s referenceStep) { t.Helper(); referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		exec := func(conn *sql.Conn, queries ...string) {
			t.Helper()
			for _, q := range queries {
				run(conn, referenceExec(q, 0))
			}
		}
		exec(owner, `CREATE COLLATION pg_temp.coll_temporary FROM pg_catalog."C"`)
		run(owner, referenceSelect(`SELECT collencoding,collprovider,collversion,pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE collname='coll_temporary'`, []string{"collencoding", "collprovider", "collversion", "visible"}, []string{"INT4", "CHAR", "TEXT", "BOOL"}, [][]any{{int64(-1), "c", nil, false}}))
		value := referenceSelect(`SELECT upper('é' COLLATE pg_temp.coll_temporary) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}})
		run(owner, value)
		run(owner, referencePrepared(value))
		exec(owner, `CREATE TEMP TABLE coll_temporary_local(id SERIAL,v TEXT COLLATE pg_temp.coll_temporary)`, `CREATE INDEX coll_temporary_local_ix ON coll_temporary_local(v)`)
		run(owner, referenceExec(`INSERT INTO coll_temporary_local(v) VALUES('a')`, 1))
		local := referenceSelect(`SELECT id,v FROM coll_temporary_local`, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}})
		run(owner, local)
		missing := referenceErrorDiagnostic(`SELECT 'a' COLLATE coll_temporary`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `collation "coll_temporary" for encoding "UTF8" does not exist`, Position: "12"})
		missing.selectRows = true
		run(owner, missing)
		var schemas, qualified string
		if err := owner.QueryRowContext(p.ctx, `SELECT current_schemas(true)`).Scan(&schemas); err != nil {
			t.Fatal(err)
		}
		for _, name := range strings.Split(strings.Trim(schemas, "{}"), ",") {
			name = strings.Trim(name, `"`)
			if strings.HasPrefix(name, "pg_temp_") {
				qualified = name
			}
		}
		if qualified == "" {
			t.Fatal("owned TEMP namespace absent from current_schemas", schemas)
		}
		peerValue := referenceSelect(`SELECT upper('é' COLLATE "`+strings.ReplaceAll(qualified, `"`, `""`)+`".coll_temporary) AS value`, []string{"value"}, []string{"TEXT"}, [][]any{{"é"}})
		run(peer, peerValue)
		consumers := []string{`CREATE DOMAIN coll_temporary_domain AS TEXT COLLATE pg_temp.coll_temporary`, `CREATE TABLE coll_temporary_t(id INT,v coll_temporary_domain,w TEXT DEFAULT ('a' COLLATE pg_temp.coll_temporary),CHECK(w COLLATE pg_temp.coll_temporary > ''))`, `CREATE INDEX coll_temporary_ix ON coll_temporary_t(w COLLATE pg_temp.coll_temporary)`, `CREATE VIEW coll_temporary_v AS SELECT w COLLATE pg_temp.coll_temporary AS w FROM coll_temporary_t`}
		exec(owner, consumers...)
		run(owner, referenceExec(`INSERT INTO coll_temporary_t VALUES(1,'é','a')`, 1))
		if !b.pg {
			var rejected bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &rejected); err == nil || rejected.Len() != 0 {
				t.Fatal("persistent TEMP provider dependencies must not produce a dangling dump", err, rejected.String())
			}
			stream := `CREATE COLLATION coll_temporary_restore_a FROM pg_catalog."C"; CREATE COLLATION coll_temporary_restore_b FROM pg_catalog."POSIX";`
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(owner, value)
			run(owner, local)
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(stream)); err != nil {
				t.Fatal(err)
			}
			run(owner, value)
			run(owner, local)
			var portable bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &portable); err != nil || strings.Contains(portable.String(), "coll_temporary FROM") || strings.Contains(portable.String(), qualified) {
				t.Fatal("temporary definition leaked into portable dump", err, portable.String())
			}
			fresh := updateReturningNativeDestination(t)
			fresh.SetMaxOpenConns(2)
			freshOwner, err := fresh.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer freshOwner.Close()
			if _, err = freshOwner.ExecContext(p.ctx, `CREATE COLLATION pg_temp.coll_temporary_destination FROM pg_catalog."C"`); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Restore(p.ctx, fresh, bytes.NewReader(portable.Bytes())); err != nil {
				t.Fatal(err)
			}
			var actual string
			if err = freshOwner.QueryRowContext(p.ctx, `SELECT upper('é' COLLATE pg_temp.coll_temporary_destination)`).Scan(&actual); err != nil || actual != "é" {
				t.Fatal("independent import replaced target TEMP capability", actual, err)
			}
			exec(owner, consumers...)
			run(owner, referenceExec(`INSERT INTO coll_temporary_t VALUES(1,'é','a')`, 1))
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
		b.db.SetMaxIdleConns(0)
		fresh := sequenceCoreConn(t, p.ctx, b.db)
		run(fresh, referenceSelect(`SELECT COUNT(*) AS n FROM pg_catalog.pg_collation WHERE collname='coll_temporary'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fresh, referenceSelect(`SELECT * FROM coll_temporary_t`, []string{"id", "w"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}}))
		run(fresh, referenceSelect(`SELECT COUNT(*) AS n FROM pg_catalog.pg_class WHERE relname='coll_temporary_ix' OR relname='coll_temporary_v' OR relname='coll_temporary_local' OR relname='coll_temporary_local_ix' OR relname='coll_temporary_local_id_seq'`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		missingType := referenceErrorDiagnostic(`SELECT NULL::coll_temporary_domain AS value`, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `type "coll_temporary_domain" does not exist`, Position: "14"})
		missingType.selectRows, missingType.errorStage = true, "execute"
		run(fresh, missingType)
	})
}
