package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Active expectations are independently grounded in REL_18_6, not an installed
// PostgreSQL SELinux provider. The ordinary phase retains provider-first errors
// and exercises separate released role/domain/comment ownership contracts.
func TestReferenceSepgsqlSimulationGenericLabelRoleOwnership(t *testing.T) {
	roles := []string{"sepgsql_gr_plain", "sepgsql_gr_creator", "sepgsql_gr_adminonly", "sepgsql_gr_member", "sepgsql_gr_target", "sepgsql_gr_super", "sepgsql_gr_bridge", "sepgsql_gr_indirect"}
	body := func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool, section string) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		both := func(s fixture.Step) { t.Helper(); run(s); run(fixture.Prepared(s)) }
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{
			"CREATE ROLE sepgsql_gr_plain", "CREATE ROLE sepgsql_gr_creator CREATEROLE", "CREATE ROLE sepgsql_gr_adminonly", "CREATE ROLE sepgsql_gr_member CREATEROLE",
			"CREATE ROLE sepgsql_gr_target", "CREATE ROLE sepgsql_gr_super SUPERUSER", "CREATE ROLE sepgsql_gr_bridge", "CREATE ROLE sepgsql_gr_indirect CREATEROLE",
			"GRANT sepgsql_gr_target TO sepgsql_gr_adminonly WITH ADMIN OPTION", "GRANT sepgsql_gr_target TO sepgsql_gr_member",
			"GRANT sepgsql_gr_target TO sepgsql_gr_bridge WITH ADMIN OPTION", "GRANT sepgsql_gr_bridge TO sepgsql_gr_indirect",
			"CREATE TABLE sepgsql_gr_data(v integer)", "GRANT SELECT ON sepgsql_gr_data TO PUBLIC",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO sepgsql_gr_data VALUES(41)", 1))
		identities := make(map[string]int64, len(roles))
		for _, name := range roles {
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname='"+name+"'").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("role identity", name, oid, err)
			}
			identities[name] = oid
		}
		creator, targetSuper, creatorMember, creatorAdmin := true, false, false, false
		unchanged := func() {
			t.Helper()
			roleRows := make([][]any, 0, len(roles))
			for _, name := range []string{"sepgsql_gr_adminonly", "sepgsql_gr_bridge", "sepgsql_gr_creator", "sepgsql_gr_indirect", "sepgsql_gr_member", "sepgsql_gr_plain", "sepgsql_gr_super", "sepgsql_gr_target"} {
				create := name == "sepgsql_gr_creator" && creator || name == "sepgsql_gr_member" || name == "sepgsql_gr_indirect"
				super := name == "sepgsql_gr_super" || name == "sepgsql_gr_target" && targetSuper
				roleRows = append(roleRows, []any{identities[name], name, create, super, false})
			}
			both(fixture.Select("SELECT oid::bigint,rolname::text,rolcreaterole,rolsuper,rolcanlogin FROM pg_roles WHERE rolname IN ('sepgsql_gr_adminonly','sepgsql_gr_bridge','sepgsql_gr_creator','sepgsql_gr_indirect','sepgsql_gr_member','sepgsql_gr_plain','sepgsql_gr_super','sepgsql_gr_target') ORDER BY rolname",
				[]string{"oid", "rolname", "rolcreaterole", "rolsuper", "rolcanlogin"}, []string{"INT8", "TEXT", "BOOL", "BOOL", "BOOL"}, roleRows))
			membershipRows := [][]any{
				{"sepgsql_gr_bridge", "sepgsql_gr_indirect", false},
				{"sepgsql_gr_target", "sepgsql_gr_adminonly", true},
				{"sepgsql_gr_target", "sepgsql_gr_bridge", true},
			}
			if creatorMember {
				membershipRows = append(membershipRows, []any{"sepgsql_gr_target", "sepgsql_gr_creator", creatorAdmin})
			}
			membershipRows = append(membershipRows, []any{"sepgsql_gr_target", "sepgsql_gr_member", false})
			both(fixture.Select("SELECT r.rolname::text AS role_name,u.rolname::text AS member_name,m.admin_option FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.roleid JOIN pg_roles u ON u.oid=m.member WHERE r.rolname IN ('sepgsql_gr_bridge','sepgsql_gr_target') AND u.rolname IN ('sepgsql_gr_adminonly','sepgsql_gr_bridge','sepgsql_gr_creator','sepgsql_gr_indirect','sepgsql_gr_member','sepgsql_gr_plain','sepgsql_gr_super','sepgsql_gr_target') ORDER BY role_name,member_name",
				[]string{"role_name", "member_name", "admin_option"}, []string{"TEXT", "TEXT", "BOOL"}, membershipRows))
			query := "SELECT (SELECT count(*) FROM pg_seclabel WHERE classoid='pg_authid'::regclass) AS local_labels,(SELECT count(*) FROM pg_shseclabel WHERE classoid='pg_authid'::regclass) AS shared_labels,(SELECT count(*) FROM pg_seclabels WHERE objtype='role') AS view_labels,(SELECT v FROM sepgsql_gr_data ORDER BY v LIMIT 1) AS value,(SELECT array_agg(v ORDER BY v)::text FROM sepgsql_gr_data) AS data_values"
			columns := []string{"local_labels", "shared_labels", "view_labels", "value", "data_values"}
			types := []string{"INT8", "INT8", "INT8", "INT4", "TEXT"}
			values := []any{int64(0), int64(0), int64(0), int64(41), "{41}"}
			if active {
				query += ",sepgsql_getcon() AS context,(SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_gr_data'::regclass AND label='" + sepgsqlP + "' AND provider='selinux') AS data_labels"
				columns = append(columns, "context", "data_labels")
				types = append(types, "TEXT", "INT8")
				values = append(values, sepgsqlA, int64(8))
			}
			both(fixture.Select(query, columns, types, [][]any{values}))
		}
		failure := func(q, code, message, detail string) {
			t.Helper()
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail})
			s.ErrorStage = "execute"
			identity := sepgsqlIntegrityIdentity(t, p, c)
			run(s)
			unchanged()
			both(identity)
			run(fixture.Prepared(s))
			unchanged()
			both(identity)
		}
		const unsupported = "SELinux: security labels are not supported for this type of object"
		const createDetail = "The current user must have the CREATEROLE attribute."
		const superDetail = "The current user must have the SUPERUSER attribute."
		adminDetail := func(name string) string { return `The current user must have the ADMIN option on role "` + name + `".` }
		cases := []struct{ actor, target, code, message, detail string }{
			{"sepgsql_gr_plain", "sepgsql_gr_plain", "42501", "permission denied", createDetail},
			{"sepgsql_gr_plain", "sepgsql_gr_target", "42501", "permission denied", createDetail},
			{"sepgsql_gr_creator", "sepgsql_gr_creator", "42501", "permission denied", adminDetail("sepgsql_gr_creator")},
			{"sepgsql_gr_creator", "sepgsql_gr_target", "42501", "permission denied", adminDetail("sepgsql_gr_target")},
			{"sepgsql_gr_adminonly", "sepgsql_gr_target", "42501", "permission denied", createDetail},
			{"sepgsql_gr_member", "sepgsql_gr_target", "42501", "permission denied", adminDetail("sepgsql_gr_target")},
			{"sepgsql_gr_indirect", "sepgsql_gr_target", "0A000", unsupported, ""},
			{"sepgsql_gr_plain", "sepgsql_gr_super", "42501", "permission denied", superDetail},
			{"sepgsql_gr_creator", "sepgsql_gr_super", "42501", "permission denied", superDetail},
			{"", "sepgsql_gr_super", "0A000", unsupported, ""}, {"", "sepgsql_gr_target", "0A000", unsupported, ""},
			{"sepgsql_gr_plain", "sepgsql_gr_absent", "42704", `role "sepgsql_gr_absent" does not exist`, ""},
		}
		var selected []struct{ actor, target, code, message, detail string }
		switch section {
		case "ownership":
			selected = cases[:6]
		case "boundaries":
			selected = cases[6:]
		case "held":
		default:
			t.Fatal("unknown role evidence section", section)
		}
		for _, tc := range selected {
			if tc.actor != "" {
				run(fixture.Exec("SET ROLE "+tc.actor, 0))
			}
			for _, label := range []string{"NULL", "''", "'invalid'", "'" + sepgsqlP + "'"} {
				for _, provider := range []string{"FOR selinux ", "", "FOR absent_provider "} {
					code, message, detail := tc.code, tc.message, tc.detail
					if provider == "FOR absent_provider " {
						code, message, detail = "22023", `security label provider "absent_provider" is not loaded`, ""
					} else if !active {
						code, message, detail = "22023", `security label provider "selinux" is not loaded`, ""
						if provider == "" {
							message = "no security label providers have been loaded"
						}
					}
					failure("SECURITY LABEL "+provider+"ON ROLE "+tc.target+" IS "+label, code, message, detail)
				}
			}
			if !active && (tc.actor == "sepgsql_gr_plain" || tc.actor == "sepgsql_gr_creator") && tc.target != tc.actor && tc.target != "sepgsql_gr_absent" {
				detail := `Only roles with the CREATEROLE attribute and the ADMIN option on role "` + tc.target + `" may rename this role.`
				if tc.target == "sepgsql_gr_super" {
					detail = "Only roles with the SUPERUSER attribute may rename roles with the SUPERUSER attribute."
				}
				failure("ALTER ROLE "+tc.target+" RENAME TO sepgsql_gr_unused", "42501", "permission denied to rename role", detail)
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		if section != "held" {
			return
		}
		// Prepare as administrator, then change actual actor and mutate the role
		// store from a separate administrative connection while that actor stays.
		q := "SECURITY LABEL FOR selinux ON ROLE sepgsql_gr_target IS 'invalid'"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		root, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer root.Close()
		rootRun := func(q string) { fixture.RunStep(t, p.Ctx, root, b.Recorder, fixture.Exec(q, 0)) }
		heldFailure := func(code, message, detail string) {
			t.Helper()
			if !active {
				code, message, detail = "22023", `security label provider "selinux" is not loaded`, ""
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail})
			s.ErrorStage = "execute"
			identity := sepgsqlIntegrityIdentity(t, p, c)
			run(s)
			unchanged()
			both(identity)
			run(fixture.Prepared(s))
			unchanged()
			both(identity)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			unchanged()
			both(identity)
		}
		run(fixture.Exec("SET ROLE sepgsql_gr_plain", 0))
		heldFailure("42501", "permission denied", createDetail)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE sepgsql_gr_creator", 0))
		heldFailure("42501", "permission denied", adminDetail("sepgsql_gr_target"))
		rootRun("GRANT sepgsql_gr_target TO sepgsql_gr_creator WITH ADMIN OPTION")
		creatorMember, creatorAdmin = true, true
		heldFailure("0A000", unsupported, "")
		rootRun("ALTER ROLE sepgsql_gr_creator NOCREATEROLE")
		creator = false
		heldFailure("42501", "permission denied", createDetail)
		rootRun("ALTER ROLE sepgsql_gr_creator CREATEROLE")
		creator = true
		rootRun("REVOKE ADMIN OPTION FOR sepgsql_gr_target FROM sepgsql_gr_creator")
		creatorAdmin = false
		heldFailure("42501", "permission denied", adminDetail("sepgsql_gr_target"))
		rootRun("GRANT sepgsql_gr_target TO sepgsql_gr_creator WITH ADMIN OPTION")
		creatorAdmin = true
		heldFailure("0A000", unsupported, "")
		rootRun("ALTER ROLE sepgsql_gr_target SUPERUSER")
		targetSuper = true
		heldFailure("42501", "permission denied", superDetail)
		rootRun("ALTER ROLE sepgsql_gr_target NOSUPERUSER")
		targetSuper = false
		heldFailure("0A000", unsupported, "")
		if !active {
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("SAVEPOINT role_rename", 0))
			run(fixture.Exec("ALTER ROLE sepgsql_gr_target RENAME TO sepgsql_gr_unused", 0))
			both(sepgsqlCount(fmt.Sprintf("SELECT count(*) FROM pg_roles WHERE oid=%d::oid AND rolname='sepgsql_gr_unused'", identities["sepgsql_gr_target"]), 1))
			run(fixture.Exec("ROLLBACK TO role_rename", 0))
			run(fixture.Exec("ROLLBACK", 0))
			unchanged()
		}
		run(fixture.Exec("RESET ROLE", 0))
	}
	// All matrix entries and the entire held lifecycle remain selected. Separate
	// genuine fixtures keep each bounded scenario within its unchanged deadline.
	for _, section := range []string{"ownership", "boundaries", "held"} {
		t.Run(section, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, roles)
			sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				body(t, p, b, c, run, false, section)
			}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				body(t, p, b, c, run, true, section)
			})
		})
	}
}

func TestReferenceSepgsqlSimulationGenericLabelDomainKind(t *testing.T) {
	body := func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool, section string, group int) {
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		both := func(s fixture.Step) { t.Helper(); run(s); run(fixture.Prepared(s)) }
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE ROLE sepgsql_gd_owner", "CREATE ROLE sepgsql_gd_reader", "GRANT CREATE ON SCHEMA public TO sepgsql_gd_owner", "SET ROLE sepgsql_gd_owner",
			"CREATE TYPE sepgsql_gd_enum AS ENUM ('v')", `CREATE TYPE "MixedType" AS ENUM ('v')`, "CREATE DOMAIN sepgsql_gd_domain AS integer", "CREATE DOMAIN sepgsql_gd_array AS integer[]", "CREATE DOMAIN sepgsql_gd_replaced AS integer", "RESET ROLE",
			"CREATE TABLE sepgsql_gd_data(v integer)", "GRANT SELECT ON sepgsql_gd_data TO PUBLIC"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO sepgsql_gd_data VALUES(41)", 1))
		type identity struct {
			oid              int64
			name, kind, base string
		}
		types := []identity{{name: "sepgsql_gd_enum", kind: "e", base: "0"}, {name: "MixedType", kind: "e", base: "0"}, {name: "sepgsql_gd_domain", kind: "d", base: "23"}, {name: "sepgsql_gd_array", kind: "d", base: "1007"}, {name: "sepgsql_gd_replaced", kind: "d", base: "23"}}
		capture := func(i int) {
			if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_type WHERE typnamespace='public'::regnamespace AND typname='"+types[i].name+"'").Scan(&types[i].oid); err != nil || types[i].oid <= 0 {
				t.Fatal("type identity", types[i], err)
			}
		}
		for i := range types {
			capture(i)
		}
		unchanged := func() {
			t.Helper()
			typeRows := make([][]any, 0, len(types))
			for _, i := range []int{1, 3, 2, 0, 4} {
				typ := types[i]
				typeRows = append(typeRows, []any{typ.oid, typ.name, typ.kind, typ.base, "sepgsql_gd_owner", "public"})
			}
			both(fixture.Select("SELECT oid::bigint,typname::text,typtype::text,typbasetype::text,typowner::regrole::text AS owner,typnamespace::regnamespace::text AS namespace FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('MixedType','sepgsql_gd_array','sepgsql_gd_domain','sepgsql_gd_enum','sepgsql_gd_replaced') ORDER BY typname",
				[]string{"oid", "typname", "typtype", "typbasetype", "owner", "namespace"}, []string{"INT8", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, typeRows))
			query := "SELECT (SELECT count(*) FROM pg_enum WHERE enumtypid='sepgsql_gd_enum'::regtype AND enumlabel='v' AND enumsortorder=1) AS enum_values,(SELECT count(*) FROM pg_seclabel WHERE classoid='pg_type'::regclass) AS local_labels,(SELECT count(*) FROM pg_shseclabel WHERE classoid='pg_type'::regclass) AS shared_labels,(SELECT count(*) FROM pg_seclabels WHERE objtype IN ('type','domain')) AS view_labels,(SELECT v FROM sepgsql_gd_data ORDER BY v LIMIT 1) AS value,(SELECT array_agg(v ORDER BY v)::text FROM sepgsql_gd_data) AS data_values"
			columns := []string{"enum_values", "local_labels", "shared_labels", "view_labels", "value", "data_values"}
			columnTypes := []string{"INT8", "INT8", "INT8", "INT8", "INT4", "TEXT"}
			values := []any{int64(1), int64(0), int64(0), int64(0), int64(41), "{41}"}
			if active {
				query += ",sepgsql_getcon() AS context,(SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_gd_data'::regclass AND label='" + sepgsqlP + "' AND provider='selinux') AS data_labels"
				columns = append(columns, "context", "data_labels")
				columnTypes = append(columnTypes, "TEXT", "INT8")
				values = append(values, sepgsqlA, int64(8))
			}
			both(fixture.Select(query, columns, columnTypes, [][]any{values}))
		}
		failure := func(q, code, message string) {
			t.Helper()
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
			s.ErrorStage = "execute"
			identity := sepgsqlIntegrityIdentity(t, p, c)
			run(s)
			unchanged()
			both(identity)
			run(fixture.Prepared(s))
			unchanged()
			both(identity)
		}
		const unsupported = "SELinux: security labels are not supported for this type of object"
		var actors []string
		switch section {
		case "administrator":
			actors = []string{""}
		case "owner":
			actors = []string{"sepgsql_gd_owner"}
		case "nonowner":
			actors = []string{"sepgsql_gd_reader"}
		case "held":
		default:
			t.Fatal("unknown domain evidence section", section)
		}
		for _, actor := range actors {
			if actor != "" {
				run(fixture.Exec("SET ROLE "+actor, 0))
			}
			spellings := []struct{ syntax, spelling string }{{"sepgsql_gd_enum", "sepgsql_gd_enum"}, {"public.sepgsql_gd_enum", "public.sepgsql_gd_enum"}, {`public."MixedType"`, "public.MixedType"}, {"INTEGER", "pg_catalog.int4"}, {"varchar(7)", "pg_catalog.varchar"}, {"sepgsql_gd_enum[]", "sepgsql_gd_enum[]"}, {"sepgsql_gd_domain[][]", "sepgsql_gd_domain[]"}}
			if group == 0 {
				spellings = spellings[:4]
			} else {
				spellings = spellings[4:]
			}
			for _, tc := range spellings {
				for _, label := range []string{"NULL", "''", "'invalid'", "'" + sepgsqlP + "'"} {
					for _, provider := range []string{"FOR selinux ", "", "FOR absent_provider "} {
						code, message := "42809", `"`+tc.spelling+`" is not a domain`
						if provider == "FOR absent_provider " {
							code, message = "22023", `security label provider "absent_provider" is not loaded`
						} else if !active {
							code, message = "22023", `security label provider "selinux" is not loaded`
							if provider == "" {
								message = "no security label providers have been loaded"
							}
						}
						failure("SECURITY LABEL "+provider+"ON DOMAIN "+tc.syntax+" IS "+label, code, message)
					}
				}
			}
			if group == 0 {
				run(fixture.Exec("RESET ROLE", 0))
				continue
			}
			for _, kindName := range []string{"DOMAIN sepgsql_gd_domain", "DOMAIN sepgsql_gd_array", "TYPE sepgsql_gd_enum", "TYPE sepgsql_gd_domain"} {
				code, message := "0A000", unsupported
				if actor == "sepgsql_gd_reader" {
					code, message = "42501", "must be owner of type "+map[string]string{"DOMAIN sepgsql_gd_domain": "sepgsql_gd_domain", "DOMAIN sepgsql_gd_array": "sepgsql_gd_array", "TYPE sepgsql_gd_enum": "sepgsql_gd_enum", "TYPE sepgsql_gd_domain": "sepgsql_gd_domain"}[kindName]
				}
				if !active {
					code, message = "22023", `security label provider "selinux" is not loaded`
				}
				failure("SECURITY LABEL FOR selinux ON "+kindName+" IS NULL", code, message)
			}
			for _, provider := range []string{"FOR selinux ", "", "FOR absent_provider "} {
				code, message := "42704", `type "sepgsql_gd_absent" does not exist`
				if provider == "FOR absent_provider " {
					code, message = "22023", `security label provider "absent_provider" is not loaded`
				} else if !active {
					code, message = "22023", `security label provider "selinux" is not loaded`
					if provider == "" {
						message = "no security label providers have been loaded"
					}
				}
				failure("SECURITY LABEL "+provider+"ON DOMAIN sepgsql_gd_absent IS NULL", code, message)
			}
			if !active {
				failure("DROP DOMAIN sepgsql_gd_enum", "42809", `"sepgsql_gd_enum" is not a domain`)
				failure("DROP DOMAIN sepgsql_gd_absent", "42704", `type "sepgsql_gd_absent" does not exist`)
				if actor == "sepgsql_gd_reader" {
					failure("DROP DOMAIN sepgsql_gd_domain", "42501", "must be owner of type sepgsql_gd_domain")
				}
				if actor == "sepgsql_gd_owner" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("DROP DOMAIN sepgsql_gd_array", 0))
					run(fixture.Exec("ROLLBACK", 0))
					unchanged()
				}
			}
			run(fixture.Exec("RESET ROLE", 0))
		}
		if section != "held" {
			return
		}
		q := "SECURITY LABEL FOR selinux ON DOMAIN sepgsql_gd_replaced IS 'invalid'"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldFailure := func(code, message string) {
			t.Helper()
			if !active {
				code, message = "22023", `security label provider "selinux" is not loaded`
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
			s.ErrorStage = "execute"
			identity := sepgsqlIntegrityIdentity(t, p, c)
			run(s)
			unchanged()
			both(identity)
			run(fixture.Prepared(s))
			unchanged()
			both(identity)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			unchanged()
			both(identity)
		}
		heldFailure("0A000", unsupported)
		run(fixture.Exec("SET ROLE sepgsql_gd_reader", 0))
		heldFailure("42501", "must be owner of type sepgsql_gd_replaced")
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE sepgsql_gd_owner", 0))
		heldFailure("0A000", unsupported)
		run(fixture.Exec("DROP DOMAIN sepgsql_gd_replaced", 0))
		run(fixture.Exec("CREATE TYPE sepgsql_gd_replaced AS ENUM ('v')", 0))
		types[4].kind, types[4].base = "e", "0"
		old := types[4].oid
		capture(4)
		if old == types[4].oid {
			t.Fatal("recreated type reused dropped identity")
		}
		heldFailure("42809", `"sepgsql_gd_replaced" is not a domain`)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("SET ROLE sepgsql_gd_reader", 0))
		heldFailure("42809", `"sepgsql_gd_replaced" is not a domain`)
		run(fixture.Exec("RESET ROLE", 0))
	}
	// The seven spellings remain selected in two genuine scenarios per actor,
	// alongside every kind/missing contrast. The held lifecycle stays intact.
	for _, section := range []string{"administrator", "owner", "nonowner", "held"} {
		groups := 2
		if section == "held" {
			groups = 1
		}
		for group := range groups {
			t.Run(fmt.Sprintf("%s/%d", section, group), func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"sepgsql_gd_owner", "sepgsql_gd_reader"})
				sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
					body(t, p, b, c, run, false, section, group)
				}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
					body(t, p, b, c, run, true, section, group)
				})
			})
		}
	}
}

func TestReferenceSepgsqlSimulationGenericLabelColumnIntegrity(t *testing.T) {
	body := func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun, active bool, section string) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			if _, err := c.ExecContext(ctx, "RESET ROLE"); err != nil {
				t.Error("column integrity role cleanup", err)
			}
			// PostgreSQL keeps FDWs outside the per-scenario schema.
			if b.PG {
				if _, err := c.ExecContext(ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS sepgsql_gc_fdw CASCADE"); err != nil {
					t.Error("column integrity foreign metadata cleanup", err)
				}
			}
		}()
		both := func(s fixture.Step) { t.Helper(); run(s); run(fixture.Prepared(s)) }
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0))
		}
		for _, q := range []string{"CREATE ROLE sepgsql_gc_owner", "CREATE ROLE sepgsql_gc_reader", "CREATE TABLE sepgsql_gc_data(v integer)", "INSERT INTO sepgsql_gc_data VALUES(41)",
			"CREATE SEQUENCE sepgsql_gc_sequence START 17 INCREMENT 3 CACHE 4", "GRANT SELECT ON sepgsql_gc_sequence,sepgsql_gc_data TO PUBLIC",
			"CREATE INDEX sepgsql_gc_index ON sepgsql_gc_data(v)", "CREATE TABLE sepgsql_gc_partition(v text) PARTITION BY RANGE(v)", "CREATE INDEX sepgsql_gc_partition_index ON sepgsql_gc_partition USING spgist(v text_ops)",
			"CREATE VIEW sepgsql_gc_view AS SELECT v FROM sepgsql_gc_data", "CREATE MATERIALIZED VIEW sepgsql_gc_mat AS SELECT v FROM sepgsql_gc_data", "CREATE TYPE sepgsql_gc_composite AS (v integer)",
			"CREATE FOREIGN DATA WRAPPER sepgsql_gc_fdw NO HANDLER", "CREATE SERVER sepgsql_gc_server FOREIGN DATA WRAPPER sepgsql_gc_fdw", "CREATE FOREIGN TABLE sepgsql_gc_foreign(v integer) SERVER sepgsql_gc_server"} {
			count := int64(0)
			if q == "INSERT INTO sepgsql_gc_data VALUES(41)" || q == "CREATE MATERIALIZED VIEW sepgsql_gc_mat AS SELECT v FROM sepgsql_gc_data" {
				count = 1
			}
			run(fixture.Exec(q, count))
		}
		relations := make(map[string]int64, 3)
		for _, name := range []string{"sepgsql_gc_index", "sepgsql_gc_partition_index", "sepgsql_gc_sequence"} {
			var oid int64
			if err := c.QueryRowContext(p.Ctx, "SELECT '"+name+"'::regclass::oid::bigint").Scan(&oid); err != nil || oid <= 0 {
				t.Fatal("relation identity", name, oid, err)
			}
			relations[name] = oid
		}
		owner := ""
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user::text").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		initialOwner := owner
		unchanged := func() {
			t.Helper()
			both(fixture.Select("SELECT oid::bigint,relname::text,relkind::text,relowner::regrole::text AS owner,relnamespace::regnamespace::text AS namespace FROM pg_class WHERE relnamespace='public'::regnamespace AND relname IN ('sepgsql_gc_index','sepgsql_gc_partition_index','sepgsql_gc_sequence') ORDER BY relname",
				[]string{"oid", "relname", "relkind", "owner", "namespace"}, []string{"INT8", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{
					{relations["sepgsql_gc_index"], "sepgsql_gc_index", "i", initialOwner, "public"},
					{relations["sepgsql_gc_partition_index"], "sepgsql_gc_partition_index", "I", initialOwner, "public"},
					{relations["sepgsql_gc_sequence"], "sepgsql_gc_sequence", "S", owner, "public"},
				}))
			both(fixture.Select("SELECT s.seqrelid::bigint,s.seqtypid::bigint,s.seqstart,s.seqincrement,s.seqcache,s.seqmin,s.seqmax,s.seqcycle,g.last_value,g.log_cnt,g.is_called FROM pg_sequence s CROSS JOIN sepgsql_gc_sequence g WHERE s.seqrelid='sepgsql_gc_sequence'::regclass",
				[]string{"seqrelid", "seqtypid", "seqstart", "seqincrement", "seqcache", "seqmin", "seqmax", "seqcycle", "last_value", "log_cnt", "is_called"},
				[]string{"INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "BOOL", "INT8", "INT8", "BOOL"},
				[][]any{{relations["sepgsql_gc_sequence"], int64(20), int64(17), int64(3), int64(4), int64(1), int64(9223372036854775807), false, int64(17), int64(0), false}}))
			query := "SELECT (SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_gc_sequence'::regclass AND objsubid<>0) AS local_column_labels,(SELECT count(*) FROM pg_shseclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_gc_sequence'::regclass) AS shared_labels,(SELECT count(*) FROM pg_seclabels WHERE objtype='column' AND objoid='sepgsql_gc_sequence'::regclass) AS view_column_labels,(SELECT v FROM sepgsql_gc_data ORDER BY v LIMIT 1) AS value,(SELECT array_agg(v ORDER BY v)::text FROM sepgsql_gc_data) AS data_values"
			columns := []string{"local_column_labels", "shared_labels", "view_column_labels", "value", "data_values"}
			columnTypes := []string{"INT8", "INT8", "INT8", "INT4", "TEXT"}
			values := []any{int64(0), int64(0), int64(0), int64(41), "{41}"}
			if active {
				query += ",sepgsql_getcon() AS context,(SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_gc_data'::regclass AND provider='selinux' AND label='" + sepgsqlP + "') AS data_labels,(SELECT count(*) FROM pg_seclabel WHERE classoid='pg_class'::regclass AND objoid='sepgsql_gc_sequence'::regclass AND objsubid=0 AND label='" + sepgsqlP + "') AS sequence_labels"
				columns = append(columns, "context", "data_labels", "sequence_labels")
				columnTypes = append(columnTypes, "TEXT", "INT8", "INT8")
				values = append(values, sepgsqlA, int64(8), int64(1))
			}
			both(fixture.Select(query, columns, columnTypes, [][]any{values}))
		}
		failure := func(q, code, message, detail string) {
			t.Helper()
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail})
			s.ErrorStage = "execute"
			identity := sepgsqlIntegrityIdentity(t, p, c)
			run(s)
			unchanged()
			both(identity)
			run(fixture.Prepared(s))
			unchanged()
			both(identity)
		}
		const message = `cannot set security label on relation "sepgsql_gc_sequence"`
		const detail = "This operation is not supported for sequences."
		var actors []string
		switch section {
		case "administrator":
			actors = []string{""}
		case "owner":
			actors = []string{"sepgsql_gc_owner"}
		case "nonowner":
			actors = []string{"sepgsql_gc_reader"}
		case "kinds", "held":
		default:
			t.Fatal("unknown column evidence section", section)
		}
		for _, actor := range actors {
			if actor == "sepgsql_gc_owner" {
				run(fixture.Exec("ALTER SEQUENCE sepgsql_gc_sequence OWNER TO sepgsql_gc_owner", 0))
				owner = "sepgsql_gc_owner"
			}
			if actor != "" {
				run(fixture.Exec("SET ROLE "+actor, 0))
			}
			for _, column := range []string{"last_value", "log_cnt", "is_called", "absent"} {
				for _, label := range []string{"NULL", "''", "'invalid'", "'" + sepgsqlP + "'"} {
					for _, provider := range []string{"FOR selinux ", "", "FOR absent_provider "} {
						code, msg, det := "42809", message, detail
						if actor == "sepgsql_gc_reader" {
							code, msg, det = "42501", "must be owner of sequence sepgsql_gc_sequence", ""
						}
						if column == "absent" {
							code, msg, det = "42703", `column "absent" of relation "sepgsql_gc_sequence" does not exist`, ""
						}
						if provider == "FOR absent_provider " {
							code, msg, det = "22023", `security label provider "absent_provider" is not loaded`, ""
						} else if !active {
							code, msg, det = "22023", `security label provider "selinux" is not loaded`, ""
							if provider == "" {
								msg = "no security label providers have been loaded"
							}
						}
						failure("SECURITY LABEL "+provider+"ON COLUMN sepgsql_gc_sequence."+column+" IS "+label, code, msg, det)
					}
				}
			}
			if !active {
				if actor != "sepgsql_gc_reader" {
					failure("COMMENT ON COLUMN sepgsql_gc_sequence.last_value IS NULL", "42809", `cannot set comment on relation "sepgsql_gc_sequence"`, detail)
				} else {
					failure("ALTER SEQUENCE sepgsql_gc_sequence INCREMENT BY 2", "42501", "must be owner of sequence sepgsql_gc_sequence", "")
				}
			}
			run(fixture.Exec("RESET ROLE", 0))
			if actor == "sepgsql_gc_owner" {
				run(fixture.Exec("ALTER SEQUENCE sepgsql_gc_sequence OWNER TO "+initialOwner, 0))
				owner = initialOwner
			}
		}
		if section != "kinds" && section != "held" {
			return
		}
		if section == "kinds" {
			for _, name := range []string{"sepgsql_gc_index", "sepgsql_gc_partition_index", "sepgsql_gc_view", "sepgsql_gc_mat", "sepgsql_gc_composite", "sepgsql_gc_foreign"} {
				code, msg, det := "0A000", "SELinux: security labels are not supported for this type of object", ""
				if name == "sepgsql_gc_index" || name == "sepgsql_gc_partition_index" {
					code, msg, det = "42809", `cannot set security label on relation "`+name+`"`, "This operation is not supported for indexes."
					if name == "sepgsql_gc_partition_index" {
						det = "This operation is not supported for partitioned indexes."
					}
				}
				if !active {
					code, msg, det = "22023", `security label provider "selinux" is not loaded`, ""
				}
				failure("SECURITY LABEL FOR selinux ON COLUMN "+name+".v IS 'invalid'", code, msg, det)
			}
			if active {
				for _, name := range []string{"sepgsql_gc_data", "sepgsql_gc_partition"} {
					both(fixture.Exec("SECURITY LABEL FOR selinux ON COLUMN "+name+".v IS '"+sepgsqlP+"'", 0))
				}
				unchanged()
			}
			return
		}
		q := "SECURITY LABEL FOR selinux ON COLUMN sepgsql_gc_sequence.last_value IS 'invalid'"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		heldFailure := func(code, msg, det string) {
			t.Helper()
			if !active {
				code, msg, det = "22023", `security label provider "selinux" is not loaded`, ""
			}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: msg, Detail: det})
			s.ErrorStage = "execute"
			identity := sepgsqlIntegrityIdentity(t, p, c)
			run(s)
			unchanged()
			both(identity)
			run(fixture.Prepared(s))
			unchanged()
			both(identity)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: q}, b.Recorder, s)
			unchanged()
			both(identity)
		}
		run(fixture.Exec("SET ROLE sepgsql_gc_owner", 0))
		heldFailure("42501", "must be owner of sequence sepgsql_gc_sequence", "")
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER SEQUENCE sepgsql_gc_sequence OWNER TO sepgsql_gc_owner", 0))
		owner = "sepgsql_gc_owner"
		run(fixture.Exec("SET ROLE sepgsql_gc_owner", 0))
		heldFailure("42809", message, detail)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("ALTER SEQUENCE sepgsql_gc_sequence OWNER TO sepgsql_gc_reader", 0))
		owner = "sepgsql_gc_reader"
		run(fixture.Exec("SET ROLE sepgsql_gc_owner", 0))
		heldFailure("42501", "must be owner of sequence sepgsql_gc_sequence", "")
		run(fixture.Exec("RESET ROLE", 0))
	}
	// Actor matrices, kind contrasts and the intact held-owner chain each use
	// genuine fixtures and ordinary cleanup, with the original scenario deadline.
	for _, section := range []string{"administrator", "owner", "nonowner", "kinds", "held"} {
		t.Run(section, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"sepgsql_gc_owner", "sepgsql_gc_reader"})
			sepgsqlCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				body(t, p, b, c, run, false, section)
			}, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run sepgsqlRun) {
				body(t, p, b, c, run, true, section)
			})
		})
	}
}

// Snapshot real physical-session principal identities before each rejection;
// allocator-dependent OIDs are not pinned to PostgreSQL's allocation sequence.
func sepgsqlIntegrityIdentity(t *testing.T, p *fixture.Pair, c *sql.Conn) fixture.Step {
	t.Helper()
	const query = "SELECT current_user::text AS actor,session_user::text AS login,current_user::regrole::oid::bigint AS actor_oid,session_user::regrole::oid::bigint AS login_oid"
	var actor, login string
	var actorOID, loginOID int64
	if err := c.QueryRowContext(p.Ctx, query).Scan(&actor, &login, &actorOID, &loginOID); err != nil || actorOID <= 0 || loginOID <= 0 {
		t.Fatal("actual session principal identity", actor, login, actorOID, loginOID, err)
	}
	return fixture.Select(query, []string{"actor", "login", "actor_oid", "login_oid"}, []string{"TEXT", "TEXT", "INT8", "INT8"}, [][]any{{actor, login, actorOID, loginOID}})
}
