package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalIndexNames(t *testing.T) {
	for _, commit := range []bool{false, true} {
		for _, kind := range []string{"explicit", "conditional", "generated"} {
			t.Run(fmt.Sprintf("commit=%t/%s", commit, kind), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						a, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer a.Close()
						runA := transactionalIndexRun(t, p, b, a)
						runC := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
						for _, q := range []string{"CREATE TABLE first(n INT)", "CREATE TABLE second(n INT)", "BEGIN", "CREATE INDEX second_n_idx ON first(n)"} {
							runA(fixture.Exec(q, 0))
						}
						defer a.ExecContext(p.Ctx, "ROLLBACK")
						ap, cp := 0, 0
						if b.PG {
							ap = fixture.LockPID(t, p.Ctx, a)
							cp = fixture.LockPID(t, p.Ctx, c)
						}
						namespaceID := fixture.UniqueReferencePublicNamespace(t, p.Ctx, b.DB, b.PG)
						runC(fixture.Exec("BEGIN", 0))
						defer c.ExecContext(p.Ctx, "ROLLBACK")
						q := "CREATE INDEX second_n_idx ON second(n)"
						if kind == "conditional" {
							q = "CREATE INDEX IF NOT EXISTS second_n_idx ON second(n)"
						}
						if kind == "generated" {
							q = "CREATE INDEX ON second(n)"
						}
						done := make(chan struct{})
						await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
							defer close(done)
							r, err := c.ExecContext(ctx, q)
							return fixture.RelationOutcome{Result: r, Err: err}
						})
						var generated fixture.RelationOutcome
						if kind == "generated" {
							// PostgreSQL's ChooseRelationName uses SnapshotDirty: a
							// private competing name is skipped, not waited upon.
							generated = await()
							fixture.DeleteReferenceCount(t, generated.Result, generated.Err, 0)
							runC(fixture.Select("SELECT tablename,indexname FROM pg_indexes WHERE schemaname='public' AND indexname='second_n_idx1'", []string{"tablename", "indexname"}, []string{"NAME", "NAME"}, [][]any{{"second", "second_n_idx1"}}))
							runA(transactionalIndexName("second_n_idx1", false))
						} else {
							fixture.RelationLockPending(t, p, b, cp, ap, 0, "", done)
						}
						// Unrelated committed rows/catalog must survive the first publisher.
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						check := transactionalIndexRun(t, p, b, peer)
						check(fixture.Exec("CREATE TABLE unrelated(n INT)", 0))
						check(fixture.Exec("INSERT INTO unrelated VALUES(9)", 1))
						check(fixture.Exec("CREATE INDEX unrelated_idx ON unrelated(n)", 0))
						if commit {
							runA(fixture.Exec("COMMIT", 0))
						} else {
							runA(fixture.Exec("ROLLBACK", 0))
						}
						o := generated
						if kind != "generated" {
							o = await()
						}
						if commit && kind != "generated" {
							if o.Result != nil {
								t.Fatal("raced failure returned Result")
							}
							d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "pg_class_relname_nsp_index"`)
							d.Detail = fmt.Sprintf("Key (relname, relnamespace)=(second_n_idx, %d) already exists.", namespaceID)
							d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_class", "pg_class_relname_nsp_index"
							fixture.AssertDiagnostic(t, o.Err, &d)
							if len(notices) != 0 {
								t.Fatal("failed raced creation emitted notice", notices)
							}
							runC(fixture.Exec("ROLLBACK", 0))
							check(fixture.Select("SELECT tablename,indexname FROM pg_indexes WHERE schemaname='public' AND indexname='second_n_idx'", []string{"tablename", "indexname"}, []string{"NAME", "NAME"}, [][]any{{"first", "second_n_idx"}}))
							if kind == "conditional" {
								// A preexisting committed name is a genuine conditional
								// skip; it is distinct from the post-capture race.
								runC(fixture.Exec("BEGIN", 0))
								runC(fixture.Exec(q, 0))
								want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "second_n_idx" already exists, skipping`}}
								if !reflect.DeepEqual(notices, want) {
									t.Fatalf("notices: %#v want %#v", notices, want)
								}
								runC(fixture.Exec("COMMIT", 0))
							}
						} else {
							fixture.DeleteReferenceCount(t, o.Result, o.Err, 0)
							var want []fixture.Diagnostic
							if !reflect.DeepEqual(notices, want) {
								t.Fatalf("notices: %#v want %#v", notices, want)
							}
							runC(fixture.Exec("COMMIT", 0))
							name := "second_n_idx"
							if kind == "generated" {
								name = "second_n_idx1"
							}
							parent := "second"
							check(fixture.Select("SELECT tablename,indexname FROM pg_indexes WHERE schemaname='public' AND indexname=$1", []string{"tablename", "indexname"}, []string{"NAME", "NAME"}, [][]any{{parent, name}}, name))
						}
						check(fixture.Select("SELECT n FROM unrelated", []string{"n"}, []string{"INT4"}, [][]any{{int64(9)}}))
						check(transactionalIndexName("unrelated_idx", true))
					})
				}
			})
		}
	}
}
