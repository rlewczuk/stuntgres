package reference_test

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func transactionalPolicyExport(s *fixture.RoleMembershipScenario, text, id string) {
	s.T.Helper()
	definition := policyExportDefinition(s, text, "original")
	compact := regexp.MustCompile(`[\s"()]`).ReplaceAllString(definition, "")
	// Stored expressions may qualify their target columns. Compare the logical
	// definition without making that presentation choice part of the contract.
	compact = strings.ReplaceAll(compact, "t.", "")
	want := "CREATEPOLICYoriginalONpublic.tASPERMISSIVEFORALLTOtp_control_ownerUSINGid=" + id + "WITHCHECKallowed;"
	if compact != want {
		s.T.Fatalf("incomplete canonical policy definition: want %s in %s", want, definition)
	}
}

func TestReferenceTransactionalPoliciesControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			fixture.RoleMembershipCases(t, []string{"tp_control_owner"}, func(s *fixture.RoleMembershipScenario) {
				s.Actor("tp_control_owner")
				fixture.WithPublicSchemaOwner(t, s.P, s.Backend, "tp_control_owner", func() { policySetup(s) })
				s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
				s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
				s.Exec("CREATE POLICY original ON t TO tp_control_owner USING(id=1) WITH CHECK(allowed)")
				s.Actor("")
				pgPolicyFacts(s, "t", "original", "*", true, "tp_control_owner", "(id = 1)", "allowed")
				before := ""
				if !s.Backend.PG {
					before = policyDump(s)
					if !strings.Contains(before, "original") || !strings.Contains(before, "FORCE ROW LEVEL SECURITY") {
						t.Fatal("missing committed policy/flags", before)
					}
				}
				if !s.Backend.PG {
					transactionalPolicyExport(s, before, "1")
				}
				s.Exec("BEGIN")
				s.Exec("SAVEPOINT before_policy")
				s.Exec("ALTER POLICY original ON t USING(id=2)")
				s.Exec("ALTER POLICY original ON t RENAME TO private")
				pgPolicyFacts(s, "t", "private", "*", true, "tp_control_owner", "(id = 2)", "allowed")
				s.Run(fixture.Exec("INSERT INTO t VALUES(4,TRUE)", 1))
				s.Actor("tp_control_owner")
				s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				s.Actor("")
				if s.Backend.PG {
					s.Exec("ROLLBACK")
					pgPolicyFacts(s, "t", "original", "*", true, "tp_control_owner", "(id = 1)", "allowed")
					s.Actor("tp_control_owner")
					s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					s.Actor("")
					return
				}
				if got := policyDump(s); got != before {
					t.Fatal("Dump leaked owner policy/data", got, before)
				}
				failure := errors.New("policy transaction reader failure")
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, &policyReader{err: failure}); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader("CREATE TABLE partial(id INT); CREATE POLICY invalid ON partial USING(1);")); err == nil {
					t.Fatal("invalid late restore succeeded")
				}
				s.Exec("ALTER POLICY private ON t WITH CHECK(TRUE)")
				pgPolicyFacts(s, "t", "private", "*", true, "tp_control_owner", "(id = 2)", "true")
				if got := policyDump(s); got != before {
					t.Fatal("failed Restore changed committed predecessor", got)
				}
				h, err := s.C.PrepareContext(s.P.Ctx, "DROP POLICY private ON t")
				if err != nil {
					t.Fatal(err)
				}
				defer h.Close()
				retained, err := s.C.QueryContext(s.P.Ctx, "SELECT id FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				if replacement == "reset" {
					err = stuntgres.Reset(s.P.Ctx, s.P.Native)
				} else {
					err = stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before))
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []int64{1, 2, 4} {
					var got int64
					if !retained.Next() {
						t.Fatal("retained row missing", retained.Err())
					}
					if err := retained.Scan(&got); err != nil || got != want {
						t.Fatal(got, want, err)
					}
				}
				if retained.Next() || retained.Err() != nil {
					t.Fatal(retained.Err())
				}
				retained.Close()
				_, err = h.ExecContext(s.P.Ctx)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = s.C.ExecContext(s.P.Ctx, "ROLLBACK TO before_policy")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = s.C.ExecContext(s.P.Ctx, "COMMIT")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				s.Exec("ROLLBACK")
				if replacement == "reset" {
					if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before)); err != nil {
						t.Fatal(err)
					}
				}
				if got := policyDump(s); got != before {
					t.Fatal("replacement resurrected private policy/data", got, before)
				}
				pgPolicyFacts(s, "t", "original", "*", true, "tp_control_owner", "(id = 1)", "allowed")
				s.Actor("tp_control_owner")
				s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s.Actor("")
				// A representable committed policy stream roundtrips exactly, then
				// this same physical session can commit another policy transaction.
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, bytes.NewBufferString(before)); err != nil {
					t.Fatal(err)
				}
				s.Exec("BEGIN")
				s.Exec("ALTER POLICY original ON t USING(id=2)")
				s.Exec("COMMIT")
				pgPolicyFacts(s, "t", "original", "*", true, "tp_control_owner", "(id = 2)", "allowed")
				after := policyDump(s)
				if after == before || strings.Contains(after, "private") || !strings.Contains(after, "original") {
					t.Fatal("committed policy export", after)
				}
				transactionalPolicyExport(s, after, "2")
				s.Actor("tp_control_owner")
				s.Run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				s.Actor("")
			})
		})
	}
}
