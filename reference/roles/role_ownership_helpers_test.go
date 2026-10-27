package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

var ownershipRoles = []string{"own_source", "own_target", "own_actor", "own_other", "own_reader", "own_middle", "own_downstream", "own_alt", "own Quoted"}

func ownershipCases(t *testing.T, body func(*fixture.RoleMembershipScenario)) {
	t.Helper()
	ownershipCasesFor(t, []bool{false, true}, body)
}

func ownershipCasesFor(t *testing.T, modes []bool, body func(*fixture.RoleMembershipScenario)) {
	t.Helper()
	fixture.RoleMembershipCasesFor(t, ownershipRoles, modes, func(s *fixture.RoleMembershipScenario) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := s.C.ExecContext(ctx, "ROLLBACK"); err != nil {
				s.T.Error(err)
			}
			if _, err := s.C.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				s.T.Error(err)
			}
			ownershipCleanup(ctx, s)
		}()
		if s.Backend.PG {
			s.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
		}
		body(s)
	})
}

// ownershipCleanup drops what the surviving ownership roles own or were
// granted, including nonpublic objects, and then the fixture schemas. Names
// can intentionally have been dropped or recreated by the body.
func ownershipCleanup(ctx context.Context, s *fixture.RoleMembershipScenario) {
	// One role per DROP OWNED: PostgreSQL 18 fails a multi-role DROP OWNED with
	// XX000 "could not find tuple for default ACL" when a listed role's default
	// privileges grant to another listed role.
	for _, role := range ownershipExistingRoles(ctx, s) {
		if _, err := s.C.ExecContext(ctx, "DROP OWNED BY "+role+" CASCADE"); err != nil {
			s.T.Error(err)
		}
	}
	if _, err := s.C.ExecContext(ctx, "DROP SCHEMA IF EXISTS own_class,own_gate,own_bulk,own_control,own_schema,own_schema_renamed CASCADE"); err != nil {
		s.T.Error(err)
	}
}

// ownershipExistingRoles returns the quoted ownershipRoles that exist, in
// ownershipRoles order, from one catalog query.
func ownershipExistingRoles(ctx context.Context, s *fixture.RoleMembershipScenario) []string {
	literals := make([]string, len(ownershipRoles))
	for i, role := range ownershipRoles {
		literals[i] = pq.QuoteLiteral(role)
	}
	rows, err := s.C.QueryContext(ctx, "SELECT rolname FROM pg_roles WHERE rolname IN ("+strings.Join(literals, ",")+")")
	if err != nil {
		s.T.Error(err)
		return nil
	}
	found := map[string]bool{}
	for rows.Next() {
		var role string
		if err := rows.Scan(&role); err != nil {
			s.T.Error(err)
		}
		found[role] = true
	}
	if err := rows.Err(); err != nil {
		s.T.Error(err)
	}
	rows.Close()
	var existing []string
	for _, role := range ownershipRoles {
		if found[role] {
			existing = append(existing, pq.QuoteIdentifier(role))
		}
	}
	return existing
}

func ownershipSelect(s *fixture.RoleMembershipScenario, q string, columns, types []string, rows [][]any) {
	s.T.Helper()
	s.Run(fixture.Select(q, columns, types, rows))
}
func ownershipOwner(s *fixture.RoleMembershipScenario, name, owner string) {
	s.T.Helper()
	ownershipSelect(s, "SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid="+pq.QuoteLiteral(name)+"::regclass::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}})
}
func ownershipRows(s *fixture.RoleMembershipScenario, name string) {
	s.T.Helper()
	ownershipSelect(s, "SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
}
func ownershipSeed(s *fixture.RoleMembershipScenario, name, owner string) {
	s.T.Helper()
	s.Actor(owner)
	if !strings.Contains(name, ".") || strings.HasPrefix(name, "public.") {
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, owner, func() { s.Exec("CREATE TABLE " + name + "(id INT)") })
	} else {
		s.Exec("CREATE TABLE " + name + "(id INT)")
	}
	s.Run(fixture.Exec("INSERT INTO "+name+" VALUES(7)", 1))
	s.Actor("")
}
func ownershipFailure(s *fixture.RoleMembershipScenario, q string, d fixture.Diagnostic) {
	s.T.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	step := fixture.ErrorDiagnostic(q, d)
	step.ErrorStage = "execute"
	s.Run(step)
}
func ownershipNotices(s *fixture.RoleMembershipScenario, want ...fixture.Diagnostic) {
	s.T.Helper()
	got := *s.Notices
	if len(got) != len(want) || len(got) != 0 && !reflect.DeepEqual(got, want) {
		s.T.Fatalf("notice vector got %#v want %#v", got, want)
	}
	s.ClearNotices()
}

func ownershipDetailLines(t *testing.T, got, want string) {
	t.Helper()
	counts := func(detail string) map[string]int {
		m := map[string]int{}
		for _, line := range strings.Split(detail, "\n") {
			m[line]++
		}
		return m
	}
	if !reflect.DeepEqual(counts(got), counts(want)) {
		t.Fatalf("dependency detail got %q want %q", got, want)
	}
}

// Catalog reference indexes do not order dependent ties. Only Detail line
// order varies: every independently specified line and multiplicity is exact.
func ownershipFailureDetailBag(s *fixture.RoleMembershipScenario, q string, want fixture.Diagnostic) {
	s.T.Helper()
	var err error
	if s.Prepared {
		stmt, e := s.C.PrepareContext(s.P.Ctx, q)
		if e != nil {
			s.T.Fatal("expected execution-phase refusal, Prepare failed", e)
		}
		defer stmt.Close()
		_, err = stmt.ExecContext(s.P.Ctx)
	} else {
		_, err = s.C.ExecContext(s.P.Ctx, q)
	}
	got := fixture.ExtractDiagnostic(s.T, err)
	ownershipDetailLines(s.T, got.Detail, want.Detail)
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	want.Detail = got.Detail // Content is independently checked above.
	fixture.AssertDiagnostic(s.T, err, &want)
}

func ownershipNoticesDetailBag(s *fixture.RoleMembershipScenario, want ...fixture.Diagnostic) {
	s.T.Helper()
	got := *s.Notices
	if len(got) != len(want) {
		s.T.Fatalf("notice vector got %#v want %#v", got, want)
	}
	for i := range want {
		ownershipDetailLines(s.T, got[i].Detail, want[i].Detail)
		want[i].Detail = got[i].Detail // Notice list order and all other fields exact.
	}
	if !reflect.DeepEqual(got, want) {
		s.T.Fatalf("notice vector got %#v want %#v", got, want)
	}
	s.ClearNotices()
}
func ownershipCascadeNotice(message, detail string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail}
}
func ownershipBool(s *fixture.RoleMembershipScenario, q string, want bool) {
	ownershipSelect(s, "SELECT ("+q+") AS ok", []string{"ok"}, []string{"BOOL"}, [][]any{{want}})
}
func ownershipCount(s *fixture.RoleMembershipScenario, q string, want int64) {
	ownershipSelect(s, "SELECT ("+q+") AS n", []string{"n"}, []string{"INT8"}, [][]any{{want}})
}

// Preserve each caller's independently specified nonowner row set.
func ownershipPolicyRows(s *fixture.RoleMembershipScenario, query string, columns []string, rows [][]any) {
	s.T.Helper()
	types := make([]string, len(columns))
	for i := range types {
		types[i] = "INT4"
	}
	ownershipSelect(s, query, columns, types, rows)
}

// Assert the inquiry and retain the independent owner-only object read witness
// for the configured all-INHERIT path.
func ownershipInherited(s *fixture.RoleMembershipScenario, member, role, mode string, want bool) {
	s.T.Helper()
	ownershipBool(s, "SELECT pg_has_role("+pq.QuoteLiteral(member)+","+pq.QuoteLiteral(role)+","+pq.QuoteLiteral(mode)+")", want)
	s.Actor("")
	s.Exec("CREATE TABLE own_inheritance_probe(id INT)")
	s.Run(fixture.Exec("INSERT INTO own_inheritance_probe VALUES(7)", 1))
	s.Exec("ALTER TABLE own_inheritance_probe OWNER TO " + pq.QuoteIdentifier(role))
	s.Actor(member)
	if want {
		ownershipRows(s, "own_inheritance_probe")
	} else {
		ownershipFailure(s, "SELECT id FROM own_inheritance_probe", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_inheritance_probe"})
	}
	s.Actor("")
	s.Exec("DROP TABLE own_inheritance_probe")
}
func ownershipRoleDependency(s *fixture.RoleMembershipScenario, role string, lines ...string) {
	s.T.Helper()
	want := fixture.ControlsReferenceDiagnostic("2BP01", fmt.Sprintf("role %q cannot be dropped because some objects depend on it", role))
	want.Detail = strings.Join(lines, "\n")
	ownershipFailureDetailBag(s, "DROP ROLE "+pq.QuoteIdentifier(role), want)
}

func ownershipAborted(s *fixture.RoleMembershipScenario, q string) {
	s.T.Helper()
	step := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
	step.Prepared = s.Prepared
	step.ErrorStage = "execute"
	if s.Prepared {
		step.ErrorStage = "prepare"
	}
	fixture.RunStep(s.T, s.P.Ctx, s.C, s.Record, step)
}

func ownershipUtilitySignature(s *fixture.RoleMembershipScenario, q string) {
	s.T.Helper()
	// The Query consumer must receive a real empty cursor, not nil Rows.
	s.Run(fixture.Select(q, []string{}, []string{}, nil))
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(s.P.Ctx, int64(1))
	if err == nil || err.Error() != "sql: expected 0 arguments, got 1" {
		s.T.Fatal("zero-input utility signature", err)
	}
}

func ownershipDefinition(s *fixture.RoleMembershipScenario, q string) {
	s.T.Helper()
	// Selected populated MVs each contain the independently known single row 7.
	if strings.HasPrefix(q, "CREATE MATERIALIZED VIEW") {
		s.Run(fixture.Exec(q, 1))
		return
	}
	s.Exec(q)
}
