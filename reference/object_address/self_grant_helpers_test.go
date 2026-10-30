package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

type selfGrantCase struct {
	t *testing.T
	p *fixture.Pair
	backend fixture.Backend
	admin, actor, peer *sql.Conn
	prepared bool
	prefix, creator string
}

func selfGrantCases(t *testing.T, prefix string, suffixes []string, body func(*selfGrantCase)) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			names := make([]string, 0, len(suffixes)+1)
			for _, suffix := range suffixes {
				names = append(names, prefix + "_" + suffix)
			}
			// Initial clauses can attribute grants between other roles to the
			// creator. Retire those endpoints before their retained grantor.
			names = append(names, prefix + "_actor")
			fixture.RoleReferenceCleanup(t, names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					admin, err := backend.DB.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer admin.Close()
					s := &selfGrantCase{t: t, p: p, backend: backend, admin: admin, prepared: prepared, prefix: prefix, creator: prefix + "_actor"}
					s.run(admin, fixture.Exec("CREATE USER " + s.creator + " CREATEROLE PASSWORD 'role-attributes-fixture'", 0))
					pool := fixture.RoleAttributesPool(t, p, backend, s.creator)
					actor, err := pool.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer actor.Close()
					peer, err := pool.Conn(p.Ctx)
					if err != nil { t.Fatal(err) }
					defer peer.Close()
					s.actor, s.peer = actor, peer
					body(s)
				})
			}
		})
	}
}

func (s *selfGrantCase) run(c fixture.Executor, step fixture.Step) {
	s.t.Helper()
	fixture.RoleAttributesRun(s.t, s.p, s.backend, c, s.prepared, step)
}
func selfGrantLiteral(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
func (s *selfGrantCase) exec(c fixture.Executor, query string) { s.t.Helper(); s.run(c, fixture.Exec(query, 0)) }
func (s *selfGrantCase) set(c fixture.Executor, text string) {
	s.t.Helper()
	s.exec(c, "SET createrole_self_grant = " + selfGrantLiteral(text))
}
func (s *selfGrantCase) create(suffix string) string {
	s.t.Helper()
	name := s.prefix + "_" + suffix
	s.exec(s.actor, "CREATE ROLE " + name)
	return name
}
func (s *selfGrantCase) reads(c fixture.Executor, text, source string) {
	s.t.Helper()
	for _, tc := range []struct{ query, column string }{
		{"SHOW createrole_self_grant", "createrole_self_grant"},
		{"SELECT current_setting('createrole_self_grant')", "current_setting"},
	} {
		step := fixture.Select(tc.query, []string{tc.column}, []string{"TEXT"}, [][]any{{text}})
		step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			types, err := rows.ColumnTypes()
			if err != nil || len(types) != 1 {
				t.Fatal("setting metadata", types, err)
			}
			nullable, known := types[0].Nullable()
			wantNullable := tc.column == "current_setting"
			if types[0].ScanType() != reflect.TypeFor[string]() || known == s.backend.PG || (s.backend.PG && nullable) || (!s.backend.PG && nullable != wantNullable) {
				t.Fatal("setting nullability/type", types[0], nullable, known)
			}
		}
		s.run(c, step)
	}
	columns := []string{"name", "setting", "unit", "category", "short_desc", "extra_desc", "context", "vartype", "source", "min_val", "max_val", "enumvals", "boot_val", "reset_val", "sourcefile", "sourceline", "pending_restart"}
	types := []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "_TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL"}
	row := []any{"createrole_self_grant", text, nil, "Client Connection Defaults / Statement Behavior", "Sets whether a CREATEROLE user automatically grants the role to themselves, and with which options.", "An empty string disables automatic self grants.", "user", "string", source, nil, nil, nil, "", "", nil, nil, false}
	s.run(c, fixture.Select("SELECT " + strings.Join(columns, ",") + " FROM pg_settings WHERE name='createrole_self_grant'", columns, types, [][]any{row}))
}

func (s *selfGrantCase) absent(c fixture.Executor, name string) {
	s.t.Helper()
	s.run(c, fixture.Select("SELECT rolname FROM pg_roles WHERE rolname=" + selfGrantLiteral(name), []string{"rolname"}, []string{"NAME"}, nil))
	s.run(c, fixture.Select("SELECT a.admin_option FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid WHERE r.rolname=" + selfGrantLiteral(name), []string{"admin_option"}, []string{"BOOL"}, nil))
}

func (s *selfGrantCase) edges(c fixture.Executor, name string, inherit, set, optional bool) {
	s.t.Helper()
	query := "SELECT a.admin_option,a.inherit_option,a.set_option,a.grantor=10 AS bootstrap,a.grantor=m.oid AS creator FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member WHERE r.rolname=" + selfGrantLiteral(name) + " AND m.rolname=" + selfGrantLiteral(s.creator)
	want := [][]any{{true, false, false, true, false}}
	if optional { want = append(want, []any{false, inherit, set, false, true}) }
	step := fixture.Select(query, []string{"admin_option", "inherit_option", "set_option", "bootstrap", "creator"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, want)
	step.Bag = true
	s.run(c, step)
	// Generated identities are relational, never compared between allocators.
	rows, err := c.QueryContext(s.p.Ctx, "SELECT a.oid,a.roleid,a.member,a.grantor FROM pg_auth_members a JOIN pg_roles r ON r.oid=a.roleid JOIN pg_roles m ON m.oid=a.member WHERE r.rolname=" + selfGrantLiteral(name) + " AND m.rolname=" + selfGrantLiteral(s.creator))
	if err != nil { s.t.Fatal(err) }
	defer rows.Close()
	seen := map[int64]bool{}
	var role, member int64
	for rows.Next() {
		var oid, r, m, g int64
		if err := rows.Scan(&oid, &r, &m, &g); err != nil { s.t.Fatal(err) }
		if oid <= 0 || r <= 0 || m <= 0 || r == m || (g != 10 && g != m) || seen[oid] || (role != 0 && (role != r || member != m)) { s.t.Fatal("invalid edge identities", oid, r, m, g) }
		seen[oid], role, member = true, r, m
	}
	if err := rows.Err(); err != nil { s.t.Fatal(err) }
	if len(seen) != len(want) { s.t.Fatal("edge count", len(seen), len(want)) }
}

func selfGrantDenied(query, detail string) fixture.Step {
	return fixture.RoleReferenceError(query, "42501", "permission denied to create role", detail, false, "execute")
}
func selfGrantInvalid(text, detail string) fixture.Step {
	return fixture.RoleReferenceError("SET createrole_self_grant=" + selfGrantLiteral(text), "22023", `invalid value for parameter "createrole_self_grant": "` + text + `"`, detail, false, "execute")
}
