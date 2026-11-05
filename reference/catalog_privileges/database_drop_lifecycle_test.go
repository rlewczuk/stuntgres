package reference_test

import (
	"context"
	"database/sql/driver"
	"io"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropLifecycle(t *testing.T) {
	// REL_18_6 dropdb removes the database identity and its local objects,
	// shared dependencies and database-qualified defaults, not peer state.
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const a, b, role = "dd_lifecycle_a", "dd_lifecycle_b", "dd_lifecycle_role"
			fixture.RoleReferenceCleanup(t, []string{role})
			names := []string{a, b}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, admin, &names}
					defaultName := c.scalar("SELECT current_database()")
					defaultOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=current_database()")
					c.run(fixture.Exec("CREATE TABLE drop_value(v integer)", 0))
					c.run(fixture.Exec("INSERT INTO drop_value VALUES(33)", 1))
					query := "DROP DATABASE IF EXISTS " + a
					route := databaseDropPrepare(c, mode, query)
					databaseDropNotice(t, &notices, a, 0)
					c.absent(a)
					route.run(c, fixture.Exec(query, 0))
					databaseDropNotice(t, &notices, a, 1)
					c.run(fixture.Exec("CREATE ROLE "+role, 0))
					c.create(a, "", false)
					c.create(b, "", false)
					c.run(fixture.Exec("GRANT CONNECT ON DATABASE "+b+" TO "+role, 0))
					c.run(fixture.Exec("GRANT SET ON PARAMETER work_mem TO "+role, 0))
					t.Cleanup(func() {
						ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
						defer cancel()
						if _, err := c.conn.ExecContext(ctx, "REVOKE SET ON PARAMETER work_mem FROM "+role); err != nil {
							t.Error("parameter grant cleanup", err)
						}
					})
					c.run(fixture.Select("SELECT has_parameter_privilege($1,'work_mem','SET') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}, role))
					origin := "dd_lifecycle_origin"
					originID := c.scalar("SELECT pg_replication_origin_create($1)::text", origin)
					if originID == "" || originID == "0" {
						t.Fatal("missing genuine standalone origin identity")
					}
					originLive := true
					t.Cleanup(func() {
						if originLive {
							ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
							defer cancel()
							if _, err := c.conn.ExecContext(ctx, "SELECT pg_replication_origin_drop($1)", origin); err != nil {
								t.Error("origin cleanup", err)
							}
						}
					})
					oldOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", a)
					peerOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", b)
					c.run(fixture.Exec("ALTER ROLE "+role+" SET statement_timeout='5s'", 0))
					c.run(fixture.Exec("ALTER DATABASE "+a+" SET statement_timeout='111ms'", 0))
					c.run(fixture.Exec("ALTER ROLE "+role+" IN DATABASE "+a+" SET lock_timeout='222ms'", 0))
					c.run(fixture.Exec("ALTER DATABASE "+b+" SET statement_timeout='333ms'", 0))
					c.run(fixture.Exec("ALTER ROLE "+role+" IN DATABASE "+b+" SET lock_timeout='444ms'", 0))
					c.run(fixture.Select("SELECT setrole=0 AS all_roles,setconfig::text FROM pg_db_role_setting WHERE setdatabase=$1::oid ORDER BY setrole",
						[]string{"all_roles", "setconfig"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{statement_timeout=111ms}"}, {false, "{lock_timeout=222ms}"}}, oldOID))
					aDB, aRaw, aRecord := c.pool(a, "")
					bDB, bRaw, bRecord := c.pool(b, "")
					for _, target := range []struct {
						db     fixture.Executor
						record *fixture.Recorder
						value  int64
					}{{aDB, aRecord, 11}, {bDB, bRecord, 22}} {
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE TABLE drop_value(v integer)", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("INSERT INTO drop_value VALUES($1)", 1, target.value))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Select("SELECT v FROM drop_value", []string{"v"}, []string{"INT4"}, [][]any{{target.value}}))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE INDEX drop_value_idx ON drop_value(v)", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE SEQUENCE drop_counter START 41", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Select("SELECT nextval('drop_counter')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(41)}}))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE TYPE drop_kind AS ENUM ('one','two')", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE SCHEMA drop_namespace", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE TABLE drop_namespace.scoped(v drop_kind)", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("CREATE EXTENSION hstore", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO "+role, 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Exec("COMMENT ON TABLE drop_value IS 'database local label'", 0))
						fixture.RunStep(t, p.Ctx, target.db, target.record, fixture.Select("SELECT lo_from_bytea(0,'\\x010203'::bytea)<>0 AS created", []string{"created"}, []string{"BOOL"}, [][]any{{true}}))
					}
					var detached driver.Rows
					if !backend.PG {
						owner, err := p.NativeConnector.WithDatabase(a).Connect(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { owner.Close() })
						detached, err = owner.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT v FROM drop_value", nil)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { detached.Close() })
						if err := owner.Close(); err != nil {
							t.Fatal(err)
						}
					}
					databaseDropClose(t, aDB, aRaw)
					fixture.DatabaseReferenceOwners(t, p, admin, record, a, 0)
					fixture.DatabaseReferenceOwners(t, p, admin, record, b, 1)
					route.run(c, fixture.Exec(query, 0))
					databaseDropNotice(t, &notices, a, 0)
					c.absent(a)
					c.run(fixture.Select("SELECT setdatabase::text FROM pg_db_role_setting WHERE setdatabase=$1::oid", []string{"setdatabase"}, []string{"TEXT"}, nil, oldOID))
					c.run(fixture.Select("SELECT dbid::text FROM pg_shdepend WHERE dbid=$1::oid OR (refclassid=1262 AND refobjid=$1::oid)", []string{"dbid"}, []string{"TEXT"}, nil, oldOID))
					missing, missingRaw, _ := c.pool(a, "")
					fatal := databaseCreateDiagnostic("3D000", "database \""+a+"\" does not exist")
					fatal.Severity, fatal.SeverityUnlocalized = "FATAL", "FATAL"
					fixture.AssertDiagnostic(t, missing.PingContext(p.Ctx), &fatal)
					databaseDropClose(t, missing, missingRaw)
					c.create(a, "", false)
					newOID := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", a)
					if newOID == oldOID || newOID == "0" {
						t.Fatalf("recreated identity %s reused retired %s", newOID, oldOID)
					}
					fresh, freshRaw, freshRecord := c.pool(a, "")
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('drop_value','drop_value_idx','drop_counter') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"0"}}))
					for _, inquiry := range []string{
						"SELECT count(*) FROM pg_type WHERE typname='drop_kind'",
						"SELECT count(*) FROM pg_namespace WHERE nspname='drop_namespace'",
						"SELECT count(*) FROM pg_extension WHERE extname='hstore'",
						"SELECT count(*) FROM pg_largeobject_metadata",
						"SELECT count(*) FROM pg_largeobject",
						"SELECT count(*) FROM pg_default_acl",
					} {
						fixture.RunStep(t, p.Ctx, fresh, freshRecord, fixture.Select(inquiry, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					}
					c.run(fixture.Select("SELECT setconfig::text FROM pg_db_role_setting WHERE setdatabase=$1::oid", []string{"setconfig"}, []string{"TEXT"}, nil, newOID))
					noTable := fixture.ErrorDiagnostic("SELECT v FROM drop_value", databaseCreateDiagnostic("42P01", `relation "drop_value" does not exist`))
					noTable.Diagnostic.Position = "15"
					fixture.RunStep(t, p.Ctx, fresh, freshRecord, noTable)
					fixture.RunStep(t, p.Ctx, bDB, bRecord, fixture.Select("SELECT v FROM drop_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
					fixture.RunStep(t, p.Ctx, bDB, bRecord, fixture.Select("SELECT nextval('drop_counter')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(42)}}))
					for _, inquiry := range []string{
						"SELECT count(*) FROM pg_type WHERE typname='drop_kind'",
						"SELECT count(*) FROM pg_namespace WHERE nspname='drop_namespace'",
						"SELECT count(*) FROM pg_extension WHERE extname='hstore'",
						"SELECT count(*) FROM pg_largeobject_metadata",
						"SELECT count(*) FROM pg_default_acl",
					} {
						fixture.RunStep(t, p.Ctx, bDB, bRecord, fixture.Select(inquiry, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
					c.run(fixture.Select("SELECT setrole=0 AS all_roles,setconfig::text FROM pg_db_role_setting WHERE setdatabase=$1::oid ORDER BY setrole", []string{"all_roles", "setconfig"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{statement_timeout=333ms}"}, {false, "{lock_timeout=444ms}"}}, peerOID))
					c.run(fixture.Select("SELECT setconfig::text FROM pg_db_role_setting WHERE setdatabase=0 AND setrole=(SELECT oid FROM pg_roles WHERE rolname=$1)", []string{"setconfig"}, []string{"TEXT"}, [][]any{{"{statement_timeout=5s}"}}, role))
					c.run(fixture.Select("SELECT roident::text,roname::text FROM pg_replication_origin WHERE roname=$1", []string{"roident", "roname"}, []string{"TEXT", "TEXT"}, [][]any{{originID, origin}}, origin))
					c.run(fixture.Select("SELECT x.privilege_type,x.is_grantable FROM pg_database d CROSS JOIN LATERAL aclexplode(d.datacl) x WHERE d.datname=$1 AND x.grantee=(SELECT oid FROM pg_roles WHERE rolname=$2)", []string{"privilege_type", "is_grantable"}, []string{"TEXT", "BOOL"}, [][]any{{"CONNECT", false}}, b, role))
					c.run(fixture.Select("SELECT spcname,spcacl::text FROM pg_tablespace WHERE oid IN(1663,1664) ORDER BY oid", []string{"spcname", "spcacl"}, []string{"NAME", "TEXT"}, [][]any{{"pg_default", nil}, {"pg_global", nil}}))
					c.run(fixture.Select("SELECT has_parameter_privilege($1,'work_mem','SET') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}}, role))
					c.run(fixture.Select("SELECT v FROM drop_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(33)}}))
					if c.scalar("SELECT current_database()") != defaultName || c.scalar("SELECT oid::text FROM pg_database WHERE datname=current_database()") != defaultOID || c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", b) != peerOID {
						t.Fatal("DROP changed default/peer identity")
					}
					databaseDropClose(t, fresh, freshRaw)
					fixture.DatabaseReferenceOwners(t, p, admin, record, a, 0)
					route.run(c, fixture.Exec(query, 0))
					c.absent(a)
					route.run(c, fixture.Exec(query, 0))
					databaseDropNotice(t, &notices, a, 1)
					if detached != nil {
						if cols := detached.Columns(); len(cols) != 1 || cols[0] != "v" || detached.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(0) != "INT4" {
							t.Fatal("retired detached row metadata changed", cols)
						}
						values := make([]driver.Value, 1)
						if err := detached.Next(values); err != nil || values[0] != int64(11) {
							t.Fatal("retired detached row changed", values, err)
						}
						if err := detached.Next(values); err != io.EOF {
							t.Fatal("detached row EOF", err)
						}
					}
					c.run(fixture.Select("SELECT pg_replication_origin_drop($1)", []string{"pg_replication_origin_drop"}, []string{"VOID"}, [][]any{{""}}, origin))
					originLive = false
					databaseDropClose(t, bDB, bRaw)
				})
			}
		})
	}
}
