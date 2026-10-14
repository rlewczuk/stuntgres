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

func ownershipCases(t *testing.T, body func(*membershipScenario)) {
	t.Helper()
	membershipCases(t, ownershipRoles, func(s *membershipScenario) {
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err := s.c.ExecContext(ctx, "ROLLBACK"); err != nil {
				s.t.Error(err)
			}
			if _, err := s.c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				s.t.Error(err)
			}
			// Real public cleanup includes nonpublic objects and received grants.
			// Names can intentionally have been dropped/recreated by the body.
			for _, role := range ownershipRoles {
				var exists bool
				if err := s.c.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)", role).Scan(&exists); err != nil {
					s.t.Error(err)
					continue
				}
				if exists {
					if _, err := s.c.ExecContext(ctx, "DROP OWNED BY "+pq.QuoteIdentifier(role)+" CASCADE"); err != nil {
						s.t.Error(err)
					}
				}
			}
			for _, schema := range []string{"own_class", "own_gate", "own_bulk", "own_control", "own_schema", "own_schema_renamed"} {
				if _, err := s.c.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pq.QuoteIdentifier(schema)+" CASCADE"); err != nil {
					s.t.Error(err)
				}
			}
		}()
		if s.backend.PG {
			s.exec("GRANT USAGE ON SCHEMA public TO PUBLIC")
		}
		body(s)
	})
}

func ownershipSelect(s *membershipScenario, q string, columns, types []string, rows [][]any) {
	s.t.Helper()
	s.run(fixture.Select(q, columns, types, rows))
}
func ownershipOwner(s *membershipScenario, name, owner string) {
	s.t.Helper()
	ownershipSelect(s, "SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.oid="+pq.QuoteLiteral(name)+"::regclass::oid", []string{"rolname"}, []string{"NAME"}, [][]any{{owner}})
}
func ownershipRows(s *membershipScenario, name string) {
	s.t.Helper()
	ownershipSelect(s, "SELECT id FROM "+name+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
}
func ownershipSeed(s *membershipScenario, name, owner string) {
	s.t.Helper()
	s.actor(owner)
	if !strings.Contains(name, ".") || strings.HasPrefix(name, "public.") {
		fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, owner, func() { s.exec("CREATE TABLE " + name + "(id INT)") })
	} else {
		s.exec("CREATE TABLE " + name + "(id INT)")
	}
	s.run(fixture.Exec("INSERT INTO "+name+" VALUES(7)", 1))
	s.actor("")
}
func ownershipFailure(s *membershipScenario, q string, d fixture.Diagnostic) {
	s.t.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	step := fixture.ErrorDiagnostic(q, d)
	step.ErrorStage = "execute"
	s.run(step)
}
func ownershipNotices(s *membershipScenario, want ...fixture.Diagnostic) {
	s.t.Helper()
	got := *s.notices
	if len(got) != len(want) || len(got) != 0 && !reflect.DeepEqual(got, want) {
		s.t.Fatalf("notice vector got %#v want %#v", got, want)
	}
	s.clearNotices()
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
func ownershipFailureDetailBag(s *membershipScenario, q string, want fixture.Diagnostic) {
	s.t.Helper()
	var err error
	if s.prepared {
		stmt, e := s.c.PrepareContext(s.p.Ctx, q)
		if e != nil {
			s.t.Fatal("expected execution-phase refusal, Prepare failed", e)
		}
		defer stmt.Close()
		_, err = stmt.ExecContext(s.p.Ctx)
	} else {
		_, err = s.c.ExecContext(s.p.Ctx, q)
	}
	got := fixture.ExtractDiagnostic(s.t, err)
	ownershipDetailLines(s.t, got.Detail, want.Detail)
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	want.Detail = got.Detail // Content is independently checked above.
	fixture.AssertDiagnostic(s.t, err, &want)
}

func ownershipNoticesDetailBag(s *membershipScenario, want ...fixture.Diagnostic) {
	s.t.Helper()
	got := *s.notices
	if len(got) != len(want) {
		s.t.Fatalf("notice vector got %#v want %#v", got, want)
	}
	for i := range want {
		ownershipDetailLines(s.t, got[i].Detail, want[i].Detail)
		want[i].Detail = got[i].Detail // Notice list order and all other fields exact.
	}
	if !reflect.DeepEqual(got, want) {
		s.t.Fatalf("notice vector got %#v want %#v", got, want)
	}
	s.clearNotices()
}
func ownershipCascadeNotice(message, detail string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail}
}
func ownershipBool(s *membershipScenario, q string, want bool) {
	ownershipSelect(s, "SELECT ("+q+") AS ok", []string{"ok"}, []string{"BOOL"}, [][]any{{want}})
}
func ownershipCount(s *membershipScenario, q string, want int64) {
	ownershipSelect(s, "SELECT ("+q+") AS n", []string{"n"}, []string{"INT8"}, [][]any{{want}})
}

// BASE admits policy applicability, flags and default deny, but deliberately
// excludes expression evaluation. Keep the real PostgreSQL row prediction and
// the native complete boundary diagnostic instead of inventing policy metadata.
func ownershipPolicyRows(s *membershipScenario, query string, columns []string, rows [][]any) {
	s.t.Helper()
	if s.backend.PG {
		types := make([]string, len(columns))
		for i := range types {
			types[i] = "INT4"
		}
		ownershipSelect(s, query, columns, types, rows)
	} else {
		predefinedFailure(s, query, fixture.Diagnostic{Code: "0A000", Message: "row-security policy expression enforcement is unsupported"}, true, "execute")
	}
}

// The inquiry is a PG supplemental provider. Shared SQL proves the same
// configured all-INHERIT path by actual read authority on an owner-only object.
func ownershipInherited(s *membershipScenario, member, role, mode string, want bool) {
	s.t.Helper()
	if s.backend.PG {
		ownershipBool(s, "SELECT pg_has_role("+pq.QuoteLiteral(member)+","+pq.QuoteLiteral(role)+","+pq.QuoteLiteral(mode)+")", want)
	}
	s.actor("")
	s.exec("CREATE TABLE own_inheritance_probe(id INT)")
	s.run(fixture.Exec("INSERT INTO own_inheritance_probe VALUES(7)", 1))
	s.exec("ALTER TABLE own_inheritance_probe OWNER TO " + pq.QuoteIdentifier(role))
	s.actor(member)
	if want {
		ownershipRows(s, "own_inheritance_probe")
	} else {
		ownershipFailure(s, "SELECT id FROM own_inheritance_probe", fixture.Diagnostic{Code: "42501", Message: "permission denied for table own_inheritance_probe"})
	}
	s.actor("")
	s.exec("DROP TABLE own_inheritance_probe")
}
func ownershipRoleDependency(s *membershipScenario, role string, lines ...string) {
	s.t.Helper()
	want := fixture.ControlsReferenceDiagnostic("2BP01", fmt.Sprintf("role %q cannot be dropped because some objects depend on it", role))
	want.Detail = strings.Join(lines, "\n")
	ownershipFailureDetailBag(s, "DROP ROLE "+pq.QuoteIdentifier(role), want)
}

func ownershipAborted(s *membershipScenario, q string) {
	s.t.Helper()
	step := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
	step.Prepared = s.prepared
	step.ErrorStage = "execute"
	if s.prepared {
		step.ErrorStage = "prepare"
	}
	fixture.RunStep(s.t, s.p.Ctx, s.c, s.record, step)
}

func ownershipUtilitySignature(s *membershipScenario, q string) {
	s.t.Helper()
	// The Query consumer must receive a real empty cursor, not nil Rows.
	s.run(fixture.Select(q, []string{}, []string{}, nil))
	stmt, err := s.c.PrepareContext(s.p.Ctx, q)
	if err != nil {
		s.t.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(s.p.Ctx, int64(1))
	if err == nil || err.Error() != "sql: expected 0 arguments, got 1" {
		s.t.Fatal("zero-input utility signature", err)
	}
}

func ownershipDefinition(s *membershipScenario, q string) {
	s.t.Helper()
	// Selected populated MVs each contain the independently known single row 7.
	if strings.HasPrefix(q, "CREATE MATERIALIZED VIEW") {
		s.run(fixture.Exec(q, 1))
		return
	}
	s.exec(q)
}
