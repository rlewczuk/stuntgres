package reference_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// External I/O callbacks deterministically exercise capture-before-I/O fencing
// and reentrancy without sleeps or workers that can outlive fixture cleanup.
type policyReader struct {
	reader io.Reader
	before func()
	once   bool
	err    error
}

func (r *policyReader) Read(b []byte) (int, error) {
	if !r.once {
		r.once = true
		if r.before != nil {
			r.before()
		}
	}
	if r.err != nil {
		return 0, r.err
	}
	return r.reader.Read(b)
}

type policyWriter struct {
	before func()
	buffer bytes.Buffer
	once   bool
	err    error
}

func (w *policyWriter) Write(b []byte) (int, error) {
	if !w.once {
		w.once = true
		if w.before != nil {
			w.before()
		}
	}
	if w.err != nil {
		return 0, w.err
	}
	return w.buffer.Write(b)
}

func policyDump(s *fixture.RoleMembershipScenario) string {
	s.T.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &out); err != nil {
		s.T.Fatal(err)
	}
	return out.String()
}

func TestReferenceRLSPolicyControls(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
		fixture.RoleMembershipCases(t, []string{"policy_control_target", "policy_control_owner"}, func(s *fixture.RoleMembershipScenario) {
			// Create with the real named owner; owner transfer is not policy setup.
			s.Actor("policy_control_owner")
			fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "policy_control_owner", func() { policySetup(s) })
			s.Actor("")
			s.Exec("CREATE TABLE z_helper(id INTEGER)")
			s.Run(fixture.Exec("INSERT INTO z_helper VALUES(1)", 1))
			s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_control_target USING(EXISTS(SELECT 1 FROM z_helper WHERE z_helper.id=t.id)) WITH CHECK(allowed AND current_user=current_role)")
			policyRoleBlocked(s, "policy_control_target", "p")
			pgPolicyFacts(s, "t", "p", "w", false, "policy_control_target", "(EXISTS ( SELECT 1\n   FROM z_helper\n  WHERE (z_helper.id = t.id)))", "(allowed AND (CURRENT_USER = CURRENT_ROLE))")
			if !s.Backend.PG {
				dump := policyDump(s)
				if strings.Contains(dump, "CREATE ROLE") || !strings.Contains(dump, "policy_control_target") || !strings.Contains(dump, "CURRENT_USER") && !strings.Contains(dump, "current_user") {
					s.T.Fatal("role/expression export", dump)
				}
				if !strings.Contains(dump, "policy_control_owner") {
					s.T.Fatal("named policy owner omitted", dump)
				}
				if strings.Index(dump, "CREATE POLICY") < strings.Index(dump, "CREATE TABLE z_helper") {
					s.T.Fatal("policy precedes provider", dump)
				}
				if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
					s.T.Fatal(err)
				}
				// Reset releases policy dependencies but retains shared role identity.
				s.Exec("ALTER ROLE policy_control_target LOGIN")
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump)); err != nil {
					s.T.Fatal(err)
				}
				if again := policyDump(s); again != dump {
					s.T.Fatalf("redump changed\n%s\n%s", dump, again)
				}
				policyRows(s)
				pgPolicyFacts(s, "t", "p", "w", false, "policy_control_target", "(EXISTS ( SELECT 1\n   FROM z_helper\n  WHERE (z_helper.id = t.id)))", "(allowed AND (CURRENT_USER = CURRENT_ROLE))")
				policyRoleBlocked(s, "policy_control_target", "p")
				policyRestrict(s, "DROP TABLE z_helper", "cannot drop table z_helper because other objects depend on it", "policy p on table t depends on table z_helper")
			}
			s.Exec("DROP POLICY p ON t")
			s.Exec("DROP ROLE policy_control_target")
		})
	})
	for _, kind := range []string{"missing_role", "bad_condition", "parameter", "reader_error", "cancellation", "role_recreated"} {
		t.Run(kind, func(t *testing.T) {
			fixture.RoleMembershipCases(t, []string{"policy_control_target"}, func(s *fixture.RoleMembershipScenario) {
				policySetup(s)
				s.Exec("CREATE POLICY original ON t USING(id>0) WITH CHECK(allowed)")
				pgPolicyFacts(s, "t", "original", "*", true, "public", "(id > 0)", "allowed")
				if s.Backend.PG {
					return
				} // PostgreSQL has no native replacement API.
				before := policyDump(s)
				stream := "CREATE TABLE replacement(id INTEGER); CREATE POLICY incoming ON replacement TO policy_control_target USING(id>0);"
				sentinel := errors.New("policy fixture reader failure")
				ctx := s.P.Ctx
				r := &policyReader{reader: strings.NewReader(stream)}
				switch kind {
				case "missing_role":
					r.reader = strings.NewReader(strings.ReplaceAll(stream, "policy_control_target", "policy_absent"))
				case "bad_condition":
					r.reader = strings.NewReader(strings.ReplaceAll(stream, "USING(id>0)", "USING(1)"))
				case "parameter":
					r.reader = strings.NewReader(strings.ReplaceAll(stream, "USING(id>0)", "USING(EXISTS(SELECT $1))"))
				case "reader_error":
					r.err = sentinel
				case "cancellation":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(s.P.Ctx)
					defer cancel()
					r.before = cancel
				case "role_recreated":
					r.before = func() { s.Exec("DROP ROLE policy_control_target"); s.Exec("CREATE ROLE policy_control_target LOGIN") }
				}
				err := stuntgres.Restore(ctx, s.P.Native, r)
				if err == nil {
					s.T.Fatal("invalid replacement succeeded", kind)
				}
				switch kind {
				case "missing_role":
					fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `role "policy_absent" does not exist`})
				case "bad_condition":
					// Pinned policy semantic diagnostics have no source Position.
					fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of POLICY must be type boolean, not type integer"})
				case "parameter":
					fixture.AssertDiagnostic(s.T, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1"})
				case "reader_error":
					if !errors.Is(err, sentinel) {
						s.T.Fatal(err)
					}
				case "cancellation":
					if !errors.Is(err, context.Canceled) {
						s.T.Fatal(err)
					}
				}
				if after := policyDump(s); before != after {
					s.T.Fatalf("failed Restore changed catalog\nbefore=%s\nafter=%s", before, after)
				}
				policyRows(s)
				pgPolicyFacts(s, "t", "original", "*", true, "public", "(id > 0)", "allowed")
				policyColumnBlocked(s, "id", "original")
			})
		})
	}
	t.Run("writer_snapshot", func(t *testing.T) {
		fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
			policySetup(s)
			s.Exec("CREATE POLICY p ON t USING(id>0)")
			pgPolicyFacts(s, "t", "p", "*", true, "public", "(id > 0)", "")
			if s.Backend.PG {
				return
			}
			before := policyDump(s)
			w := &policyWriter{before: func() { s.Exec("ALTER POLICY p ON t USING(FALSE)") }}
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, w); err != nil {
				s.T.Fatal(err)
			}
			if w.buffer.String() != before {
				s.T.Fatal("Dump mixed pre/post callback catalog")
			}
			after := policyDump(s)
			if after == before {
				s.T.Fatal("writer callback mutation absent")
			}
			pgPolicyFacts(s, "t", "p", "*", true, "public", "false", "")
			sentinel := errors.New("policy fixture writer failure")
			if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &policyWriter{err: sentinel}); !errors.Is(err, sentinel) {
				s.T.Fatal(err)
			}
			if policyDump(s) != after {
				s.T.Fatal("writer failure mutated policy")
			}
		})
	})
}
