package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUsingIndexInclude(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run(kind, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_owned "+kind+" USING INDEX ux_idx", 0)
				ids := c.ids("ux_owned")
				nn := c.notNullIDs()
				// Native conkey inquiry is absent; shared deparse, inference,
				// dependency and payload-independent writes plus private descriptors
				// prove key-only semantics. Retain the exact server column witness.
				if c.b.PG {
					c.run(fixture.Select("SELECT conkey::text FROM pg_constraint WHERE conname='ux_owned'", []string{"conkey"}, []string{"TEXT"}, [][]any{{"{1,2}"}}))
				}
				c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{kind + " (a, b) INCLUDE (payload)"}}))
				c.run(fixture.Select("SELECT relnatts FROM pg_class WHERE relname='ux_owned'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(3)}}))
				c.run(fixture.Select("SELECT d.refobjsubid FROM pg_depend d WHERE d.classid::bigint='pg_constraint'::regclass::bigint AND d.objid=$1 AND d.refclassid::bigint='pg_class'::regclass::bigint AND d.refobjid=$2 AND d.deptype='a' ORDER BY d.refobjsubid", []string{"refobjsubid"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}, ids[0], ids[1]))
				c.exec("INSERT INTO ux_t VALUES(3,'z',NULL),(4,'q','kept')", 2)
				c.exec("INSERT INTO ux_t VALUES(1,'x','different') ON CONFLICT(a,b) DO NOTHING", 0)
				c.exec("INSERT INTO ux_t VALUES(1,'x','different') ON CONFLICT ON CONSTRAINT ux_owned DO NOTHING", 0)
				c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(1,'x','different')", fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists.")))
				c.exec("ALTER TABLE ux_t RENAME payload TO current_payload", 0)
				c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{kind + " (a, b) INCLUDE (current_payload)"}}))
				c.exec("CREATE TABLE ux_copy(LIKE ux_t INCLUDING ALL)", 0)
				c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid::bigint='ux_copy'::regclass::bigint AND (contype='u' OR contype='p')", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{kind + " (a, b) INCLUDE (current_payload)"}}))
				var copyName string
				if err := c.conn.QueryRowContext(c.p.Ctx, "SELECT conname FROM pg_constraint WHERE conrelid::bigint='ux_copy'::regclass::bigint AND (contype='u' OR contype='p')").Scan(&copyName); err != nil || copyName == "" {
					c.t.Fatal(copyName, err)
				}
				copyIDs := c.ids(copyName)
				if copyIDs[0] == ids[0] || copyIDs[1] == ids[1] || copyIDs[2] == ids[2] {
					c.t.Fatal("LIKE did not remap reciprocal identities", ids, copyIDs)
				}
				c.indexAttributes(copyName, [][]any{{"a", int64(23)}, {"b", int64(25)}, {"payload", int64(25)}})
				c.notices = nil
				c.exec("ALTER TABLE ux_t ALTER current_payload TYPE VARCHAR(40)", 0)
				c.quiet()
				current := c.ids("ux_owned")
				if current[0] == ids[0] || current[1] != ids[1] || current[2] == ids[2] {
					c.t.Fatal("INCLUDE TYPE must remint constraint/index only", ids, current)
				}
				if !reflect.DeepEqual(c.notNullIDs(), nn) || c.ids(copyName) != copyIDs {
					c.t.Fatal("INCLUDE TYPE changed key NN or LIKE group")
				}
				required := kind == "PRIMARY KEY"
				c.attributes([][]any{{"a", int64(23), required}, {"b", int64(25), required}, {"current_payload", int64(1043), false}})
				c.indexAttributes("ux_owned", [][]any{{"a", int64(23)}, {"b", int64(25)}, {"current_payload", int64(1043)}})
				c.run(fixture.Select("SELECT a,b,current_payload FROM ux_t ORDER BY a", []string{"a", "b", "current_payload"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}, {int64(3), "z", nil}, {int64(4), "q", "kept"}}))
				c.run(fixture.Select("SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname='ux_owned'", []string{"pg_get_constraintdef"}, []string{"TEXT"}, [][]any{{kind + " (a, b) INCLUDE (current_payload)"}}))
				c.notices = nil
				c.exec("ALTER TABLE ux_t DROP current_payload RESTRICT", 0)
				c.quiet()
				if !reflect.DeepEqual(c.notNullIDs(), nn) || c.ids(copyName) != copyIDs {
					c.t.Fatal("INCLUDE DROP changed key NN or LIKE group")
				}
				c.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ux_owned'", []string{"relname"}, []string{"NAME"}, nil))
				var constraints [][]any
				if required {
					constraints = [][]any{{"ux_t_a_not_null", "n", "NOT NULL a", nil}, {"ux_t_b_not_null", "n", "NOT NULL b", nil}}
				}
				c.catalog(constraints)
				c.attributes([][]any{{"a", int64(23), required}, {"b", int64(25), required}})
				c.run(fixture.Select("SELECT a,b FROM ux_t ORDER BY a", []string{"a", "b"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "x"}, {int64(2), "y"}, {int64(3), "z"}, {int64(4), "q"}}))
				c.exec("INSERT INTO ux_copy VALUES(1,'x',NULL)", 1)
				c.run(fixture.ErrorDiagnostic("INSERT INTO ux_copy VALUES(1,'x','different')", fixture.UniqueReferenceDuplicate("ux_copy", copyName, "Key (a, b)=(1, x) already exists.")))
				c.exec("INSERT INTO ux_t VALUES(1,'x')", 1)
				if kind == "PRIMARY KEY" {
					c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(NULL,'x')", fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, x")))
				}
			})
		})
	}
}

func TestReferenceUsingIndexOwnership(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run(kind, func(t *testing.T) {
			fixture.UniqueReferenceSchemaCleanup(t, "ux_moved")
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_owned "+kind+" USING INDEX ux_idx", 0)
				ids := c.ids("ux_owned")
				want := usingIndexDiagnostic("2BP01", `cannot drop index ux_owned because constraint ux_owned on table ux_t requires it`)
				want.Hint = "You can drop constraint ux_owned on table ux_t instead."
				c.run(fixture.ErrorDiagnostic("DROP INDEX ux_owned", want))
				c.exec("ALTER INDEX ux_owned RENAME TO ux_renamed", 0)
				if c.ids("ux_renamed") != ids {
					c.t.Fatal("index rename broke reciprocal identity")
				}
				c.exec("ALTER TABLE ux_t RENAME CONSTRAINT ux_renamed TO ux_final", 0)
				if c.ids("ux_final") != ids {
					c.t.Fatal("constraint rename broke reciprocal identity")
				}
				c.exec("CREATE SCHEMA ux_moved", 0)
				c.exec("ALTER TABLE ux_t SET SCHEMA ux_moved", 0)
				if c.ids("ux_final") != ids {
					c.t.Fatal("schema move replaced group")
				}
				c.exec("ALTER TABLE ux_moved.ux_t DROP CONSTRAINT ux_final", 0)
				c.run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ux_final'", []string{"relname"}, []string{"NAME"}, nil))
				c.exec("INSERT INTO ux_moved.ux_t VALUES(1,'x','duplicate allowed')", 1)
				if kind == "PRIMARY KEY" {
					want := fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, x, null")
					want.Schema = "ux_moved"
					c.run(fixture.ErrorDiagnostic("INSERT INTO ux_moved.ux_t VALUES(NULL,'x',NULL)", want))
				}
				c.exec("DROP SCHEMA ux_moved CASCADE", 0)
			})
		})
	}
}

func TestReferenceUsingIndexTransactions(t *testing.T) {
	for _, end := range []string{"COMMIT", "ROLLBACK", "ROLLBACK TO before_attach"} {
		t.Run(end, func(t *testing.T) {
			usingIndexScenario(t, func(c *usingIndexCase) {
				c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
				parent, index := c.oid("ux_t"), c.oid("ux_idx")
				peer, err := c.b.DB.Conn(c.p.Ctx)
				if err != nil {
					c.t.Fatal(err)
				}
				defer peer.Close()
				c.exec("BEGIN", 0)
				defer c.conn.ExecContext(c.p.Ctx, "ROLLBACK")
				c.exec("SAVEPOINT before_attach", 0)
				c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_pk PRIMARY KEY USING INDEX ux_idx", 0)
				attached := c.ids("ux_pk")
				if attached[1] != parent || attached[2] != index {
					c.t.Fatal(attached)
				}
				// Catalog reads do not acquire AX on ux_t; ordinary peer table reads would wait.
				fixture.RunStep(c.t, c.p.Ctx, peer, c.b.Recorder, fixture.Select("SELECT conname FROM pg_constraint WHERE conname='ux_pk'", []string{"conname"}, []string{"NAME"}, nil))
				fixture.RunStep(c.t, c.p.Ctx, peer, c.b.Recorder, fixture.Select("SELECT relname FROM pg_class WHERE oid=$1", []string{"relname"}, []string{"NAME"}, [][]any{{"ux_idx"}}, index))
				c.exec(end, 0)
				if end == "ROLLBACK TO before_attach" {
					c.exec("COMMIT", 0)
				}
				if end == "COMMIT" {
					if c.ids("ux_pk") != attached {
						c.t.Fatal("commit replaced attached group")
					}
					c.run(fixture.ErrorDiagnostic("INSERT INTO ux_t VALUES(NULL,'z',NULL)", fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, z, null")))
				} else {
					if c.oid("ux_idx") != index {
						c.t.Fatal("rollback changed standalone index")
					}
					c.exec("INSERT INTO ux_t VALUES(NULL,'z',NULL)", 1)
					c.run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid::bigint='ux_t'::regclass::bigint", []string{"conname"}, []string{"NAME"}, nil))
				}
			})
		})
	}
	t.Run("failure_abort_savepoint", func(t *testing.T) {
		usingIndexScenario(t, func(c *usingIndexCase) {
			c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b)")
			old := c.oid("ux_idx")
			c.exec("BEGIN", 0)
			defer c.conn.ExecContext(c.p.Ctx, "ROLLBACK")
			c.exec("SAVEPOINT before_attach", 0)
			c.failure("ALTER TABLE ux_t ADD CONSTRAINT ux_owned UNIQUE USING INDEX ux_idx, ALTER missing SET NOT NULL", usingIndexDiagnostic("42703", `column "missing" of relation "ux_t" does not exist`), false)
			c.run(fixture.ErrorDiagnostic("SELECT a FROM ux_t", fixture.TransactionAborted()))
			c.exec("ROLLBACK TO before_attach", 0)
			if c.oid("ux_idx") != old {
				c.t.Fatal("failed private attach leaked")
			}
			c.rows()
			c.exec("ALTER TABLE ux_t ADD UNIQUE USING INDEX ux_idx", 0)
			c.exec("COMMIT", 0)
		})
	})
}

func TestReferenceUsingIndexRetainedStatements(t *testing.T) {
	usingIndexScenario(t, func(c *usingIndexCase) {
		c.setup("CREATE UNIQUE INDEX ux_idx ON ux_t(a,b) INCLUDE(payload)")
		q := "INSERT INTO ux_t VALUES($1,$2,$3)"
		insert, err := c.conn.PrepareContext(c.p.Ctx, q)
		if err != nil {
			c.t.Fatal(err)
		}
		defer insert.Close()
		selectQ := "SELECT a,b,payload FROM ux_t ORDER BY a"
		selectStmt, err := c.conn.PrepareContext(c.p.Ctx, selectQ)
		if err != nil {
			c.t.Fatal(err)
		}
		defer selectStmt.Close()
		selected := fixture.HeldStatement{Executor: c.conn, Statement: selectStmt, SQL: selectQ}
		fixture.TableDDLMetadata(c.t, c.p.Ctx, selected, c.b.PG, false, selectQ, []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, []bool{true, true, true}, 2)
		fixture.TableDDLMetadata(c.t, c.p.Ctx, c.conn, c.b.PG, c.prepared, "SELECT a,b,payload FROM ux_t WHERE FALSE", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, []bool{true, true, true}, 0)
		var eager *sql.Rows
		if !c.b.PG {
			// Native owns eager immutable Rows; PostgreSQL does not expose this API.
			eager, err = c.b.DB.QueryContext(c.p.Ctx, selectQ)
			if err != nil {
				c.t.Fatal(err)
			}
			defer eager.Close()
		}
		conflictQ := "INSERT INTO ux_t VALUES($1,$2,$3) ON CONFLICT ON CONSTRAINT ux_owned DO NOTHING"
		conflict, err := c.conn.PrepareContext(c.p.Ctx, conflictQ)
		missingConflict := usingIndexDiagnostic("42704", `constraint "ux_owned" for table "ux_t" does not exist`)
		fixture.AssertDiagnostic(c.t, err, &missingConflict)
		if conflict != nil {
			conflict.Close()
			c.t.Fatal("failed Prepare exposed a statement")
		}
		held := fixture.HeldStatement{Executor: c.conn, Statement: insert, SQL: q}
		c.exec("ALTER TABLE ux_t ADD CONSTRAINT ux_owned PRIMARY KEY USING INDEX ux_idx", 0)
		if conflict == nil {
			conflict, err = c.conn.PrepareContext(c.p.Ctx, conflictQ)
			if err != nil {
				c.t.Fatal(err)
			}
		}
		defer conflict.Close()
		fixture.TableDDLMetadata(c.t, c.p.Ctx, selected, c.b.PG, false, selectQ, []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, []bool{false, false, true}, 2)
		fixture.TableDDLMetadata(c.t, c.p.Ctx, c.conn, c.b.PG, c.prepared, "SELECT a,b,payload FROM ux_t WHERE FALSE", []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, []bool{false, false, true}, 0)
		if eager != nil {
			columns, err := eager.ColumnTypes()
			if err != nil {
				c.t.Fatal(err)
			}
			fixture.TableDDLColumnTypes(c.t, false, columns, []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, []bool{true, true, true})
			for _, want := range [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}} {
				if !eager.Next() {
					c.t.Fatal("pre-attach eager row lost", eager.Err())
				}
				var a int64
				var b string
				var payload any
				if err := eager.Scan(&a, &b, &payload); err != nil || a != want[0] || b != want[1] || payload != want[2] {
					c.t.Fatal(a, b, payload, err)
				}
			}
			if eager.Next() || eager.Err() != nil {
				c.t.Fatal(eager.Err())
			}
			eager.Close()
		}
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_owned", "Key (a, b)=(1, x) already exists."), int64(1), "x", "other"))
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.PrimaryKeyReferenceNull("ux_t", "a", "null, z, null"), nil, "z", nil))
		fixture.RunStep(c.t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: conflict, SQL: conflictQ}, c.recorder, fixture.Exec(conflictQ, 0, int64(1), "x", nil))
		fixture.RunStep(c.t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: selectStmt, SQL: selectQ}, c.recorder, fixture.Select(selectQ, []string{"a", "b", "payload"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "x", "kept"}, {int64(2), "y", nil}}))
		// A held conflict target must reanalyze its name after rename/drop, unlike
		// the ordinary retained INSERT which now reports the new constraint name.
		conflictMissing := func() {
			s := fixture.ErrorDiagnostic(conflictQ, missingConflict, int64(1), "x", nil)
			s.ErrorStage = "execute"
			fixture.RunStep(c.t, c.p.Ctx, fixture.HeldStatement{Executor: c.conn, Statement: conflict, SQL: conflictQ}, c.recorder, s)
		}
		c.exec("ALTER TABLE ux_t RENAME CONSTRAINT ux_owned TO ux_current", 0)
		conflictMissing()
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_current", "Key (a, b)=(1, x) already exists."), int64(1), "x", nil))
		c.exec("BEGIN", 0)
		defer c.conn.ExecContext(c.p.Ctx, "ROLLBACK")
		c.exec("ALTER TABLE ux_t DROP CONSTRAINT ux_current", 0)
		c.exec("ROLLBACK", 0)
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.ErrorDiagnostic(q, fixture.UniqueReferenceDuplicate("ux_t", "ux_current", "Key (a, b)=(1, x) already exists."), int64(1), "x", nil))
		conflictMissing()
		c.exec("ALTER TABLE ux_t DROP CONSTRAINT ux_current", 0)
		conflictMissing()
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.Exec(q, 1, int64(1), "x", "now allowed"))
		fixture.RunStep(c.t, c.p.Ctx, held, c.recorder, fixture.Exec(q, 1, int64(3), "z", nil))
	})
}
