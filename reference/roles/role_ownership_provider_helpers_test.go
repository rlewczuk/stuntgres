package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/lib/pq"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func ownershipProviderCases(t *testing.T, body func(*fixture.RoleMembershipScenario, string)) {
	ownershipProviderCasesFor(t, []bool{false, true}, body)
}

func ownershipProviderCasesFor(t *testing.T, modes []bool, body func(*fixture.RoleMembershipScenario, string)) {
	ownershipCasesFor(t, modes, func(s *fixture.RoleMembershipScenario) {
		database := ownershipDatabaseName(s)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO " + pq.QuoteIdentifier(s.Bootstrap), "REVOKE CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " FROM PUBLIC"} {
				if _, err := s.C.ExecContext(ctx, q); err != nil {
					s.T.Error(err)
				}
			}
			// Extension members owned by one role drop with their extension,
			// owned by another, only in one DROP OWNED of both roles.
			if roles := ownershipExistingRoles(ctx, s); len(roles) > 0 {
				if _, err := s.C.ExecContext(ctx, "DROP OWNED BY "+strings.Join(roles, ",")+" CASCADE"); err != nil {
					s.T.Error(err)
				}
			}
		}()
		body(s, database)
	})
}
func ownershipDatabaseName(s *fixture.RoleMembershipScenario) string {
	s.T.Helper()
	rows, err := s.C.QueryContext(s.P.Ctx, "SELECT current_database()")
	if err != nil {
		s.T.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "NAME" {
		s.T.Fatal("attached database NAME metadata", types, err)
	}
	var name string
	if !rows.Next() {
		s.T.Fatal("missing attached database", rows.Err())
	}
	if err = rows.Scan(&name); err != nil || name == "" {
		s.T.Fatal(name, err)
	}
	if rows.Next() || rows.Err() != nil {
		s.T.Fatal("attached database cardinality", rows.Err())
	}
	return name
}
func ownershipDatabaseDescription(s *fixture.RoleMembershipScenario, name string) string {
	if s.Backend.PG {
		return name
	} // pinned fixture's independently named database
	return pq.QuoteIdentifier(name) // opaque native attachment contains path punctuation
}
func ownershipDatabaseOwner(s *fixture.RoleMembershipScenario, owner string) {
	s.T.Helper()
	if s.Backend.PG {
		ownershipSelect(s, "SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=current_database()", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}})
	}
	ownershipInherited(s, owner, "pg_database_owner", "USAGE", true)
	s.Actor(owner)
	s.Exec("SET ROLE pg_database_owner")
	ownershipSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"pg_database_owner"}})
	s.Exec("RESET ROLE")
	s.Actor("")
}
func ownershipProviderOwners(s *fixture.RoleMembershipScenario, extension, member string) {
	s.T.Helper()
	ownershipSelect(s, "SELECT r.rolname FROM pg_extension e JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='unaccent'", []string{"rolname"}, []string{"NAME"}, [][]any{{extension}})
	ownershipSelect(s, "SELECT r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner WHERE p.proname='unaccent' OR p.proname='unaccent_init' OR p.proname='unaccent_lexize' ORDER BY p.oid", []string{"rolname"}, []string{"NAME"}, [][]any{{member}, {member}, {member}, {member}})
	ownershipSelect(s, "SELECT r.rolname FROM pg_ts_dict d JOIN pg_roles r ON r.oid=d.dictowner WHERE d.dictname='unaccent'", []string{"rolname"}, []string{"NAME"}, [][]any{{member}})
	if s.Backend.PG {
		ownershipSelect(s, "SELECT proname,proargtypes::TEXT AS args FROM pg_proc WHERE proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize' ORDER BY proname,args", []string{"proname", "args"}, []string{"NAME", "TEXT"}, [][]any{{"unaccent", "25"}, {"unaccent", "3769 25"}, {"unaccent_init", "2281"}, {"unaccent_lexize", "2281 2281 2281 2281"}})
	}
	ownershipSelect(s, "SELECT dictname FROM pg_ts_dict d JOIN pg_ts_template t ON t.oid=d.dicttemplate WHERE d.dictname='unaccent' AND t.tmplname='unaccent'", []string{"dictname"}, []string{"NAME"}, [][]any{{"unaccent"}})
}
func ownershipProviderIDs(s *fixture.RoleMembershipScenario) map[string][]int64 {
	s.T.Helper()
	out := map[string][]int64{}
	for _, entry := range []struct{ key, query string }{{"extension", "SELECT oid::BIGINT FROM pg_extension WHERE extname='unaccent' ORDER BY oid"}, {"procedure", "SELECT oid::BIGINT FROM pg_proc WHERE proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize' ORDER BY oid"}, {"template", "SELECT oid::BIGINT FROM pg_ts_template WHERE tmplname='unaccent' ORDER BY oid"}, {"dictionary", "SELECT oid::BIGINT FROM pg_ts_dict WHERE dictname='unaccent' ORDER BY oid"}} {
		rows, err := s.C.QueryContext(s.P.Ctx, entry.query)
		if err != nil {
			s.T.Fatal(err)
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil || id <= 0 {
				rows.Close()
				s.T.Fatal("provider identity", id, err)
			}
			out[entry.key] = append(out[entry.key], id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			s.T.Fatal(err)
		}
	}
	if len(out["extension"]) != 1 || len(out["procedure"]) != 4 || len(out["template"]) != 1 || len(out["dictionary"]) != 1 {
		s.T.Fatal("exact seven provider identities", out)
	}
	ownershipCount(s, fmt.Sprintf("SELECT count(*) FROM pg_depend WHERE refclassid=3079 AND refobjid=%d AND deptype='e'", out["extension"][0]), 6)
	return out
}
func ownershipProviderStable(s *fixture.RoleMembershipScenario, old map[string][]int64) {
	s.T.Helper()
	if next := ownershipProviderIDs(s); !reflect.DeepEqual(next, old) {
		s.T.Fatal("provider identities reminted", old, next)
	}
}
func ownershipProviderGone(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	for _, q := range []string{"SELECT count(*) FROM pg_extension WHERE extname='unaccent'", "SELECT count(*) FROM pg_proc WHERE proname='unaccent' OR proname='unaccent_init' OR proname='unaccent_lexize'", "SELECT count(*) FROM pg_ts_template WHERE tmplname='unaccent'", "SELECT count(*) FROM pg_ts_dict WHERE dictname='unaccent'"} {
		ownershipCount(s, q, 0)
	}
}
func ownershipHotel(s *fixture.RoleMembershipScenario, q string) {
	ownershipSelect(s, q, []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}})
}
func ownershipProviderSetup(s *fixture.RoleMembershipScenario, database string, super bool) {
	s.Exec("CREATE SCHEMA own_provider AUTHORIZATION own_other")
	s.Exec("GRANT own_other TO own_source WITH INHERIT TRUE, SET FALSE")
	if super {
		s.Exec("ALTER ROLE own_source SUPERUSER")
	} else {
		s.Exec("GRANT CREATE ON DATABASE " + pq.QuoteIdentifier(database) + " TO own_source")
	}
	s.Actor("own_source")
	s.Exec("CREATE EXTENSION unaccent SCHEMA own_provider")
	s.Actor("")
	if super {
		s.Exec("ALTER ROLE own_source NOSUPERUSER")
	}
}

type ownershipProviderMember struct {
	kind, name, signature, sqlIdentity string
}

var ownershipProviderMembers = []ownershipProviderMember{
	{"dictionary", "unaccent", "", "unaccent"},
	{"procedure", "unaccent", "25", "unaccent(text)"},
	{"procedure", "unaccent", "3769,25", "unaccent(regdictionary,text)"},
	{"procedure", "unaccent_init", "2281", "unaccent_init(internal)"},
	{"procedure", "unaccent_lexize", "2281,2281,2281,2281", "unaccent_lexize(internal,internal,internal,internal)"},
}

func ownershipProviderMemberOwners(s *fixture.RoleMembershipScenario, owner string, selected ...ownershipProviderMember) {
	s.T.Helper()
	s.Actor("")
	if s.Backend.PG {
		members := selected
		if len(members) == 0 {
			members = ownershipProviderMembers
		}
		for _, member := range members {
			kind := "FUNCTION"
			if member.kind == "dictionary" {
				kind = "TEXT SEARCH DICTIONARY"
			}
			s.Exec("ALTER " + kind + " own_provider." + member.sqlIdentity + " OWNER TO " + pq.QuoteIdentifier(owner))
		}
		return
	}
	// Native has no direct member ALTER grammar. Its existing public named-owner
	// Restore codec sets real independently owned members, not invented providers.
	var dump bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
		s.T.Fatal(err)
	}
	script := ownershipProviderOwnerScript(s.T, dump.String(), owner, selected...)
	if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(script)); err != nil {
		s.T.Fatal(err)
	}
}
func ownershipProviderOwnerScript(t *testing.T, script, memberOwner string, selected ...ownershipProviderMember) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v2 "
	start := strings.Index(script, marker)
	if start < 0 {
		t.Fatal("missing real member owner metadata")
	}
	endOffset := strings.IndexByte(script[start:], '\n')
	if endOffset < 0 {
		t.Fatal("unterminated owner metadata")
	}
	end := start + endOffset
	data, err := base64.StdEncoding.DecodeString(script[start+len(marker) : end])
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	var members []map[string]json.RawMessage
	if err := json.Unmarshal(record["members"], &members); err != nil || len(members) != 5 {
		t.Fatal("exact five owner-bearing members", members, err)
	}
	encoded, err := json.Marshal(memberOwner)
	if err != nil {
		t.Fatal(err)
	}
	changed := 0
	for _, member := range members {
		var kind, name, signature string
		for _, field := range []struct {
			key   string
			value *string
		}{{"kind", &kind}, {"name", &name}, {"signature", &signature}} {
			if err := json.Unmarshal(member[field.key], field.value); err != nil {
				t.Fatal("member owner identity", field.key, err)
			}
		}
		match := len(selected) == 0
		for _, target := range selected {
			if kind == target.kind && name == target.name && signature == target.signature {
				match = true
			}
		}
		if match {
			member["owner"] = encoded
			changed++
		}
	}
	want := len(selected)
	if want == 0 {
		want = 5
	}
	if changed != want {
		t.Fatalf("selected member owner count got %d want %d", changed, want)
	}
	record["members"], err = json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	data, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return script[:start] + marker + base64.StdEncoding.EncodeToString(data) + script[end:]
}
func ownershipProviderHeld(s *fixture.RoleMembershipScenario, q string) *sql.Rows {
	s.T.Helper()
	var rows *sql.Rows
	var err error
	if s.Backend.PG {
		rows, err = s.P.Postgres.QueryContext(s.P.Ctx, q)
	} else {
		rows, err = s.C.QueryContext(s.P.Ctx, q)
	}
	if err != nil {
		s.T.Fatal(err)
	}
	return rows
}
func ownershipProviderHeldHotel(s *fixture.RoleMembershipScenario, rows *sql.Rows) {
	s.T.Helper()
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		s.T.Fatal(types, err)
	}
	if !rows.Next() {
		s.T.Fatal("retained result missing", rows.Err())
	}
	var v string
	if err := rows.Scan(&v); err != nil || v != "Hotel" {
		s.T.Fatal(v, err)
	}
	if rows.Next() || rows.Err() != nil {
		s.T.Fatal("retained cardinality", rows.Err())
	}
}
