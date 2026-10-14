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

func policyDump(s *membershipScenario) string {
	s.t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &out); err != nil {
		s.t.Fatal(err)
	}
	return out.String()
}

func TestReferenceRLSPolicyControls(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
		membershipCases(t, []string{"policy_control_target", "policy_control_owner"}, func(s *membershipScenario) {
			// Create with the real named owner; owner transfer is not policy setup.
			s.actor("policy_control_owner")
			fixture.WithPublicSchemaOwner(s.t, s.p, s.backend, "policy_control_owner", func() { policySetup(s) })
			s.actor("")
			s.exec("CREATE TABLE z_helper(id INTEGER)")
			s.run(fixture.Exec("INSERT INTO z_helper VALUES(1)", 1))
			s.exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_control_target USING(EXISTS(SELECT 1 FROM z_helper WHERE z_helper.id=t.id)) WITH CHECK(allowed AND current_user=current_role)")
			policyRoleBlocked(s, "policy_control_target", "p")
			if !s.backend.PG {
				dump := policyDump(s)
				if strings.Contains(dump, "CREATE ROLE") || !strings.Contains(dump, "policy_control_target") || !strings.Contains(dump, "CURRENT_USER") && !strings.Contains(dump, "current_user") {
					s.t.Fatal("role/expression export", dump)
				}
				if !strings.Contains(dump, "policy_control_owner") {
					s.t.Fatal("named policy owner omitted", dump)
				}
				if strings.Index(dump, "CREATE POLICY") < strings.Index(dump, "CREATE TABLE z_helper") {
					s.t.Fatal("policy precedes provider", dump)
				}
				if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
					s.t.Fatal(err)
				}
				// Reset releases policy dependencies but retains shared role identity.
				s.exec("ALTER ROLE policy_control_target LOGIN")
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(dump)); err != nil {
					s.t.Fatal(err)
				}
				if again := policyDump(s); again != dump {
					s.t.Fatalf("redump changed\n%s\n%s", dump, again)
				}
				policyRows(s)
				policyRoleBlocked(s, "policy_control_target", "p")
				policyRestrict(s, "DROP TABLE z_helper", "cannot drop table z_helper because other objects depend on it", "policy p on table t depends on table z_helper")
			}
			s.exec("DROP POLICY p ON t")
			s.exec("DROP ROLE policy_control_target")
		})
	})
	for _, kind := range []string{"missing_role", "bad_condition", "parameter", "reader_error", "cancellation", "role_recreated"} {
		t.Run(kind, func(t *testing.T) {
			membershipCases(t, []string{"policy_control_target"}, func(s *membershipScenario) {
				policySetup(s)
				s.exec("CREATE POLICY original ON t USING(id>0) WITH CHECK(allowed)")
				if s.backend.PG {
					return
				} // PostgreSQL has no native replacement API.
				before := policyDump(s)
				stream := "CREATE TABLE replacement(id INTEGER); CREATE POLICY incoming ON replacement TO policy_control_target USING(id>0);"
				sentinel := errors.New("policy fixture reader failure")
				ctx := s.p.Ctx
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
					ctx, cancel = context.WithCancel(s.p.Ctx)
					defer cancel()
					r.before = cancel
				case "role_recreated":
					r.before = func() { s.exec("DROP ROLE policy_control_target"); s.exec("CREATE ROLE policy_control_target LOGIN") }
				}
				err := stuntgres.Restore(ctx, s.p.Native, r)
				if err == nil {
					s.t.Fatal("invalid replacement succeeded", kind)
				}
				switch kind {
				case "missing_role":
					fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `role "policy_absent" does not exist`})
				case "bad_condition":
					// Pinned policy semantic diagnostics have no source Position.
					fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of POLICY must be type boolean, not type integer"})
				case "parameter":
					fixture.AssertDiagnostic(s.t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P02", Message: "there is no parameter $1"})
				case "reader_error":
					if !errors.Is(err, sentinel) {
						s.t.Fatal(err)
					}
				case "cancellation":
					if !errors.Is(err, context.Canceled) {
						s.t.Fatal(err)
					}
				}
				if after := policyDump(s); before != after {
					s.t.Fatalf("failed Restore changed catalog\nbefore=%s\nafter=%s", before, after)
				}
				policyRows(s)
				policyColumnBlocked(s, "id", "original")
			})
		})
	}
	t.Run("writer_snapshot", func(t *testing.T) {
		membershipCases(t, nil, func(s *membershipScenario) {
			policySetup(s)
			s.exec("CREATE POLICY p ON t USING(id>0)")
			if s.backend.PG {
				return
			}
			before := policyDump(s)
			w := &policyWriter{before: func() { s.exec("ALTER POLICY p ON t USING(FALSE)") }}
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, w); err != nil {
				s.t.Fatal(err)
			}
			if w.buffer.String() != before {
				s.t.Fatal("Dump mixed pre/post callback catalog")
			}
			after := policyDump(s)
			if after == before {
				s.t.Fatal("writer callback mutation absent")
			}
			sentinel := errors.New("policy fixture writer failure")
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &policyWriter{err: sentinel}); !errors.Is(err, sentinel) {
				s.t.Fatal(err)
			}
			if policyDump(s) != after {
				s.t.Fatal("writer failure mutated policy")
			}
		})
	})
}
