package reference_test

import (
	"context"
	"database/sql"
	"slices"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainAlterConstraintMessages(t *testing.T) {
	for _, kind := range []string{"private_create", "private_create_failure", "private_container_failure", "private_invalid_assignment", "failed_prefix", "own_insert", "own_update", "own_delete", "later_container", "removed_container", "add_drop", "multiple_alters", "later_error", "cancel", "promoted_rollback", "query_results", "no_truth_replay", "index_outputs"} {
		t.Run(kind, func(t *testing.T) {
			domainAlterScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
				if kind == "private_create" {
					q := "CREATE DOMAIN domain_s.d AS integer; CREATE TABLE domain_s.t(v domain_s.d); INSERT INTO domain_s.t VALUES(-1); ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID; UPDATE domain_s.t SET v=1; ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive"
					run(fixture.Exec(q, 0))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					return
				}
				if kind == "private_create_failure" || kind == "private_container_failure" || kind == "private_invalid_assignment" {
					prefix := "CREATE DOMAIN domain_s.d AS integer; CREATE DOMAIN domain_s.child AS domain_s.d; "
					var failure fixture.Step
					if kind == "private_container_failure" {
						failure = domainAlterContainerFailure(prefix+"CREATE TABLE domain_s.t(v domain_s.child[]); ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)", "child", "t", "v")
					} else if kind == "private_invalid_assignment" {
						failure = domainCheckFailure(prefix+"CREATE TABLE domain_s.t(v domain_s.child); INSERT INTO domain_s.t VALUES(-1); ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0) NOT VALID; INSERT INTO domain_s.t VALUES(-2)", "child", "positive", false, false)
					} else {
						failure = domainAlterOldFailure(prefix+"CREATE TABLE domain_s.t(v domain_s.child); INSERT INTO domain_s.t VALUES(-1); ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)", "t", "v")
					}
					t.Run("failure", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, failure) })
					for _, name := range []string{"d", "child"} {
						t.Run("absent_"+name, func(t *testing.T) {
							q := "SELECT 1::domain_s." + name + " FROM public.one"
							want := fixture.ControlsReferenceDiagnostic("42704", `type "domain_s.`+name+`" does not exist`)
							want.Position = "11"
							s := fixture.ErrorDiagnostic(q, want)
							s.SelectRows, s.ErrorStage = true, "execute"
							run(s)
						})
					}
					t.Run("absent_table", func(t *testing.T) {
						want := fixture.ControlsReferenceDiagnostic("42P01", `relation "domain_s.t" does not exist`)
						want.Position = "15"
						s := fixture.ErrorDiagnostic("SELECT v FROM domain_s.t", want)
						s.SelectRows, s.ErrorStage = true, "execute"
						run(s)
					})
					return
				}
				run(fixture.Exec("CREATE DOMAIN domain_s.d AS integer", 0))
				run(fixture.Exec("CREATE TABLE domain_s.t(v domain_s.d)", 0))
				seed := 1
				if kind == "failed_prefix" || kind == "own_update" || kind == "own_delete" || kind == "later_error" {
					seed = -1
				}
				run(fixture.Exec("INSERT INTO domain_s.t VALUES($1)", 1, seed))
				add := "ALTER DOMAIN domain_s.d ADD CONSTRAINT positive CHECK(VALUE>0)"
				validate := "ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT positive"
				switch kind {
				case "failed_prefix":
					t.Run("failure", func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure(add+" NOT VALID; "+validate, "t", "v"))
					})
					run(domainAlterFlags("d", [][]any{}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
					run(fixture.Exec(add+" NOT VALID", 0))
				case "own_insert":
					t.Run("failure", func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, r, domainAlterOldFailure("INSERT INTO domain_s.t VALUES(-2); "+add, "t", "v"))
					})
					run(domainAlterFlags("d", [][]any{}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				case "own_update":
					run(fixture.Exec(add+" NOT VALID", 0))
					run(fixture.Exec("UPDATE domain_s.t SET v=2; "+validate, 0))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
				case "own_delete":
					run(fixture.Exec(add+" NOT VALID", 0))
					run(fixture.Exec("DELETE FROM domain_s.t; "+validate, 0))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{}))
				case "later_container":
					run(fixture.Exec(add+"; CREATE TABLE domain_s.holder(v domain_s.d[])", 0))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
					t.Run("repeated_validate", func(t *testing.T) {
						fixture.RunStep(t, p.Ctx, c, r, domainAlterContainerFailure(validate, "d", "holder", "v"))
					})
				case "removed_container":
					run(fixture.Exec("CREATE TABLE domain_s.holder(v domain_s.d[])", 0))
					run(fixture.Exec("DROP TABLE domain_s.holder; "+add, 0))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
				case "add_drop":
					run(fixture.Exec(add+"; ALTER DOMAIN domain_s.d DROP CONSTRAINT positive; INSERT INTO domain_s.t VALUES(-2)", 1))
					run(domainAlterFlags("d", [][]any{}))
					run(fixture.Select("SELECT v FROM domain_s.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(-2)}, {int64(1)}}))
				case "no_truth_replay":
					run(fixture.Exec("CREATE SEQUENCE domain_s.calls", 0))
					run(fixture.Exec("ALTER DOMAIN domain_s.d ADD CONSTRAINT counted CHECK(nextval('domain_s.calls')>0) NOT VALID; ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT counted; INSERT INTO domain_s.t VALUES(2)", 1))
					run(fixture.Select("SELECT last_value,is_called FROM domain_s.calls", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(2), true}}))
					run(domainAlterFlags("d", [][]any{{"counted", true}}))
				case "multiple_alters":
					run(fixture.Exec(add+" NOT VALID; ALTER DOMAIN domain_s.d ADD CONSTRAINT upper_bound CHECK(VALUE<10); ALTER DOMAIN domain_s.d VALIDATE CONSTRAINT upper_bound; ALTER DOMAIN domain_s.d DROP CONSTRAINT upper_bound; "+validate, 0))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
				case "later_error":
					q := add + " NOT VALID; UPDATE domain_s.t SET v=1; " + validate + "; SELECT missing"
					want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
					want.Position = fixture.Position(strings.LastIndex(q, "missing") + 1)
					t.Run("failure", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic(q, want)) })
					run(domainAlterFlags("d", [][]any{}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(-1)}}))
				case "cancel":
					q := add + " NOT VALID; SELECT pg_sleep(600); " + validate
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					fixture.SleepActiveCancel(t, p, b, c, q, func(ctx context.Context) {
						fixture.RunStep(t, ctx, peer, b.Recorder, domainAlterFlags("d", [][]any{}))
					}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, q, false) })
					run(domainAlterFlags("d", [][]any{}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				case "promoted_rollback":
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					run(fixture.Exec(add+" NOT VALID; INSERT INTO domain_s.t VALUES(2); BEGIN; SAVEPOINT keep_prefix", 0))
					run(domainAlterFlags("d", [][]any{{"positive", false}}))
					peer, err := b.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer peer.Close()
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, domainAlterFlags("d", [][]any{}))
					fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
					want.Position = "8"
					t.Run("recoverable_failure", func(t *testing.T) { fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("SELECT missing", want)) })
					run(fixture.Exec("ROLLBACK TO keep_prefix", 0))
					run(domainAlterFlags("d", [][]any{{"positive", false}}))
					run(fixture.Select("SELECT v FROM domain_s.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
					run(fixture.Exec("ROLLBACK", 0))
					run(domainAlterFlags("d", [][]any{}))
					run(fixture.Select("SELECT v FROM domain_s.t", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
				case "query_results":
					q := add + " NOT VALID; SELECT convalidated AS valid FROM pg_constraint WHERE contypid='domain_s.d'::regtype::oid; UPDATE domain_s.t SET v=2; " + validate + "; SELECT v FROM domain_s.t"
					rows, err := c.QueryContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					for i, want := range []any{false, int64(2)} {
						if i > 0 && !rows.NextResultSet() {
							t.Fatal("missing message result set", rows.Err())
						}
						name, typ := "valid", "BOOL"
						if i == 1 {
							name, typ = "v", "INT4"
						}
						names, err := rows.Columns()
						if err != nil || !slices.Equal(names, []string{name}) {
							t.Fatal(names, err)
						}
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != typ {
							t.Fatal(types, err)
						}
						if !rows.Next() {
							t.Fatal("missing message row", rows.Err())
						}
						var value any
						if err := rows.Scan(&value); err != nil {
							t.Fatal(err)
						}
						fixture.AssertRawValue(t, b.PG, typ, value)
						if fixture.Normalize(value) != want {
							t.Fatalf("message cell %v want %v", value, want)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("extra message row/error", rows.Err())
						}
					}
					if rows.NextResultSet() || rows.Err() != nil {
						t.Fatal("extra message result/error", rows.Err())
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
				case "index_outputs":
					run(fixture.Exec("CREATE INDEX message_idx ON domain_s.t(((v::domain_s.d)::integer))", 0))
					run(fixture.Exec("INSERT INTO domain_s.t VALUES(2); "+add+" NOT VALID; "+validate+"; INSERT INTO domain_s.t VALUES(3)", 1))
					run(domainAlterFlags("d", [][]any{{"positive", true}}))
					run(fixture.Select("SELECT v FROM domain_s.t ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
				}
			})
		})
	}
}
