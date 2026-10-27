package reference_test

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// PostgreSQL consumes the actual native stream in the same shared Pair. There
// is no PostgreSQL Restore API and no independently authored duplicate schema.
func TestReferenceRLSPolicyPostgresReplay(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"policy_replay_owner", "policy_replay_target"})
			p := fixture.NewScenario(t)
			for _, role := range []string{"policy_replay_owner", "policy_replay_target"} {
				p.Step(t, fixture.Exec("CREATE ROLE "+role+" LOGIN", 0))
			}
			c, err := p.Memory.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run := func(step fixture.Step) {
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, p.MemoryRecord, step)
			}
			exec := func(q string) { run(fixture.Exec(q, 0)) }
			exec("SET SESSION AUTHORIZATION policy_replay_owner")
			fixture.WithPublicSchemaOwner(t, p, fixture.Backend{Name: "memory", DB: p.Memory, Recorder: p.MemoryRecord}, "policy_replay_owner", func() { exec("CREATE TABLE t(id INTEGER,allowed BOOLEAN)") })
			run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE)", 2))
			exec("RESET SESSION AUTHORIZATION")
			exec("CREATE TABLE replay_helper(id INTEGER)")
			run(fixture.Exec("INSERT INTO replay_helper VALUES(1)", 1))
			exec("CREATE DOMAIN replay_domain AS INTEGER")
			exec("CREATE COLLATION replay_collation FROM \"C\"")
			exec("CREATE SEQUENCE replay_sequence START 40")
			exec("CREATE TABLE replay_type(id INTEGER)")
			run(fixture.Exec("INSERT INTO replay_type VALUES(11)", 1))
			exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_replay_target USING(EXISTS(SELECT 1 FROM replay_helper WHERE replay_helper.id=t.id) AND id::replay_domain>0 AND 'a' COLLATE replay_collation='a' AND nextval('replay_sequence')>0 AND 'replay_type'::regtype IS NOT NULL) WITH CHECK(allowed AND CURRENT_USER IS NOT NULL AND id::replay_domain>0 AND 'a' COLLATE replay_collation='a' AND nextval('replay_sequence')>0 AND 'replay_type'::regtype IS NOT NULL AND NULL::regtype IS NULL)")
			exec("CREATE VIEW replay_type_view AS SELECT 'replay_type'::regtype AS type_identity")
			exec("ALTER TABLE replay_type RENAME TO renamed_replay_type")
			exec("CREATE TABLE replay_type(id INTEGER)")
			run(fixture.Exec("INSERT INTO replay_type VALUES(22)", 1))
			metadata := pgPolicyStructuralRow(pgPolicyRow("t", "p", "w", false, "policy_replay_target", "present", "present"))
			run(metadata)
			var stream bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(stream.String(), "CREATE ROLE") || !strings.Contains(stream.String(), "policy_replay_owner") || !strings.Contains(stream.String(), "policy_replay_target") || !strings.Contains(stream.String(), "renamed_replay_type") {
				t.Fatal("emitted role/type identities", stream.String())
			}
			for range 2 {
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || !bytes.Equal(stream.Bytes(), again.Bytes()) {
					t.Fatal("native stable redump", err, again.String())
				}
				run(pgPolicyStructuralRow(pgPolicyRow("t", "p", "w", false, "policy_replay_target", "present", "present")))
			}
			if p.Postgres != nil {
				oracle, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer oracle.Close()
				defer func() {
					// The emitted stream contains owner directives. Restore the
					// bootstrap actor even on partial replay failure, then remove
					// consumer objects before the fixture cleans cluster roles.
					oracle.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
					oracle.ExecContext(context.Background(), "DROP VIEW IF EXISTS replay_type_view; DROP TABLE IF EXISTS t,replay_helper,renamed_replay_type,replay_type CASCADE; DROP DOMAIN IF EXISTS replay_domain CASCADE; DROP COLLATION IF EXISTS replay_collation CASCADE; DROP SEQUENCE IF EXISTS replay_sequence CASCADE")
				}()
				// Roles preexist on the consumer. Scoped ownership is explicit
				// environmental setup; the emitted stream itself stays unchanged.
				fixture.WithPublicSchemaOwner(t, p, fixture.Backend{Name: "postgres", DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}, "policy_replay_owner", func() {
					if _, err := oracle.ExecContext(p.Ctx, stream.String()); err != nil {
						t.Fatal("pinned PostgreSQL rejected actual native policy stream", err)
					}
				})
				if _, err := oracle.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
				step := pgPolicyStructuralRow(pgPolicyRow("t", "p", "w", false, "policy_replay_target", "present", "present"))
				step.Prepared = prepared
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, step)
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, fixture.Prepared(fixture.Select(`SELECT r.rolname AS owner,p.polname,p.polpermissive,p.polcmd::text AS command,p.polroles=ARRAY[(SELECT oid FROM pg_roles WHERE rolname='policy_replay_target')]::oid[] AS targets,p.polqual IS NOT NULL AS using_present,p.polwithcheck IS NOT NULL AS check_present FROM pg_policy p JOIN pg_class c ON c.oid=p.polrelid JOIN pg_roles r ON r.oid=c.relowner WHERE p.polrelid='t'::regclass`, []string{"owner", "polname", "polpermissive", "command", "targets", "using_present", "check_present"}, []string{"NAME", "NAME", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"policy_replay_owner", "p", false, "w", true, true, true}})))
				// Each provider edge is expected independently of native output;
				// the recreated old row-type name must not be adopted on replay.
				for _, provider := range []struct{ class, identity string }{
					{"pg_class", "'replay_helper'::regclass"},
					{"pg_type", "'replay_domain'::regtype"},
					{"pg_collation", "'replay_collation'::regcollation"},
					{"pg_class", "'replay_sequence'::regclass"},
					{"pg_type", "'renamed_replay_type'::regtype"},
				} {
					q := "SELECT EXISTS(SELECT 1 FROM pg_depend d JOIN pg_policy p ON p.oid=d.objid WHERE d.classid='pg_policy'::regclass AND p.polrelid='t'::regclass AND p.polname='p' AND d.refclassid='" + provider.class + "'::regclass AND d.refobjid=" + provider.identity + " AND d.deptype='n') AS linked"
					fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, fixture.Select(q, []string{"linked"}, []string{"BOOL"}, [][]any{{true}}))
				}
				fixture.RunStep(t, p.Ctx, oracle, p.PostgresRecord, fixture.Select(`SELECT EXISTS(SELECT 1 FROM pg_depend d JOIN pg_policy p ON p.oid=d.objid WHERE d.classid='pg_policy'::regclass AND p.polrelid='t'::regclass AND d.refclassid='pg_type'::regclass AND d.refobjid='replay_type'::regtype) AS adopted`, []string{"adopted"}, []string{"BOOL"}, [][]any{{false}}))
				var using, check string
				if err := oracle.QueryRowContext(p.Ctx, "SELECT pg_get_expr(polqual,polrelid),pg_get_expr(polwithcheck,polrelid) FROM pg_policy WHERE polrelid='t'::regclass AND polname='p'").Scan(&using, &check); err != nil {
					t.Fatal(err)
				}
				for _, fragment := range []string{"replay_helper", "t.id", "replay_domain", "replay_collation", "replay_sequence", "renamed_replay_type"} {
					if !strings.Contains(using, fragment) {
						t.Fatal("replayed USING omitted", fragment, using)
					}
				}
				for _, fragment := range []string{"allowed", "CURRENT_USER", "replay_domain", "replay_collation", "replay_sequence", "renamed_replay_type", "NULL::regtype"} {
					if !strings.Contains(check, fragment) {
						t.Fatal("replayed CHECK omitted", fragment, check)
					}
				}
			}
			for _, step := range []fixture.Step{
				fixture.Select("SELECT id,allowed FROM t ORDER BY id", []string{"id", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}),
				fixture.Select("SELECT id FROM replay_helper", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
				fixture.Select("SELECT id FROM renamed_replay_type", []string{"id"}, []string{"INT4"}, [][]any{{int64(11)}}),
				fixture.Select("SELECT id FROM replay_type", []string{"id"}, []string{"INT4"}, [][]any{{int64(22)}}),
				fixture.Select("SELECT last_value,is_called FROM replay_sequence", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(40), false}}),
				fixture.Select("SELECT type_identity='renamed_replay_type'::regtype AS same_identity,type_identity='replay_type'::regtype AS replacement_identity FROM replay_type_view", []string{"same_identity", "replacement_identity"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}),
			} {
				step.Prepared = prepared
				p.Step(t, step)
			}
			for _, provider := range []struct{ drop, subject string }{
				{"DROP TABLE replay_helper", "table replay_helper"},
				{"DROP DOMAIN replay_domain", "type replay_domain"},
				{"DROP COLLATION replay_collation", "collation replay_collation"},
				{"DROP SEQUENCE replay_sequence", "sequence replay_sequence"},
			} {
				step := fixture.ImplicitError(provider.drop, fixture.Diagnostic{Code: "2BP01", Message: "cannot drop " + provider.subject + " because other objects depend on it", Detail: "policy p on table t depends on " + provider.subject, Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
				step.Prepared, step.ErrorStage = prepared, "execute"
				p.Step(t, step)
			}
			p.Step(t, fixture.Exec("DROP POLICY p ON t", 0))
			p.Step(t, fixture.Exec("DROP ROLE policy_replay_target", 0))
		})
	}
}
