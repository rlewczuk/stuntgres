package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"github.com/ngrok/sqlmw"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceRangePartitionOwnerControls(t *testing.T) {
	roles := []string{"rp_owned_a", "rp_owned_b", "rp_owned_c"}
	roleReferenceCleanup(t, roles)
	schemaReferenceCleanup(t, "rp_owned_a_schema", "rp_owned_b_schema", "rp_owned_c_schema")
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		conn, err := b.db.Conn(p.ctx)
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
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		for _, role := range roles {
			run(referenceExec("CREATE USER "+role, 0))
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
			run(referenceExec(query, 0))
		}
		if b.pg {
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
			run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO rp_owned_b", 0))
		}
		// This ordinary owner denial is exercised before any oracle-only importer
		// provisioning. Native parent-only Restore authority is not a SQL privilege.
		run(referenceExec("SET SESSION AUTHORIZATION rp_owned_b", 0))
		for _, prepared := range []bool{false, true} {
			step := referenceRangeError("CREATE TABLE rp_owned_b_schema.rp_owned_denied PARTITION OF public.rp_owned_gate FOR VALUES FROM(1) TO(3)", "42501", "must be owner of table rp_owned_gate", referenceDiagnostic{})
			step.prepared = prepared
			run(step)
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		if b.pg {
			run(referenceExec("REVOKE USAGE,CREATE ON SCHEMA public FROM rp_owned_b", 0))
		}
		if b.pg {
			for _, query := range []string{"ALTER TABLE rp_owned_a_schema.rp_owned_root OWNER TO rp_owned_a", "ALTER TABLE rp_owned_b_schema.rp_owned_middle OWNER TO rp_owned_b", "ALTER TABLE rp_owned_c_schema.rp_owned_leaf OWNER TO rp_owned_c"} {
				run(referenceExec(query, 0))
			}
		} else {
			// Real private Restore reconstructs the named owners without granting B
			// live ownership of A or elevating B's child type/expression authority.
			script := strings.Join(definitions[:4], "; ") + "; SET SESSION AUTHORIZATION 'rp_owned_a'; " + definitions[4] + "; SET SESSION AUTHORIZATION 'rp_owned_b'; " + definitions[5] + "; SET SESSION AUTHORIZATION 'rp_owned_c'; " + definitions[6] + ";"
			if err := stuntgres.Restore(p.ctx, p.native, strings.NewReader(script)); err != nil {
				t.Fatal(err)
			}
		}
		run(referenceExec("CREATE SEQUENCE rp_owned_a_schema.rp_owned_mismatch_seq", 0))
		for _, prepared := range []bool{false, true} {
			step := referenceRangeError("ALTER SEQUENCE rp_owned_a_schema.rp_owned_mismatch_seq OWNED BY rp_owned_a_schema.rp_owned_root.payload", "55000", "sequence must have same owner as table it is linked to", referenceDiagnostic{})
			step.prepared = prepared
			run(step)
		}
		run(referenceSelect("SELECT COUNT(*) FROM pg_depend WHERE objid='rp_owned_a_schema.rp_owned_mismatch_seq'::regclass::OID AND classid=1259 AND refclassid=1259 AND deptype='a'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(referenceExec("DROP SEQUENCE rp_owned_a_schema.rp_owned_mismatch_seq", 0))
		run(referenceExec("INSERT INTO rp_owned_a_schema.rp_owned_root(k,payload) VALUES(1,7),(3,8)", 2))
		check := func(db referenceExecutor, record *referenceRecorder) {
			referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT * FROM rp_owned_a_schema.rp_owned_root ORDER BY k", []string{"k", "payload"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(7)}, {int64(3), int64(8)}}))
			referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT c.relname,t.schemaname AS nspname,t.tableowner AS rolname,c.relkind AS kind FROM pg_class c JOIN pg_tables t ON t.tablename=c.relname WHERE c.relname='rp_owned_root' OR c.relname='rp_owned_middle' OR c.relname='rp_owned_leaf' ORDER BY c.relname", []string{"relname", "nspname", "rolname", "kind"}, []string{"NAME", "NAME", "NAME", "CHAR"}, [][]any{{"rp_owned_leaf", "rp_owned_c_schema", "rp_owned_c", "r"}, {"rp_owned_middle", "rp_owned_b_schema", "rp_owned_b", "p"}, {"rp_owned_root", "rp_owned_a_schema", "rp_owned_a", "p"}}))
			referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT conname FROM pg_constraint WHERE conrelid='rp_owned_c_schema.rp_owned_leaf'::regclass::OID AND contype='u'", []string{"conname"}, []string{"NAME"}, [][]any{{"rp_owned_payload_unique"}}))
			referenceRunStep(t, p.ctx, db, record, referenceSelect("SELECT relname FROM pg_class WHERE relname='rp_owned_denied'", []string{"relname"}, []string{"NAME"}, nil))
		}
		check(conn, b.recorder)
		if b.pg {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(conn, b.recorder)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.ctx, p.native, &redump); err != nil || redump.String() != dump.String() {
			t.Fatal("mixed-owner redump changed", err)
		}
		driver := stuntgres.NewDriver()
		raw, err := driver.OpenConnector("range-owner-destination")
		if err != nil {
			t.Fatal(err)
		}
		native := sql.OpenDB(raw)
		defer native.Close()
		wrapped := sqlmw.Driver(driver, referenceInterceptor{})
		configured, err := wrapped.(sqldriver.DriverContext).OpenConnector("range-owner-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(configured)
		defer destination.Close()
		record := &referenceRecorder{}
		// Different role allocation order proves named principal remapping rather
		// than accidental equality of source and destination principal IDs.
		for i := range len(roles) {
			referenceRunStep(t, p.ctx, destination, record, referenceExec("CREATE USER "+roles[len(roles)-1-i], 0))
		}
		if err := stuntgres.Restore(p.ctx, native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.ctx, p.native); err != nil {
			t.Fatal(err)
		}
		check(destination, record)
		referenceRunStep(t, p.ctx, destination, record, referenceRangeError("INSERT INTO rp_owned_a_schema.rp_owned_root VALUES(4,9),(2,7)", "23505", `duplicate key value violates unique constraint "rp_owned_payload_unique"`, referenceDiagnostic{Detail: "Key (payload)=(7) already exists.", Schema: "rp_owned_c_schema", Table: "rp_owned_leaf", Constraint: "rp_owned_payload_unique"}))
		check(destination, record)
		if p.postgres != nil {
			oracle, err := p.postgres.Conn(p.ctx)
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
				if _, err := oracle.ExecContext(p.ctx, "CREATE USER "+role); err != nil {
					t.Fatal(err)
				}
			}
			for _, query := range []string{"GRANT rp_owned_a TO rp_owned_b", "GRANT rp_owned_b TO rp_owned_c"} {
				if _, err := oracle.ExecContext(p.ctx, query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := oracle.ExecContext(p.ctx, dump.String()); err != nil {
				t.Fatal("PostgreSQL rejected actual mixed-owner dump", err)
			}
			check(oracle, p.postgresRecord)
		}
	})
}
