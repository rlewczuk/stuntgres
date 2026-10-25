package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"github.com/ngrok/sqlmw"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceRangePartitionOwnerControls(t *testing.T) {
	roles := []string{"rp_owned_a", "rp_owned_b", "rp_owned_c"}
	fixture.RoleReferenceCleanup(t, roles)
	fixture.SchemaReferenceCleanup(t, "rp_owned_a_schema", "rp_owned_b_schema", "rp_owned_c_schema")
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		for _, role := range roles {
			run(fixture.Exec("CREATE USER "+role, 0))
		}
		definitions := []string{
			"CREATE SCHEMA rp_owned_a_schema AUTHORIZATION rp_owned_a",
			"CREATE SCHEMA rp_owned_b_schema AUTHORIZATION rp_owned_b",
			"CREATE SCHEMA rp_owned_c_schema AUTHORIZATION rp_owned_c",
			"CREATE TABLE public.rp_owned_gate(k INT) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_owned_a_schema.rp_owned_root(k INT NOT NULL,payload INT DEFAULT 7,CONSTRAINT rp_owned_check CHECK(payload>0)) PARTITION BY RANGE(k)",
			"CREATE TABLE rp_owned_b_schema.rp_owned_middle PARTITION OF rp_owned_a_schema.rp_owned_root FOR VALUES FROM(1) TO(5) PARTITION BY RANGE(payload)",
			"CREATE TABLE rp_owned_c_schema.rp_owned_leaf PARTITION OF rp_owned_b_schema.rp_owned_middle(CONSTRAINT rp_owned_payload_unique UNIQUE(payload)) FOR VALUES FROM(1) TO(20)",
		}
		for _, query := range definitions {
			run(fixture.Exec(query, 0))
		}
		if b.PG {
			// Recreated public has no actor privileges. Supply only the namespace
			// rights needed to reach the genuine parent-owner denial below.
			// Register cleanup before granting, including failed prepared routes.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET SESSION AUTHORIZATION", "REVOKE USAGE,CREATE ON SCHEMA public FROM rp_owned_b"} {
					if _, err := conn.ExecContext(ctx, query); err != nil {
						t.Error(err)
					}
				}
			}()
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO rp_owned_b", 0))
		}
		// This ordinary owner denial is exercised before any oracle-only importer
		// provisioning. Native parent-only Restore authority is not a SQL privilege.
		run(fixture.Exec("SET SESSION AUTHORIZATION rp_owned_b", 0))
		for _, prepared := range []bool{false, true} {
			step := referenceRangeError("CREATE TABLE rp_owned_b_schema.rp_owned_denied PARTITION OF public.rp_owned_gate FOR VALUES FROM(1) TO(3)", "42501", "must be owner of table rp_owned_gate", fixture.Diagnostic{})
			step.Prepared = prepared
			run(step)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if b.PG {
			run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA public FROM rp_owned_b", 0))
		}
		for _, query := range []string{"ALTER TABLE rp_owned_a_schema.rp_owned_root OWNER TO rp_owned_a", "ALTER TABLE rp_owned_b_schema.rp_owned_middle OWNER TO rp_owned_b", "ALTER TABLE rp_owned_c_schema.rp_owned_leaf OWNER TO rp_owned_c"} {
			run(fixture.Exec(query, 0))
		}
		run(fixture.Exec("CREATE SEQUENCE rp_owned_a_schema.rp_owned_mismatch_seq", 0))
		for _, prepared := range []bool{false, true} {
			step := referenceRangeError("ALTER SEQUENCE rp_owned_a_schema.rp_owned_mismatch_seq OWNED BY rp_owned_a_schema.rp_owned_root.payload", "55000", "sequence must have same owner as table it is linked to", fixture.Diagnostic{})
			step.Prepared = prepared
			run(step)
		}
		run(fixture.Select("SELECT COUNT(*) FROM pg_depend WHERE objid='rp_owned_a_schema.rp_owned_mismatch_seq'::regclass::OID AND classid=1259 AND refclassid=1259 AND deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("DROP SEQUENCE rp_owned_a_schema.rp_owned_mismatch_seq", 0))
		run(fixture.Exec("INSERT INTO rp_owned_a_schema.rp_owned_root(k,payload) VALUES(1,7),(3,8)", 2))
		check := func(db fixture.Executor, record *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT * FROM rp_owned_a_schema.rp_owned_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(8)}}))
			fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT c.relname,t.schemaname AS nspname,t.tableowner AS rolname,c.relkind AS kind FROM pg_class c JOIN pg_tables t ON t.tablename=c.relname WHERE c.relname='rp_owned_root' OR c.relname='rp_owned_middle' OR c.relname='rp_owned_leaf' ORDER BY c.relname", []string{"relname", "nspname", "rolname", "kind"}, []string{"NAME", "NAME", "NAME", "CHAR"}, [][]any{{"rp_owned_leaf", "rp_owned_c_schema", "rp_owned_c", "r"}, {"rp_owned_middle", "rp_owned_b_schema", "rp_owned_b", "p"}, {"rp_owned_root", "rp_owned_a_schema", "rp_owned_a", "p"}}))
			fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='rp_owned_c_schema.rp_owned_leaf'::regclass::OID AND contype='u'", []string{"conname"}, []string{"NAME"}, [][]any{{"rp_owned_payload_unique"}}))
			fixture.RunStep(t, p.Ctx, db, record, fixture.Select("SELECT relname FROM pg_class WHERE relname='rp_owned_denied'", []string{"relname"}, []string{"NAME"}, nil))
		}
		check(conn, b.Recorder)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(conn, b.Recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("mixed-owner redump changed", err)
		}
		driver := stuntgres.NewDriver()
		raw, err := driver.OpenConnector("range-owner-destination")
		if err != nil {
			t.Fatal(err)
		}
		native := sql.OpenDB(raw)
		defer native.Close()
		wrapped := sqlmw.Driver(driver, fixture.Interceptor{})
		configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("range-owner-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(configured)
		defer destination.Close()
		record := &fixture.Recorder{}
		// Different role allocation order proves named principal remapping rather
		// than accidental equality of source and destination principal IDs.
		for i := range len(roles) {
			fixture.RunStep(t, p.Ctx, destination, record, fixture.Exec("CREATE USER "+roles[len(roles)-1-i], 0))
		}
		if err := stuntgres.Restore(p.Ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		check(destination, record)
		fixture.RunStep(t, p.Ctx, destination, record, referenceRangeError("INSERT INTO rp_owned_a_schema.rp_owned_root VALUES(4,9),(2,7)", "23505", `duplicate key value violates unique constraint "rp_owned_payload_unique"`, fixture.Diagnostic{Detail: "Key (payload)=(7) already exists.", Schema: "rp_owned_c_schema", Table: "rp_owned_leaf", Constraint: "rp_owned_payload_unique"}))
		check(destination, record)
		if p.Postgres != nil {
			oracle, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer oracle.Close()
			// The unchanged Dump stream imports under each named owner. PostgreSQL
			// requires explicitly provisioned reconstruction rights; native derives
			// only parent lookup/ownership from its private importer capability.
			cleanup := func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				for _, query := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "REVOKE rp_owned_b FROM rp_owned_c", "REVOKE rp_owned_a FROM rp_owned_b", "DROP SCHEMA IF EXISTS rp_owned_a_schema CASCADE", "DROP SCHEMA IF EXISTS rp_owned_b_schema CASCADE", "DROP SCHEMA IF EXISTS rp_owned_c_schema CASCADE", "DROP TABLE IF EXISTS public.rp_owned_gate"} {
					if _, err := oracle.ExecContext(cleanupCtx, query); err != nil {
						t.Error(err)
					}
				}
				for i := range len(roles) {
					if _, err := oracle.ExecContext(cleanupCtx, "DROP ROLE IF EXISTS "+roles[len(roles)-1-i]); err != nil {
						t.Error(err)
					}
				}
			}
			defer cleanup()
			for _, role := range roles {
				if _, err := oracle.ExecContext(p.Ctx, "CREATE USER "+role); err != nil {
					t.Fatal(err)
				}
			}
			for _, query := range []string{"GRANT rp_owned_a TO rp_owned_b", "GRANT rp_owned_b TO rp_owned_c"} {
				if _, err := oracle.ExecContext(p.Ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := oracle.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL rejected actual mixed-owner dump", err)
			}
			check(oracle, p.PostgresRecord)
		}
	})
}
