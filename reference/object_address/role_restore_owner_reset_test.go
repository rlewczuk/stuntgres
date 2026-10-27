package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"github.com/lib/pq"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleRestoreOwnerReset(t *testing.T) {
	const ownerA = "rr_reset_a'quote"
	const ownerB = `rr_reset_b\slash`
	fixture.RoleReferenceCleanup(t, []string{ownerA, ownerB, "rr_reset_forbidden"})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			admin, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			defer admin.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			peer, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			defer peer.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			probe, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer probe.Close()
			defer func() {
				if _, err := probe.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION; DROP SCHEMA IF EXISTS rr_reset_a CASCADE; DROP SCHEMA IF EXISTS rr_reset_b CASCADE; DROP TABLE IF EXISTS rr_reset_admin"); err != nil {
					t.Error("owner reset cleanup", err)
				}
			}()
			run := func(db fixture.Executor, step fixture.Step) { fixture.RunStep(t, p.Ctx, db, backend.Recorder, step) }
			both := func(db fixture.Executor, step fixture.Step) { run(db, step); run(db, fixture.Prepared(step)) }
			bootstrap := "postgres"
			if backend.PG {
				bootstrap = "stuntgres"
			}
			for _, query := range []string{
				"CREATE USER " + pq.QuoteIdentifier(ownerA), "CREATE USER " + pq.QuoteIdentifier(ownerB),
				"CREATE SCHEMA rr_reset_a AUTHORIZATION " + pq.QuoteIdentifier(ownerA),
				"CREATE SCHEMA rr_reset_b AUTHORIZATION " + pq.QuoteIdentifier(ownerB),
			} {
				run(admin, fixture.Exec(query, 0))
			}
			roleID := func(name string) int64 {
				var id int64
				if err := admin.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", name).Scan(&id); err != nil || id <= 0 {
					t.Fatal("own role identity", name, id, err)
				}
				return id
			}
			aID, bID, bootstrapID := roleID(ownerA), roleID(ownerB), roleID(bootstrap)
			run(admin, fixture.Exec("SET SESSION AUTHORIZATION "+pq.QuoteIdentifier(ownerB), 0))
			run(admin, fixture.Exec("CREATE TABLE rr_reset_b.first(id integer)", 0))
			run(admin, fixture.Exec("INSERT INTO rr_reset_b.first VALUES(7)", 1))
			run(admin, fixture.Exec("SET SESSION AUTHORIZATION "+pq.QuoteIdentifier(ownerA), 0))
			run(admin, fixture.Exec("CREATE TABLE rr_reset_a.second(id integer)", 0))
			run(admin, fixture.Exec("INSERT INTO rr_reset_a.second VALUES(8)", 1))
			run(peer, fixture.Exec("SET SESSION AUTHORIZATION "+pq.QuoteIdentifier(ownerB), 0))
			identity := func(db fixture.Executor, name string) {
				both(db, fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{name, name}}))
			}
			tableOwner := func(db *sql.Conn, table string, owner int64) {
				var id int64
				if err := db.QueryRowContext(p.Ctx, "SELECT '"+table+"'::regclass::oid").Scan(&id); err != nil || id <= 0 {
					t.Fatal("own table identity", table, id, err)
				}
				both(db, fixture.Select("SELECT relowner::bigint,relacl FROM pg_class WHERE oid=$1", []string{"relowner", "relacl"}, []string{"INT8", "_ACLITEM"}, [][]any{{owner, nil}}, id))
			}
			check := func() {
				identity(admin, ownerA)
				identity(peer, ownerB)
				both(admin, fixture.Select("SELECT id FROM rr_reset_a.second", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
				both(peer, fixture.Select("SELECT id FROM rr_reset_b.first", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				tableOwner(admin, "rr_reset_a.second", aID)
				tableOwner(peer, "rr_reset_b.first", bID)
				both(admin, fixture.Select("SELECT oid::bigint,rolname,rolcanlogin,rolsuper FROM pg_roles WHERE rolname IN ($1,$2) ORDER BY rolname", []string{"oid", "rolname", "rolcanlogin", "rolsuper"}, []string{"INT8", "NAME", "BOOL", "BOOL"}, [][]any{{aID, ownerA, true, false}, {bID, ownerB, true, false}}, ownerA, ownerB))
			}
			marker := func() {
				tableOwner(probe, "public.rr_reset_admin", bootstrapID)
				both(probe, fixture.Select("SELECT marker FROM rr_reset_admin", []string{"marker"}, []string{"INT4"}, [][]any{{int64(9)}}))
			}
			check()
			resets := []string{"RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION DEFAULT", "SET AUTHORIZATION DEFAULT"}
			for _, prepared := range []bool{false, true} {
				for _, reset := range resets {
					run(probe, fixture.Exec("SET SESSION AUTHORIZATION "+pq.QuoteIdentifier(ownerB), 0))
					step := fixture.Exec(reset, 0)
					if backend.PG && reset == "SET AUTHORIZATION DEFAULT" {
						// This preexisting native alias is not PostgreSQL 18
						// syntax. Failure must preserve the probe's current owner.
						want := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "AUTHORIZATION"`)
						want.Position = "5"
						step = fixture.ErrorDiagnostic(reset, want)
						step.Prepared, step.ErrorStage = prepared, "execute"
						if prepared {
							step.ErrorStage = "prepare"
						}
						run(probe, step)
						identity(probe, ownerB)
						run(probe, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
						identity(probe, bootstrap)
						check()
						continue
					}
					if prepared {
						step = fixture.Prepared(step)
					}
					run(probe, step)
					identity(probe, bootstrap)
					run(probe, fixture.Exec("CREATE TABLE rr_reset_admin(marker integer DEFAULT 9)", 0))
					run(probe, fixture.Exec("INSERT INTO rr_reset_admin DEFAULT VALUES", 1))
					marker()
					run(probe, fixture.Exec("DROP TABLE rr_reset_admin", 0))
					check()
				}
			}
			if backend.PG {
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
			check()
			for _, reset := range resets {
				script := dump.String() + reset + "; CREATE TABLE rr_reset_admin(marker integer DEFAULT 9); INSERT INTO rr_reset_admin DEFAULT VALUES;"
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
					t.Fatal("isolated administrative reset", reset, err)
				}
				marker()
				check()
			}
			const roleMutationMessage = "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX/POLICY/RULE, materialized extension dependency ALTER, row-security/existing-index/eligible NOT VALID ALTER TABLE, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION"
			for _, rejected := range []struct {
				query   string
				message string
			}{
				{"SET AUTHORIZATION " + pq.QuoteLiteral(bootstrap), "restore accepts only named SET SESSION AUTHORIZATION owner directives or DEFAULT"},
				{"CREATE ROLE rr_reset_forbidden", roleMutationMessage},
				{"ALTER ROLE " + pq.QuoteIdentifier(ownerA) + " NOLOGIN", roleMutationMessage},
				{"DROP ROLE " + pq.QuoteIdentifier(ownerB), roleMutationMessage},
			} {
				script := dump.String() + "SET SESSION AUTHORIZATION DEFAULT; CREATE TABLE rr_reset_unpublished(id integer); " + rejected.query + ";"
				err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script))
				want := fixture.ControlsReferenceDiagnostic("0A000", rejected.message)
				fixture.AssertDiagnostic(t, err, &want)
				marker()
				check()
				both(probe, fixture.Select("SELECT relname FROM pg_class WHERE relname='rr_reset_unpublished'", []string{"relname"}, []string{"NAME"}, nil))
				both(probe, fixture.Select("SELECT rolname FROM pg_roles WHERE rolname='rr_reset_forbidden'", []string{"rolname"}, []string{"NAME"}, nil))
			}
		})
	}
}
