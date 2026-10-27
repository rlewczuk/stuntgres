package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalCommentIdentity(t *testing.T) {
	p := transactionalCommentScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
			transactionalCommentSetup(run)
			defer transactionalCommentCleanup(t, p, b)
			comments := func(x fixture.Executor, relation string) {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT obj_description('tx_comment_private'::regnamespace,'pg_namespace') AS schema_text,obj_description('"+relation+"'::regclass,'pg_class') AS table_text,col_description('"+relation+"'::regclass,3) AS column_text", []string{"schema_text", "table_text", "column_text"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"private schema", "private table", "third"}}))
			}
			var schemaID, tableID int64
			descriptions := func(x fixture.Executor, present bool) {
				rows := [][]any{}
				if present {
					rows = [][]any{{schemaID, int64(2615), int64(0), "private schema"}, {tableID, int64(1259), int64(0), "private table"}, {tableID, int64(1259), int64(3), "third"}}
				}
				fixture.PgDescriptionExecutorRows(t, p, b, x, pgDescriptionSelect("SELECT objoid,classoid,objsubid,description FROM pg_description WHERE (classoid=2615 AND objoid=$1) OR (classoid=1259 AND objoid=$2)", rows, schemaID, tableID))
			}
			objects := func(x fixture.Executor, present bool) {
				namespaces, tables, columns := int64(0), int64(0), int64(0)
				if present {
					namespaces, tables, columns = 1, 1, 2
				}
				fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT (SELECT count(*) FROM pg_namespace WHERE oid=$1) AS namespaces,(SELECT count(*) FROM pg_class WHERE oid=$2) AS tables,(SELECT count(*) FROM pg_attribute WHERE attrelid=$2 AND attnum>0 AND NOT attisdropped) AS columns", []string{"namespaces", "tables", "columns"}, []string{"INT8", "INT8", "INT8"}, [][]any{{namespaces, tables, columns}}, schemaID, tableID))
			}
			for _, commit := range []bool{false, true} {
				run(fixture.Exec("BEGIN", 0))
				defer c.ExecContext(p.Ctx, "ROLLBACK")
				for _, q := range []string{"CREATE SCHEMA tx_comment_private", "CREATE TABLE tx_comment_private.t(a INT,b INT)", "ALTER TABLE tx_comment_private.t ADD COLUMN c TEXT", "COMMENT ON SCHEMA tx_comment_private IS 'private schema'", "COMMENT ON TABLE tx_comment_private.t IS 'private table'", "COMMENT ON COLUMN tx_comment_private.t.c IS 'third'", "ALTER TABLE tx_comment_private.t DROP COLUMN b", "ALTER TABLE tx_comment_private.t RENAME COLUMN c TO renamed", "ALTER TABLE tx_comment_private.t RENAME TO moved"} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("ALTER TABLE tx_comment_private.moved SET SCHEMA tx_comment_schema", 0))
				run(fixture.Select("SELECT obj_description('tx_comment_schema.moved'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{"private table"}}))
				comments(c, "tx_comment_schema.moved")
				run(fixture.Exec("ALTER TABLE tx_comment_schema.moved SET SCHEMA tx_comment_private", 0))
				run(fixture.Exec("INSERT INTO tx_comment_private.moved VALUES(7,'sentinel')", 1))
				run(fixture.Select("SELECT attnum,attname FROM pg_attribute WHERE attrelid='tx_comment_private.moved'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname"}, []string{"INT2", "NAME"}, [][]any{{int64(1), "a"}, {int64(3), "renamed"}}))
				run(fixture.Select("SELECT col_description('tx_comment_private.moved'::regclass,3) AS text", []string{"text"}, []string{"TEXT"}, [][]any{{"third"}}))
				if err := c.QueryRowContext(p.Ctx, "SELECT 'tx_comment_private'::regnamespace::oid,'tx_comment_private.moved'::regclass::oid").Scan(&schemaID, &tableID); err != nil || schemaID <= 0 || tableID <= 0 {
					t.Fatal("private object identities", schemaID, tableID, err)
				}
				comments(c, "tx_comment_private.moved")
				objects(c, true)
				descriptions(c, true)
				objects(peer, false)
				descriptions(peer, false)
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_comment_private'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				if commit {
					run(fixture.Exec("COMMIT", 0))
				} else {
					run(fixture.Exec("ROLLBACK", 0))
				}
				count := int64(0)
				if commit {
					count = 1
				}
				fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname='tx_comment_private'", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
				objects(peer, commit)
				descriptions(peer, commit)
				if commit {
					comments(peer, "tx_comment_private.moved")
				} else {
					objects(c, false)
					descriptions(c, false)
				}
			}
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("DROP TABLE tx_comment_private.moved", 0))
			run(fixture.Exec("CREATE TABLE tx_comment_private.moved(a INT)", 0))
			run(fixture.Select("SELECT obj_description('tx_comment_private.moved'::regclass,'pg_class') AS text", []string{"text"}, []string{"TEXT"}, [][]any{{nil}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT a,renamed FROM tx_comment_private.moved", []string{"a", "renamed"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "sentinel"}}))
			comments(c, "tx_comment_private.moved")
			comments(peer, "tx_comment_private.moved")
			objects(c, true)
			descriptions(c, true)
			descriptions(peer, true)
			const liveRole = "tx_comment_renamed"
			run(fixture.Exec("ALTER ROLE tx_comment_role RENAME TO "+liveRole, 0))
			run(fixture.Select("SELECT shobj_description(oid,'pg_authid') AS text FROM pg_roles WHERE rolname='"+liveRole+"'", []string{"text"}, []string{"TEXT"}, [][]any{{"old"}}))
			run(fixture.Exec("DROP ROLE "+liveRole, 0))
			run(fixture.Exec("CREATE ROLE tx_comment_role", 0))
			transactionalCommentValue(t, p, b, c, transactionalCommentKinds[5], nil)
			run(fixture.Select("SELECT nextval('tx_comment_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("COMMENT ON SEQUENCE tx_comment_sequence IS 'temporary'", 0))
			run(fixture.Select("SELECT nextval('tx_comment_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Exec("ROLLBACK", 0))
			transactionalCommentValue(t, p, b, c, transactionalCommentKinds[6], "old")
			run(fixture.Select("SELECT nextval('tx_comment_sequence') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
		})
	}
}

func TestReferenceTransactionalCommentDiagnostics(t *testing.T) {
	p := transactionalCommentScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, peer := fixture.LockConnections(t, p.Ctx, b.DB)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			transactionalCommentSetup(run)
			defer transactionalCommentCleanup(t, p, b)
			run(fixture.Exec("CREATE ROLE tx_comment_actor", 0))
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO tx_comment_actor", 0))
			for _, k := range transactionalCommentKinds {
				tx, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				q := "COMMENT ON " + k.target + " IS 'readonly'"
				s, err := tx.PrepareContext(p.Ctx, q)
				if err != nil {
					tx.Rollback()
					t.Fatal("quiet read-only Prepare", err)
				}
				_, err = s.ExecContext(p.Ctx)
				want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute COMMENT in a read-only transaction")
				fixture.AssertDiagnostic(t, err, &want)
				s.Close()
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				transactionalCommentValue(t, p, b, peer, k, "old")
			}
			q := "COMMENT ON TABLE tx_comment IS 'held'"
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			run(fixture.Exec("BEGIN", 0))
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			run(fixture.Exec("SAVEPOINT authority", 0))
			run(fixture.Exec("SET ROLE tx_comment_actor", 0))
			held := fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleReferenceError(q, "42501", "must be owner of table tx_comment", "", false, "execute"))
			run(fixture.RoleReferenceError("COMMENT ON ROLE absent IS NULL", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute"))
			run(fixture.Exec("ROLLBACK TO authority", 0))
			transactionalCommentValue(t, p, b, c, transactionalCommentKinds[0], "old")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			transactionalCommentValue(t, p, b, c, transactionalCommentKinds[0], "held")
			transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[0], "old")
			run(fixture.Exec("ROLLBACK", 0))
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec("BEGIN READ ONLY", 0))
				run(fixture.RoleReferenceError("COMMENT ON TABLE absent_schema.absent IS NULL", "25006", "cannot execute COMMENT in a read-only transaction", "", prepared, "execute"))
				run(fixture.RoleReferenceError("COMMENT ON COLUMN tx_comment.absent IS NULL", "25P02", "current transaction is aborted, commands ignored until end of transaction block", "", false, "execute"))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("BEGIN READ ONLY", 0))
				run(fixture.Exec("SET ROLE tx_comment_actor", 0))
				run(fixture.RoleReferenceError(q, "25006", "cannot execute COMMENT in a read-only transaction", "", prepared, "execute"))
				run(fixture.Exec("ROLLBACK", 0))
			}
			run(fixture.Exec("DROP TABLE tx_comment", 0))
			run(fixture.Exec("CREATE TABLE tx_comment(a INT,b TEXT)", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 0))
			transactionalCommentValue(t, p, b, peer, transactionalCommentKinds[0], "held")
		})
	}
}
