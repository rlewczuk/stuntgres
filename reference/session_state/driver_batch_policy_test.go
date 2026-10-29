package reference_test

import (
	"bytes"
	"database/sql"
	"regexp"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// Native policy metadata is observed through supported utility consumers and
// committed Dump, not invented private pg_policy inquiries or RLS enforcement.
// PostgreSQL additionally exposes real catalog identities and stored selectors.
func batchPolicyDump(t *testing.T, p *fixture.Pair) string {
	t.Helper()
	var out bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func batchPolicyDefinition(t *testing.T, p *fixture.Pair, name, role, using, check string) {
	t.Helper()
	text := batchPolicyDump(t, p)
	definition := regexp.MustCompile(`(?is)CREATE\s+POLICY\s+"?` + regexp.QuoteMeta(name) + `"?\s+ON\s+[^;]+;`).FindString(text)
	if definition == "" {
		t.Fatal("policy missing from committed export", name, text)
	}
	// Read complete exported fields, preserving the bound schema/table names.
	// These simple conditions have no compound precedence; redundant parentheses,
	// identifier quoting and whitespace are presentation, not new SQL goldens.
	fields := regexp.MustCompile(`(?is)^CREATE\s+POLICY\s+(\S+)\s+ON\s+(\S+)\s+AS\s+(\S+)\s+FOR\s+(\S+)\s+TO\s+(.+?)\s+USING\s+(.+?)\s+WITH\s+CHECK\s+(.+);\s*$`).FindStringSubmatch(definition)
	if len(fields) != 8 {
		t.Fatal("incomplete exported policy fields", definition)
	}
	presentation := regexp.MustCompile(`[\s"()]`)
	compact := func(text string) string { return presentation.ReplaceAllString(text, "") }
	want := []string{name, "public.policy_t", "PERMISSIVE", "ALL", role, using, check}
	for i, expected := range want {
		if compact(fields[i+1]) != compact(expected) {
			t.Fatalf("exported policy field%d=%q want=%q definition=%s", i, fields[i+1], expected, definition)
		}
	}
}

func batchPolicyOID(t *testing.T, p *fixture.Pair, x *sql.Conn, name, role string) int64 {
	t.Helper()
	var oid, relation int64
	var policy, command string
	var permissive, targets, using, check bool
	q := "SELECT oid,polrelid,polname,polcmd::text,polpermissive,polroles=ARRAY[(SELECT oid FROM pg_roles WHERE rolname='" + role + "')]::oid[],polqual IS NOT NULL,polwithcheck IS NOT NULL FROM pg_policy WHERE polrelid='policy_t'::regclass AND polname='" + name + "'"
	if err := x.QueryRowContext(p.Ctx, q).Scan(&oid, &relation, &policy, &command, &permissive, &targets, &using, &check); err != nil {
		t.Fatal(err)
	}
	var table int64
	if err := x.QueryRowContext(p.Ctx, "SELECT 'policy_t'::regclass::oid").Scan(&table); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 1<<32-1 || relation != table || policy != name || command != "*" || !permissive || !targets || !using || !check {
		t.Fatalf("policy identity/metadata %d/%d/%d %s %s %t/%t/%t/%t", oid, relation, table, policy, command, permissive, targets, using, check)
	}
	return oid
}

func batchPolicyMissing(t *testing.T, p *fixture.Pair, x *sql.Conn, name string) {
	t.Helper()
	batchError(t, p, x, "ALTER POLICY "+name+" ON policy_t", batchDiag("42704", `policy "`+name+`" for table "policy_t" does not exist`))
}

func batchPolicySeed(t *testing.T, p *fixture.Pair, b fixture.Backend, a *sql.Conn) {
	t.Helper()
	batchExec(t, p, b, a, "CREATE TABLE policy_t(id INT,allowed BOOL)", 0)
	batchExec(t, p, b, a, "INSERT INTO policy_t VALUES(1,TRUE),(2,FALSE)", 2)
}

func batchPolicyRows(t *testing.T, p *fixture.Pair, b fixture.Backend, x *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.Select("SELECT id,allowed FROM policy_t ORDER BY id", []string{"id", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
}

func TestReferenceDriverBatchPolicyCreation(t *testing.T) {
	for _, disposition := range []string{"commit", "late_failure", "promote_commit", "promote_rollback"} {
		t.Run(disposition, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"batch_policy_target"})
			batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
				batchPolicySeed(t, p, b, a)
				before := ""
				if !b.PG {
					before = batchPolicyDump(t, p)
				}
				q := "CREATE ROLE batch_policy_target LOGIN; CREATE SEQUENCE batch_policy_seq START 41; CREATE POLICY p ON policy_t TO batch_policy_target USING(nextval('batch_policy_seq')>0) WITH CHECK(allowed); ALTER POLICY p ON policy_t RENAME TO renamed; ALTER POLICY renamed ON policy_t WITH CHECK(id>0)"
				if disposition == "late_failure" {
					q += "; SELECT missing FROM policy_t"
					w := batchDiag("42703", `column "missing" does not exist`)
					w.Position = batchPosition(q, "missing")
					batchError(t, p, a, q, w)
				} else {
					if strings.HasPrefix(disposition, "promote_") {
						q += "; BEGIN"
					}
					batchExec(t, p, b, a, q, 0)
				}
				var oid int64
				if strings.HasPrefix(disposition, "promote_") {
					// These are actual private consumers, not private native Dump.
					fixture.RunStep(t, p.Ctx, a, b.Recorder, fixture.Select("SELECT last_value,is_called FROM batch_policy_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}}))
					if b.PG {
						oid = batchPolicyOID(t, p, a, "renamed", "batch_policy_target")
						var n int
						if err := peer.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_policy WHERE polname='renamed' AND polrelid='policy_t'::regclass").Scan(&n); err != nil || n != 0 {
							t.Fatal("peer private policy", n, err)
						}
					} else if batchPolicyDump(t, p) != before {
						t.Fatal("private policy/role/provider leaked into committed Dump")
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_user WHERE usename='batch_policy_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					if disposition == "promote_commit" {
						batchExec(t, p, b, a, "COMMIT", 0)
					} else {
						batchExec(t, p, b, a, "ROLLBACK", 0)
					}
				}
				published := disposition == "commit" || disposition == "promote_commit"
				if published {
					if b.PG {
						got := batchPolicyOID(t, p, peer, "renamed", "batch_policy_target")
						if oid != 0 && got != oid {
							t.Fatal("promotion changed policy OID", oid, got)
						}
					} else {
						batchPolicyDefinition(t, p, "renamed", "batch_policy_target", "nextval('public.batch_policy_seq')>0", "policy_t.id>0")
					}
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT qual,with_check FROM pg_policies WHERE tablename='policy_t' AND policyname='renamed'", []string{"qual", "with_check"}, []string{"TEXT", "TEXT"}, [][]any{{"(nextval('batch_policy_seq'::regclass) > 0)", "(id > 0)"}}))
					batchExec(t, p, b, peer, "ALTER POLICY renamed ON policy_t", 0)
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT last_value,is_called FROM batch_policy_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(41), false}}))
					w := batchDiag("2BP01", `role "batch_policy_target" cannot be dropped because some objects depend on it`)
					w.Detail = "target of policy renamed on table policy_t"
					batchError(t, p, a, "DROP ROLE batch_policy_target", w)
					w = batchDiag("2BP01", "cannot drop sequence batch_policy_seq because other objects depend on it")
					w.Detail = "policy renamed on table policy_t depends on sequence batch_policy_seq"
					w.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					batchError(t, p, a, "DROP SEQUENCE batch_policy_seq", w)
					batchExec(t, p, b, a, "DROP POLICY renamed ON policy_t; DROP ROLE batch_policy_target; DROP SEQUENCE batch_policy_seq", 0)
				} else {
					batchPolicyMissing(t, p, a, "renamed")
					batchPolicyMissing(t, p, a, "p")
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_user WHERE usename='batch_policy_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE relname='batch_policy_seq'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					if !b.PG && batchPolicyDump(t, p) != before {
						t.Fatal("rollback changed committed policy/role/provider export")
					}
				}
				batchPolicyRows(t, p, b, peer)
			})
		})
	}
}

func TestReferenceDriverBatchPolicyAlterDrop(t *testing.T) {
	for _, operation := range []string{"alter", "drop"} {
		for _, disposition := range []string{"commit", "late_failure", "promote_commit", "promote_rollback"} {
			t.Run(operation+"/"+disposition, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"batch_policy_old", "batch_policy_new"})
				batchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, a, peer *sql.Conn) {
					batchPolicySeed(t, p, b, a)
					batchExec(t, p, b, a, "CREATE ROLE batch_policy_old LOGIN", 0)
					batchExec(t, p, b, a, "CREATE POLICY p ON policy_t TO batch_policy_old USING(id>0) WITH CHECK(allowed)", 0)
					var oid int64
					before := ""
					if b.PG {
						oid = batchPolicyOID(t, p, a, "p", "batch_policy_old")
					} else {
						batchPolicyDefinition(t, p, "p", "batch_policy_old", "policy_t.id>0", "policy_t.allowed")
						before = batchPolicyDump(t, p)
					}
					q := "CREATE ROLE batch_policy_new LOGIN; ALTER POLICY p ON policy_t TO batch_policy_new USING(id>1); ALTER POLICY p ON policy_t RENAME TO renamed; DROP ROLE batch_policy_old"
					if operation == "drop" {
						q = "DROP POLICY p ON policy_t; DROP ROLE batch_policy_old; CREATE POLICY p ON policy_t; DROP POLICY p ON policy_t; CREATE POLICY scratch ON policy_t; DROP POLICY scratch ON policy_t"
					}
					if disposition == "late_failure" {
						q += "; SELECT missing FROM policy_t"
						w := batchDiag("42703", `column "missing" does not exist`)
						w.Position = batchPosition(q, "missing")
						batchError(t, p, a, q, w)
					} else {
						if strings.HasPrefix(disposition, "promote_") {
							q += "; BEGIN"
						}
						batchExec(t, p, b, a, q, 0)
					}
					if strings.HasPrefix(disposition, "promote_") {
						// Mutations reached before BEGIN are private consumers.
						if b.PG && operation == "alter" && batchPolicyOID(t, p, a, "renamed", "batch_policy_new") != oid {
							t.Fatal("private ALTER changed OID")
						}
						if b.PG {
							if batchPolicyOID(t, p, peer, "p", "batch_policy_old") != oid {
								t.Fatal("peer policy changed before end")
							}
						} else if batchPolicyDump(t, p) != before {
							t.Fatal("private ALTER leaked")
						}
						if disposition == "promote_commit" {
							batchExec(t, p, b, a, "COMMIT", 0)
						} else {
							batchExec(t, p, b, a, "ROLLBACK", 0)
						}
					}
					published := disposition == "commit" || disposition == "promote_commit"
					if !published {
						batchExec(t, p, b, peer, "ALTER POLICY p ON policy_t", 0)
						if b.PG {
							if batchPolicyOID(t, p, peer, "p", "batch_policy_old") != oid {
								t.Fatal("rollback changed OID")
							}
						} else if batchPolicyDump(t, p) != before {
							t.Fatal("rollback changed full export")
						}
					} else if operation == "alter" {
						batchExec(t, p, b, peer, "ALTER POLICY renamed ON policy_t", 0)
						if b.PG {
							if batchPolicyOID(t, p, peer, "renamed", "batch_policy_new") != oid {
								t.Fatal("ALTER/rename changed OID")
							}
						} else {
							batchPolicyDefinition(t, p, "renamed", "batch_policy_new", "policy_t.id>1", "policy_t.allowed")
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT qual,with_check FROM pg_policies WHERE tablename='policy_t' AND policyname='renamed'", []string{"qual", "with_check"}, []string{"TEXT", "TEXT"}, [][]any{{"(id > 1)", "allowed"}}))
						batchExec(t, p, b, a, "DROP POLICY renamed ON policy_t; DROP ROLE batch_policy_new", 0)
					} else {
						batchPolicyMissing(t, p, peer, "p")
						batchPolicyMissing(t, p, peer, "scratch")
					}
					batchPolicyRows(t, p, b, peer)
				})
			})
		}
	}
}
