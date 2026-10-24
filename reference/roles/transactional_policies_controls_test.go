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

func transactionalPolicyExport(s *membershipScenario, text, id string) {
	s.t.Helper()
	definition := policyExportDefinition(s, text, "original")
	compact := regexp.MustCompile(`[\s"()]`).ReplaceAllString(definition, "")
	// Stored expressions may qualify their target columns. Compare the logical
	// definition without making that presentation choice part of the contract.
	compact = strings.ReplaceAll(compact, "t.", "")
	want := "CREATEPOLICYoriginalONpublic.tASPERMISSIVEFORALLTOtp_control_ownerUSINGid=" + id + "WITHCHECKallowed;"
	if compact != want {
		s.t.Fatalf("incomplete canonical policy definition: want %s in %s", want, definition)
	}
}

func TestReferenceTransactionalPoliciesControls(t *testing.T) {
	for _, replacement := range []string{"reset", "restore"} {
		t.Run(replacement, func(t *testing.T) {
			membershipCases(t, []string{"tp_control_owner"}, func(s *membershipScenario) {
				s.actor("tp_control_owner")
				fixture.WithPublicSchemaOwner(t, s.p, s.backend, "tp_control_owner", func() { policySetup(s) })
				s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
				s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
				s.exec("CREATE POLICY original ON t TO tp_control_owner USING(id=1) WITH CHECK(allowed)")
				s.actor("")
				before := ""
				if !s.backend.PG {
					before = policyDump(s)
					if !strings.Contains(before, "original") || !strings.Contains(before, "FORCE ROW LEVEL SECURITY") {
						t.Fatal("missing committed policy/flags", before)
					}
				}
				if !s.backend.PG {
					transactionalPolicyExport(s, before, "1")
				}
				s.exec("BEGIN")
				s.exec("SAVEPOINT before_policy")
				s.exec("ALTER POLICY original ON t USING(id=2)")
				s.exec("ALTER POLICY original ON t RENAME TO private")
				s.run(fixture.Exec("INSERT INTO t VALUES(4,TRUE)", 1))
				s.actor("tp_control_owner")
				s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				s.actor("")
				if s.backend.PG {
					s.exec("ROLLBACK")
					s.actor("tp_control_owner")
					s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					s.actor("")
					return
				}
				if got := policyDump(s); got != before {
					t.Fatal("Dump leaked owner policy/data", got, before)
				}
				failure := errors.New("policy transaction reader failure")
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, &policyReader{err: failure}); !errors.Is(err, failure) {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("CREATE TABLE partial(id INT); CREATE POLICY invalid ON partial USING(1);")); err == nil {
					t.Fatal("invalid late restore succeeded")
				}
				s.exec("ALTER POLICY private ON t WITH CHECK(TRUE)")
				if got := policyDump(s); got != before {
					t.Fatal("failed Restore changed committed predecessor", got)
				}
				h, err := s.c.PrepareContext(s.p.Ctx, "DROP POLICY private ON t")
				if err != nil {
					t.Fatal(err)
				}
				defer h.Close()
				retained, err := s.c.QueryContext(s.p.Ctx, "SELECT id FROM t ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				if replacement == "reset" {
					err = stuntgres.Reset(s.p.Ctx, s.p.Native)
				} else {
					err = stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(before))
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
				_, err = h.ExecContext(s.p.Ctx)
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = s.c.ExecContext(s.p.Ctx, "ROLLBACK TO before_policy")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				_, err = s.c.ExecContext(s.p.Ctx, "COMMIT")
				fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
				s.exec("ROLLBACK")
				if replacement == "reset" {
					if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader(before)); err != nil {
						t.Fatal(err)
					}
				}
				if got := policyDump(s); got != before {
					t.Fatal("replacement resurrected private policy/data", got, before)
				}
				s.actor("tp_control_owner")
				s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				s.actor("")
				// A representable committed policy stream roundtrips exactly, then
				// this same physical session can commit another policy transaction.
				if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewBufferString(before)); err != nil {
					t.Fatal(err)
				}
				s.exec("BEGIN")
				s.exec("ALTER POLICY original ON t USING(id=2)")
				s.exec("COMMIT")
				after := policyDump(s)
				if after == before || strings.Contains(after, "private") || !strings.Contains(after, "original") {
					t.Fatal("committed policy export", after)
				}
				transactionalPolicyExport(s, after, "2")
				s.actor("tp_control_owner")
				s.run(fixture.Select("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				s.actor("")
			})
		})
	}
}
