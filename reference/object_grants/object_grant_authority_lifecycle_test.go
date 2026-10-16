package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// Expectations are source-derived PG18 contracts, pending the root's pinned
// oracle run. Each callback's discovery filter must be removed before native proof.
func TestReferenceObjectGrantAuthorityLifecycle(t *testing.T) {
	t.Run("grantor_precedence_and_origins", func(t *testing.T) {
		objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			defer objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
			for _, q := range []string{
				"GRANT EXECUTE ON FUNCTION og_missing() TO og_missing GRANTED BY og_missing_grantor",
				"REVOKE EXECUTE ON FUNCTION og_missing() FROM og_missing GRANTED BY og_missing_grantor",
			} {
				fixture.RoleGrantError(run, q, "42704", `role "og_missing_grantor" does not exist`)
			}
			for _, verb := range []string{"GRANT EXECUTE ON FUNCTION og_missing() TO ", "REVOKE EXECUTE ON FUNCTION og_missing() FROM "} {
				fixture.RoleGrantError(run, verb+"og_missing GRANTED BY og_object_other", "0A000", "grantor must be current user")
			}
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_other WITH GRANT OPTION GRANTED BY CURRENT_USER", 0))
			run(fixture.Exec("GRANT og_object_other TO og_object_reader WITH INHERIT TRUE", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader GRANTED BY og_object_other", "0A000", "grantor must be current user")
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader WITH GRANT OPTION GRANTED BY CURRENT_ROLE", 0))
			})
			run(fixture.Exec("REVOKE og_object_other FROM og_object_reader", 0))
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
			query := "SELECT abs($1::integer) AS v"
			stmt, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
			want := fixture.Select(query, []string{"v"}, []string{"INT4"}, [][]any{{int64(3)}}, int64(-3))
			read := func() {
				fixture.RoleGrantActor(run, "og_object_reader", func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, want) })
			}
			read() // The inherited grant was recorded under the option-bearing role.
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantError(run, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_other WITH GRANT OPTION", "0LP01", "grant options cannot be granted back to your own grantor")
			})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", 0))
			restrict := fixture.ImplicitError("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_other RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "dependent privileges exist", Hint: "Use CASCADE to revoke them too."})
			restrict.ErrorStage = "execute"
			run(restrict)
			read() // RESTRICT must not publish its valid prefix.
			run(fixture.Exec("REVOKE GRANT OPTION FOR EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_other CASCADE", 0))
			read() // Independent bootstrap-origin EXECUTE survives selective cascade.
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				denied := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for function abs"}, int64(-3))
				denied.SelectRows = true
				denied.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
				fixture.RoleGrantPermissionDenied(run, "SELECT abs(-3::integer) AS v", "function", "abs")
			})
			run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO PUBLIC", 0))
			var notices []fixture.Diagnostic
			_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "SET SESSION AUTHORIZATION og_object_reader")
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_other", fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01007", Message: `no privileges were granted for "abs"`})
			fixture.RoleGrantNoticeExec(t, p, nc, recorder, prepared, &notices, "RESET SESSION AUTHORIZATION")
			// PUBLIC use is not an option source: revoking the other's direct use
			// and PUBLIC leaves no recipient authority from the warned grant.
			run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_other,PUBLIC", 0))
			fixture.RoleGrantActor(run, "og_object_other", func() { fixture.RoleGrantPermissionDenied(run, "SELECT abs(-3::integer) AS v", "function", "abs") })
		})
	})

	t.Run("owner_use_and_role_dependencies", func(t *testing.T) {
		objectGrantScenario(t, []string{"og_life_owner"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
			run(fixture.Exec("CREATE SCHEMA og_life_owner AUTHORIZATION og_object_reader", 0))
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA og_life_owner TO og_object_other", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Exec("CREATE TYPE og_life_owner.e AS ENUM('a')", 0))
				run(fixture.Exec("REVOKE USAGE ON TYPE og_life_owner.e FROM PUBLIC,og_object_reader", 0))
				fixture.RoleGrantPermissionDenied(run, "CREATE TABLE og_life_owner.denied(x og_life_owner.e)", "type", "og_life_owner.e")
				// Owners retain implicit options even after revoking their own use.
				run(fixture.Exec("GRANT USAGE ON TYPE og_life_owner.e TO og_object_other WITH GRANT OPTION", 0))
			})
			fixture.RoleGrantActor(run, "og_object_other", func() {
				run(fixture.Exec("CREATE TABLE og_life_owner.allowed(x og_life_owner.e)", 0))
				run(fixture.Exec("INSERT INTO og_life_owner.allowed VALUES('a')", 1))
				run(fixture.Select("SELECT x::TEXT AS x FROM og_life_owner.allowed", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}}))
			})
			run(fixture.Exec("DROP TABLE og_life_owner.allowed", 0))
			run(fixture.Exec("REVOKE USAGE,CREATE ON SCHEMA og_life_owner FROM og_object_other", 0))
			run(fixture.RoleReferenceError("DROP ROLE og_object_other", "2BP01", `role "og_object_other" cannot be dropped because some objects depend on it`, "privileges for type og_life_owner.e", prepared, "execute"))
			run(fixture.Exec("DROP OWNED BY og_object_other", 0))
			run(fixture.Exec("DROP ROLE og_object_other", 0))
			run(fixture.Exec("CREATE ROLE og_object_other", 0))
			// The option-bearing recipient is also a downstream grantor. Cleanup
			// must remove its outgoing origin, not merely its incoming ACL item.
			run(fixture.Exec("CREATE SEQUENCE og_life_dep", 0))
			run(fixture.Exec("GRANT SELECT ON SEQUENCE og_life_dep TO og_object_other WITH GRANT OPTION", 0))
			fixture.RoleGrantActor(run, "og_object_other", func() { run(fixture.Exec("GRANT SELECT ON SEQUENCE og_life_dep TO og_object_reader", 0)) })
			run(fixture.RoleReferenceError("DROP ROLE og_object_other", "2BP01", `role "og_object_other" cannot be dropped because some objects depend on it`, "privileges for sequence og_life_dep", prepared, "execute"))
			run(fixture.Exec("GRANT SELECT ON SEQUENCE og_life_dep TO og_object_reader", 0))
			run(fixture.Exec("DROP OWNED BY og_object_other", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				run(fixture.Select("SELECT last_value FROM og_life_dep", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
			run(fixture.Exec("REVOKE SELECT ON SEQUENCE og_life_dep FROM og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_reader", func() {
				fixture.RoleGrantPermissionDenied(run, "SELECT last_value FROM og_life_dep", "sequence", "og_life_dep")
			})
			run(fixture.Exec("DROP ROLE og_object_other", 0))
			run(fixture.Exec("CREATE ROLE og_object_other", 0))
		})
	})

	t.Run("transferable_members_and_bootstrap_identity", func(t *testing.T) {
		objectGrantScenario(t, []string{"og_life_transfer"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
			bootstrap, _ := fixture.RoleGrantEnvironment(t, p, c)
			run(fixture.Exec("CREATE SCHEMA og_life_transfer AUTHORIZATION og_object_reader", 0))
			for _, ext := range []string{"unaccent", "pgcrypto", "fuzzystrmatch"} {
				run(fixture.Exec("CREATE EXTENSION "+ext+" SCHEMA og_life_transfer", 0))
			}
			fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec("CREATE TYPE og_life_transfer.e AS ENUM('a')", 0)) })
			if b.PG {
				for _, target := range []string{"og_life_transfer.unaccent(text)", "og_life_transfer.digest(text,text)", "og_life_transfer.daitch_mokotoff(text)"} {
					run(fixture.Exec("ALTER FUNCTION "+target+" OWNER TO og_object_reader", 0))
				}
			} else {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				script := objectGrantMemberOwnerScript(t, dump.String(), "og_object_reader")
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)); err != nil {
					t.Fatal(err)
				}
			}
			run(fixture.Exec("REASSIGN OWNED BY og_object_reader TO og_object_other", 0))
			run(fixture.Select("SELECT r.rolname FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='og_life_transfer'", []string{"rolname"}, []string{"NAME"}, [][]any{{"og_object_other"}}))
			for _, name := range []string{"unaccent", "digest", "daitch_mokotoff"} {
				// Only the selected formal was transferred, not its sibling overload.
				args := map[string]string{"unaccent": "text", "digest": "text,text", "daitch_mokotoff": "text"}[name]
				run(fixture.Select("SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid='og_life_transfer."+name+"("+args+")'::regprocedure::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{"og_object_other"}}))
			}
			run(fixture.Select("SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.oid IN (1397,4375) ORDER BY p.oid", []string{"rolname"}, []string{"NAME"}, [][]any{{bootstrap}, {bootstrap}}))
			fixture.RoleGrantActor(run, "og_object_other", func() {
				run(fixture.Exec("REVOKE USAGE ON TYPE og_life_transfer.e FROM PUBLIC", 0))
				run(fixture.Exec("GRANT USAGE ON TYPE og_life_transfer.e TO og_object_reader", 0))
				for _, target := range []string{"unaccent(text)", "digest(text,text)", "daitch_mokotoff(text)"} {
					run(fixture.Exec("GRANT EXECUTE ON FUNCTION og_life_transfer."+target+" TO og_object_reader WITH GRANT OPTION", 0))
				}
				run(fixture.Exec("CREATE TABLE og_life_transfer.owned(x og_life_transfer.e)", 0))
				run(fixture.Select("SELECT og_life_transfer.unaccent('Hôtel') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}}))
			})
			// DROP OWNED for an ACL-only principal removes rights, not the actual
			// extension provider or the independently reassigned namespace/type.
			run(fixture.Exec("DROP OWNED BY og_object_reader", 0))
			fixture.RoleGrantActor(run, "og_object_other", func() {
				run(fixture.Exec("INSERT INTO og_life_transfer.owned VALUES('a')", 1))
				run(fixture.Select("SELECT x::TEXT AS x FROM og_life_transfer.owned", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}}))
			})
			run(fixture.Exec("REASSIGN OWNED BY og_object_other TO "+bootstrap, 0))
		})
	})
}

// Reuse the public named-owner Restore format, as the ownership provider
// witnesses do; native has no direct ALTER FUNCTION OWNER grammar.
func objectGrantMemberOwnerScript(t *testing.T, script, owner string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v1 "
	lines := strings.Split(script, "\n")
	encodedOwner, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for i, line := range lines {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, marker))
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		var members []map[string]json.RawMessage
		if err := json.Unmarshal(record["members"], &members); err != nil {
			t.Fatal(err)
		}
		for _, member := range members {
			var identity struct{ Kind, Name, Signature string }
			data, err := json.Marshal(member)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &identity); err != nil {
				t.Fatal(err)
			}
			if identity.Kind == "procedure" && (identity.Name == "unaccent" && identity.Signature == "25" ||
				identity.Name == "digest" && identity.Signature == "25,25" ||
				identity.Name == "daitch_mokotoff" && identity.Signature == "25") {
				member["owner"] = encodedOwner
				changed++
			}
		}
		record["members"], err = json.Marshal(members)
		if err != nil {
			t.Fatal(err)
		}
		data, err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = marker + base64.StdEncoding.EncodeToString(data)
	}
	if changed != 3 {
		t.Fatalf("missing selected actual provider owner records: got %d, want 3", changed)
	}
	return strings.Join(lines, "\n")
}
