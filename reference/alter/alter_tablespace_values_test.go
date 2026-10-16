package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTablespaceValues(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsExec(t, p, b, c, "CREATE ROLE ats_reader")
		atsExec(t, p, b, c, "GRANT SELECT ON t TO ats_reader")
		var grantor string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&grantor); err != nil {
			t.Fatal(err)
		}
		// Exact ACL entries independently prove SELECT survived without UPDATE.
		acl := "{" + grantor + "=arwdDxtm/" + grantor + ",ats_reader=r/" + grantor + "}"
		var oid, owner, index int64
		snapshot := func() (int64, int64, int64) {
			var a, o, i int64
			err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,relowner::bigint,(SELECT oid::bigint FROM pg_class WHERE oid='ats_idx'::regclass::oid) FROM pg_class WHERE oid='t'::regclass::oid").Scan(&a, &o, &i)
			if err != nil {
				t.Fatal(err)
			}
			return a, o, i
		}
		oid, owner, index = snapshot()
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec("ALTER TABLE t SET TABLESPACE pg_default", 0)
			s.Prepared = prepared
			atsRun(t, p, b, c, s)
			atsRun(t, p, b, c, atsRows())
			atsPlacement(t, p, b, c, "t", 0)
			atsPlacement(t, p, b, c, "ats_idx", 0)
			atsRun(t, p, b, c, fixture.Select("SELECT tablespace FROM pg_tables WHERE schemaname='public' AND tablename='t'", []string{"tablespace"}, []string{"NAME"}, [][]any{{nil}}))
			a, o, i := snapshot()
			if a != oid || o != owner || i != index {
				t.Fatal("no-op changed identities", a, o, i, oid, owner, index)
			}
			atsRun(t, p, b, c, fixture.Select("SELECT obj_description('t'::regclass)", []string{"obj_description"}, []string{"TEXT"}, [][]any{{"placement witness"}}))
			atsRun(t, p, b, c, fixture.Select("SELECT relacl FROM pg_class WHERE oid='t'::regclass::oid", []string{"relacl"}, []string{"_ACLITEM"}, [][]any{{acl}}))
			atsRun(t, p, b, c, fixture.Select("SELECT attname,attnotnull,atthasdef FROM pg_attribute WHERE attrelid='t'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "attnotnull", "atthasdef"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"id", true, false}, {"note", false, true}}))
		}
	})
}

func TestReferenceAlterTablespaceKinds(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		for _, q := range []string{"CREATE TEMP TABLE ats_temp(a INT)", "CREATE TABLE ats_parent(a INT) PARTITION BY RANGE(a)", "CREATE TABLE ats_child PARTITION OF ats_parent FOR VALUES FROM(0) TO(10)", "CREATE INDEX ats_parent_idx ON ats_parent(a)", "CREATE MATERIALIZED VIEW ats_mv AS SELECT id FROM t", "CREATE VIEW ats_view AS SELECT id FROM t", "CREATE SEQUENCE ats_seq", "CREATE TYPE ats_composite AS(a INT)", "INSERT INTO ats_parent VALUES(7)"} {
			if q == "CREATE INDEX ats_parent_idx ON ats_parent(a)" && !b.PG {
				atsError(t, p, b, c, q, atsDiagnostic("0A000", "partitioned indexes are unsupported"))
				continue
			}
			n := int64(0)
			if q == "INSERT INTO ats_parent VALUES(7)" || q == "CREATE MATERIALIZED VIEW ats_mv AS SELECT id FROM t" {
				n = 1
			}
			atsRun(t, p, b, c, fixture.Exec(q, n))
		}
		atsExec(t, p, b, c, "CREATE FOREIGN DATA WRAPPER ats_fdw")
		defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER ats_fdw CASCADE")
		atsExec(t, p, b, c, "CREATE SERVER ats_server FOREIGN DATA WRAPPER ats_fdw")
		atsExec(t, p, b, c, "CREATE FOREIGN TABLE ats_foreign(id INT) SERVER ats_server")
		foreign := atsDiagnostic("42809", `ALTER action SET TABLESPACE cannot be performed on relation "ats_foreign"`)
		foreign.Detail = "This operation is not supported for foreign tables."
		atsError(t, p, b, c, "ALTER TABLE ats_foreign SET TABLESPACE pg_default", foreign)
		for _, name := range []string{"t", "ats_temp", "ats_parent", "ats_idx", "ats_parent_idx", "ats_mv"} {
			if name == "ats_parent_idx" && !b.PG {
				continue // Creation's explicit native prerequisite boundary was asserted above.
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("ALTER TABLE "+name+" SET TABLESPACE pg_default", 0)
				s.Prepared = prepared
				atsRun(t, p, b, c, s)
				atsPlacement(t, p, b, c, name, 0)
			}
		}
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		atsRun(t, p, b, tx, fixture.Select("SELECT a FROM ats_child", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
		atsExec(t, p, b, c, "SET lock_timeout='100ms'")
		atsExec(t, p, b, c, "ALTER TABLE ats_parent * SET TABLESPACE pg_default")
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		atsExec(t, p, b, c, "RESET lock_timeout")
		atsPlacement(t, p, b, c, "ats_child", 0)
		mixed := "ALTER TABLE ats_parent ADD COLUMN extra INT DEFAULT 4, SET TABLESPACE pg_default"
		if b.PG {
			atsExec(t, p, b, c, mixed)
			atsRun(t, p, b, c, fixture.Select("SELECT a,extra FROM ats_parent", []string{"a", "extra"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(4)}}))
		} else {
			atsError(t, p, b, c, mixed, atsDiagnostic("0A000", "altering partition hierarchies is unsupported"))
			atsRun(t, p, b, c, fixture.Select("SELECT a FROM ats_parent", []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}}))
			atsPlacement(t, p, b, c, "ats_parent", 0)
			atsPlacement(t, p, b, c, "ats_child", 0)
		}
		for _, tc := range []struct{ name, kind string }{{"ats_view", "views"}, {"ats_seq", "sequences"}} {
			want := atsDiagnostic("42809", "ALTER action SET TABLESPACE cannot be performed on relation \""+tc.name+"\"")
			want.Detail = "This operation is not supported for " + tc.kind + "."
			atsError(t, p, b, c, "ALTER TABLE "+tc.name+" SET TABLESPACE pg_default", want)
		}
		want := atsDiagnostic("42809", `"ats_composite" is a composite type`)
		want.Hint = "Use ALTER TYPE instead."
		atsError(t, p, b, c, "ALTER TABLE ats_composite SET TABLESPACE pg_default", want)
	})
}

func TestReferenceAlterTablespacePrepared(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsRoles(t, p, b, c)
		q := "SELECT id,note FROM t ORDER BY id"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var retained *sql.Rows
		if !b.PG {
			retained, err = p.Native.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
		}
		for range 2 {
			atsRun(t, p, b, c, fixture.Prepared(fixture.Exec("ALTER TABLE t SET TABLESPACE pg_default", 0)))
			atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, atsRows())
		}
		if retained != nil {
			names, err := retained.Columns()
			if err != nil || !reflect.DeepEqual(names, []string{"id", "note"}) {
				t.Fatal(names, err)
			}
			types, err := retained.ColumnTypes()
			if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
				t.Fatal(types, err)
			}
			if !retained.Next() {
				t.Fatal(retained.Err())
			}
			var id int64
			var note string
			if err = retained.Scan(&id, &note); err != nil || id != 7 || note != "keep" {
				t.Fatal(id, note, err)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal(retained.Err())
			}
		}
		late, err := c.PrepareContext(p.Ctx, "ALTER TABLE ats_late SET TABLESPACE pg_default")
		if err != nil {
			t.Fatal("utility Prepare resolved absent relation", err)
		}
		defer late.Close()
		atsExec(t, p, b, c, "CREATE TABLE ats_late(a INT)")
		atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: late, SQL: "ALTER TABLE ats_late SET TABLESPACE pg_default"}, fixture.Exec("ALTER TABLE ats_late SET TABLESPACE pg_default", 0))
		utility, err := c.PrepareContext(p.Ctx, "ALTER TABLE t SET TABLESPACE pg_default")
		if err != nil {
			t.Fatal(err)
		}
		defer utility.Close()
		atsExec(t, p, b, c, "SET ROLE ats_reader")
		atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: utility, SQL: "ALTER TABLE t SET TABLESPACE pg_default"}, fixture.ErrorDiagnostic("ALTER TABLE t SET TABLESPACE pg_default", atsDiagnostic("42501", "must be owner of table t")))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_default, ADD COLUMN extra INT DEFAULT 9")
		atsRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, atsRows())
		atsRun(t, p, b, c, fixture.Select("SELECT extra FROM t", []string{"extra"}, []string{"INT4"}, [][]any{{int64(9)}}))
	})
}

func TestReferenceAlterTablespaceTransactions(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		for _, prepared := range []bool{false, true} {
			for _, actions := range []string{"ADD COLUMN transient INT DEFAULT 3, SET TABLESPACE ats_missing", "SET TABLESPACE ats_missing, ADD COLUMN transient INT DEFAULT 3", "ADD COLUMN transient INT DEFAULT 3, SET TABLESPACE pg_default, ALTER COLUMN absent SET NOT NULL", "SET TABLESPACE pg_default, ADD CONSTRAINT ats_bad CHECK(id<0)"} {
				want := atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)
				if actions == "ADD COLUMN transient INT DEFAULT 3, SET TABLESPACE pg_default, ALTER COLUMN absent SET NOT NULL" {
					want = atsDiagnostic("42703", `column "absent" of relation "t" does not exist`)
				}
				if actions == "SET TABLESPACE pg_default, ADD CONSTRAINT ats_bad CHECK(id<0)" {
					want = atsDiagnostic("23514", `check constraint "ats_bad" of relation "t" is violated by some row`)
					want.Schema, want.Table, want.Constraint = "public", "t", "ats_bad"
				}
				s := fixture.ErrorDiagnostic("ALTER TABLE t "+actions, want)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				atsRun(t, p, b, c, s)
				atsRun(t, p, b, c, atsRows())
				atsPlacement(t, p, b, c, "t", 0)
				atsRun(t, p, b, c, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='t'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"id"}, {"note"}}))
			}
		}
		atsExec(t, p, b, c, "BEGIN")
		atsExec(t, p, b, c, "ALTER TABLE t SET TABLESPACE pg_default, ADD COLUMN rolled INT DEFAULT 4")
		atsExec(t, p, b, c, "ROLLBACK")
		atsExec(t, p, b, c, "BEGIN")
		atsExec(t, p, b, c, "SAVEPOINT ats_save")
		atsRun(t, p, b, c, fixture.ErrorDiagnostic("ALTER TABLE t SET TABLESPACE ats_missing", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)))
		atsRun(t, p, b, c, fixture.ErrorDiagnostic("ALTER TABLE t SET TABLESPACE pg_default", atsDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		atsExec(t, p, b, c, "ROLLBACK TO ats_save")
		atsRun(t, p, b, c, fixture.Prepared(fixture.Exec("ALTER TABLE t ADD COLUMN committed INT DEFAULT 5, SET TABLESPACE pg_default", 0)))
		atsExec(t, p, b, c, "COMMIT")
		atsRun(t, p, b, c, fixture.Select("SELECT id,note,committed FROM t", []string{"id", "note", "committed"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(7), "keep", int64(5)}}))
		atsExec(t, p, b, c, "CREATE TABLE ats_inherit(a INT)")
		atsExec(t, p, b, c, "CREATE TABLE ats_inherit_child() INHERITS(ats_inherit)")
		atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_inherit_child VALUES(NULL)", 1))
		want := atsDiagnostic("23502", `column "a" of relation "ats_inherit_child" contains null values`)
		want.Schema, want.Table, want.Column = "public", "ats_inherit_child", "a"
		atsError(t, p, b, c, "ALTER TABLE ats_inherit SET TABLESPACE pg_default, ALTER a SET NOT NULL", want)
		atsRun(t, p, b, c, fixture.Select("SELECT attnotnull FROM pg_attribute WHERE attrelid='ats_inherit'::regclass::oid AND attname='a'", []string{"attnotnull"}, []string{"BOOL"}, [][]any{{false}}))
	})
}
