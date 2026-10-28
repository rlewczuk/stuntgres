package reference_test

import (
	"bytes"
	"fmt"
	"sort"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Creation names deliberately overlap final names, repeat across definitions,
// and occupy the renderer's staging-name namespace. None is a NULL-slot alias
// when it originates from a subscription name rather than an explicit option.
func TestReferenceSubscriptionRenamedSlotReplay(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			cases := []struct {
				origin, name string
				enabled      bool
				nullSlot     bool
			}{
				{"UpperSlotReplay", "none", true, false},
				{"none", "UpperSlotReplay", true, false},
				{"ÉtageSlotReplay", "renamed_utf_slot", true, false},
				{"UpperSlotReplay", "again_upper_slot", false, false},
				{"none", "again_none_slot", false, false},
				{"ÉtageSlotReplay", "again_utf_slot", false, false},
				{"_stuntgres_subscription_1", "_stuntgres_subscription_2", false, false},
				{"null_slot_origin", "_stuntgres_subscription_3", false, true},
			}
			identifier := func(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
			literal := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
			intermediate := func(i int) string { return fmt.Sprintf("slot_replay_fixture_%d", i) }
			var names []string
			for i, c := range cases {
				names = append(names, c.origin, c.name, intermediate(i))
			}
			owners := []string{"slot_replay_owner_a", "slot_replay_owner_b"}
			fixture.RoleReferenceCleanup(t, owners)
			p := subscriptionScenario(t, names...)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					for _, owner := range owners {
						subscriptionExec(t, p, b, b.DB, "CREATE ROLE "+owner, prepared)
					}
					subscriptionExec(t, p, b, b.DB, "CREATE TABLE slot_replay_sentinel(v int)", prepared)
					subscriptionRun(t, p, b, b.DB, fixture.Exec("INSERT INTO slot_replay_sentinel VALUES (77)", 1))
					for i, c := range cases {
						option := ""
						if c.nullSlot {
							option = ",slot_name=NONE"
						}
						subscriptionExec(t, p, b, b.DB, "CREATE SUBSCRIPTION "+identifier(c.origin)+" CONNECTION 'host=publisher.invalid password=replay_secret application_name=slot_replay' PUBLICATION second_remote,first_remote WITH (connect=false,binary=true,streaming=true,two_phase=true,disable_on_error=true,password_required=false,run_as_owner=true,failover=true,synchronous_commit=remote_apply,origin=none"+option+")", prepared)
						// Immediately free the origin, including repeated origins.
						subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+identifier(c.origin)+" RENAME TO "+identifier(intermediate(i)), prepared)
					}
					for i, c := range cases {
						name := identifier(c.name)
						subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+identifier(intermediate(i))+" RENAME TO "+name, prepared)
						subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+name+" SKIP (lsn='B/2345')", prepared)
						if c.enabled {
							// Oracle ENABLE is observed inside a rolled-back transaction:
							// no apply worker is launched against the dummy publisher.
							tx, err := b.DB.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							subscriptionExec(t, p, b, tx, "ALTER SUBSCRIPTION "+name+" ENABLE", prepared)
							subscriptionRun(t, p, b, tx, fixture.Select("SELECT subenabled,subslotname FROM pg_subscription WHERE subname="+literal(c.name), []string{"subenabled", "subslotname"}, []string{"BOOL", "NAME"}, [][]any{{true, c.origin}}))
							if err := tx.Rollback(); err != nil {
								t.Fatal(err)
							}
							if !b.PG {
								subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+name+" ENABLE", prepared)
							}
						}
						subscriptionExec(t, p, b, b.DB, "ALTER SUBSCRIPTION "+name+" OWNER TO "+owners[i%len(owners)], prepared)
					}
					database := subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_database WHERE datname=current_database()")
					ownerIDs := make([]int64, len(owners))
					for i, owner := range owners {
						ownerIDs[i] = subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_roles WHERE rolname="+literal(owner))
					}
					check := func() []int64 {
						t.Helper()
						ids := make([]int64, len(cases))
						for i, c := range cases {
							ids[i] = subscriptionNumber(t, p, b.DB, "SELECT oid::bigint FROM pg_subscription WHERE subname="+literal(c.name))
							if ids[i] <= 0 {
								t.Fatal("invalid replay identity", c.name, ids[i])
							}
							var slot any = c.origin
							if c.nullSlot {
								slot = nil
							}
							step := subscriptionEmpty(b.PG, "SELECT * FROM pg_subscription WHERE subname="+literal(c.name), false)
							step.Rows = [][]any{{ids[i], database, "B/2345", c.name, ownerIDs[i%len(owners)], c.enabled && !b.PG, true, "t", "p", true, false, true, true, "host=publisher.invalid password=replay_secret application_name=slot_replay", slot, "remote_apply", "{second_remote,first_remote}", "none"}}
							step.Prepared = prepared
							fixture.OidReferenceRows(t, p.Ctx, b, b.DB, step)
						}
						if n := subscriptionNumber(t, p, b.DB, "SELECT count(*) FROM pg_subscription WHERE subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())"); n != int64(len(cases)) {
							t.Fatal("replay leaked or lost subscriptions", n)
						}
						for ownerIndex, owner := range owners {
							var owned []int
							for i := range cases {
								if i%len(owners) == ownerIndex {
									owned = append(owned, i)
								}
							}
							sort.Slice(owned, func(i, j int) bool { return ids[owned[i]] < ids[owned[j]] })
							details := make([]string, len(owned))
							for i, index := range owned {
								name := cases[index].name
								details[i] = "owner of subscription " + name
							}
							want := fixture.ControlsReferenceDiagnostic("2BP01", `role "`+owner+`" cannot be dropped because some objects depend on it`)
							want.Detail = strings.Join(details, "\n")
							step := fixture.ErrorDiagnostic("DROP ROLE "+owner, want)
							step.Prepared = prepared
							step.ErrorStage = "execute"
							subscriptionRun(t, p, b, b.DB, step)
						}
						return ids
					}
					ids := check()
					if !b.PG {
						dump := func() []byte {
							t.Helper()
							var out bytes.Buffer
							if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
								t.Fatal(err)
							}
							return out.Bytes()
						}
						canonical := dump()
						if !bytes.Equal(canonical, dump()) {
							t.Fatal("initial replay dump is nondeterministic")
						}
						// A failure after the complete replay must not replace the
						// destination's identities, fields, links or newer table data.
						subscriptionRun(t, p, b, b.DB, fixture.Exec("UPDATE slot_replay_sentinel SET v=91", 1))
						beforeFailure := dump()
						invalid := append(bytes.Clone(canonical), []byte("CREATE SUBSCRIPTION late_invalid_slot_replay CONNECTION 'testconn' PUBLICATION p WITH (connect=false);")...)
						err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(invalid))
						fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "invalid connection string syntax: missing \"=\" after \"testconn\" in connection info string\n"})
						unchanged := check()
						for i, id := range ids {
							if unchanged[i] != id {
								t.Fatal("failed replay replaced identity", cases[i].name)
							}
						}
						if !bytes.Equal(beforeFailure, dump()) {
							t.Fatal("failed replay changed destination")
						}
						subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT v FROM slot_replay_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}}))
						for round := range 3 {
							if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
								t.Fatal(err)
							}
							if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(canonical)); err != nil {
								t.Fatal(err)
							}
							restored := check()
							for i, id := range restored {
								if id == ids[i] {
									t.Fatal("successful replay did not remint identity", cases[i].name, round)
								}
							}
							ids = restored
							subscriptionRun(t, p, b, b.DB, fixture.Select("SELECT v FROM slot_replay_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(77)}}))
							if !bytes.Equal(canonical, dump()) {
								t.Fatal("replay changed canonical dump", round)
							}
						}
					}
					for _, c := range cases {
						subscriptionDetach(t, p, b, b.DB, identifier(c.name))
					}
					for _, owner := range owners {
						subscriptionExec(t, p, b, b.DB, "DROP ROLE "+owner, prepared)
					}
				})
			}
		})
	}
}
