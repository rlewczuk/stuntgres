package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDomainCheckNativeControls(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{
				`CREATE DOMAIN domain_s."Parent Value" AS integer CONSTRAINT z_parent CHECK (VALUE>0) CHECK (VALUE<100)`,
				`CREATE DOMAIN domain_s.child AS domain_s."Parent Value" CONSTRAINT a_child CHECK (VALUE>10) CHECK (VALUE<90)`,
				"CREATE TABLE domain_s.stored (id integer,v domain_s.child DEFAULT 11::domain_s.child)",
				"CREATE VIEW domain_s.stored_view AS SELECT id,(v+1)::domain_s.child AS v FROM domain_s.stored",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.stored VALUES (1,11),(2,NULL)", 2))
			data := fixture.Select("SELECT id,v FROM domain_s.stored ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(11)}, {int64(2), nil}})
			data.CheckMetadata = func(t *testing.T, rows *sql.Rows) { domainReferenceCheckMetadata(t, rows, b.PG, false, data.Types) }
			check := func(t *testing.T, executor fixture.Executor, recorder *fixture.Recorder) {
				t.Helper()
				for _, prepared := range []bool{false, true} {
					s := data
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, executor, recorder, s)
					s = fixture.Select("SELECT id,v FROM domain_s.stored_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(12)}, {int64(2), nil}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, executor, recorder, s)
					s = fixture.Select("SELECT 11::domain_s.child AS v,NULL::domain_s.child AS n FROM public.one", []string{"v", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(11), nil}})
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, executor, recorder, s)
					for _, tc := range []struct{ value, constraint string }{{"0", "z_parent"}, {"5", "a_child"}, {"95", "child_check"}, {"101", "Parent Value_check"}} {
						t.Run(tc.value+map[bool]string{false: "_direct", true: "_prepared"}[prepared], func(t *testing.T) {
							fixture.RunStep(t, p.Ctx, executor, recorder, domainCheckFailure("SELECT "+tc.value+"::domain_s.child AS v FROM public.one", "child", tc.constraint, true, prepared))
						})
					}
				}
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec("INSERT INTO domain_s.stored(id) VALUES (3)", 1))
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select("SELECT v FROM domain_s.stored WHERE id=3", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Exec("DELETE FROM domain_s.stored WHERE id=3", 1))
			}
			check(t, c, r)
			// PostgreSQL supplies ordinary SQL witnesses, not a fictitious control API.
			if b.PG {
				return
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{`CONSTRAINT "Parent Value_check"`, `CONSTRAINT child_check`} {
				if !strings.Contains(dump.String(), name) {
					t.Errorf("dump does not explicitly preserve generated name %s", name)
				}
			}
			check(t, c, r)
			retained, err := b.DB.QueryContext(p.Ctx, data.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			domainReferenceCheckMetadata(t, retained, false, false, data.Types)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			check(t, c, r)
			domainReferenceCheckMetadata(t, retained, false, false, data.Types)
			for _, want := range []struct {
				id    int64
				value sql.NullInt64
			}{{1, sql.NullInt64{Int64: 11, Valid: true}}, {2, sql.NullInt64{}}} {
				if !retained.Next() {
					t.Fatalf("retained row missing: %v", retained.Err())
				}
				var id int64
				var value sql.NullInt64
				if err := retained.Scan(&id, &value); err != nil {
					t.Fatal(err)
				}
				if id != want.id || value != want.value {
					t.Fatalf("retained row=%d,%+v want=%+v", id, value, want)
				}
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatalf("retained trailing rows/error: %v", retained.Err())
			}
			if err := retained.Close(); err != nil {
				t.Fatal(err)
			}
			target := fixture.NewFixture(t, false, true)
			// Occupy unrelated identities before target replacement/remapping.
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE DOMAIN spacer AS text", 0))
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(dump.String())); err != nil {
				t.Fatal(err)
			}
			check(t, target.Memory, target.MemoryRecord)
			var second bytes.Buffer
			if err := stuntgres.Dump(target.Ctx, target.Native, &second); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(second.String())); err != nil {
				t.Fatal(err)
			}
			check(t, target.Memory, target.MemoryRecord)
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("CREATE TABLE sentinel (v integer)", 0))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec("INSERT INTO sentinel VALUES (42)", 1))
			bad := "CREATE SCHEMA staged; CREATE DOMAIN staged.d AS integer CONSTRAINT valid CHECK (VALUE>0); CREATE TABLE staged.t (v staged.d); INSERT INTO staged.t VALUES (0);"
			fixture.State(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), "23514")
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Error("SELECT 1::staged.d FROM sentinel", "3F000"))
			ctx, cancel := context.WithCancel(target.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, target.Native, strings.NewReader(dump.String())); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled Restore=%v", err)
			}
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
			check(t, target.Memory, target.MemoryRecord)
			check(t, c, r)
		})
	})
	t.Run("check_owned_dependency", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.provider AS integer",
				"CREATE DOMAIN domain_s.dependent AS integer CONSTRAINT provider_check CHECK (VALUE::domain_s.provider>0)",
				"CREATE DOMAIN domain_s.descendant AS domain_s.dependent CHECK (VALUE<100)",
				"CREATE TABLE domain_s.survivor (id integer,v domain_s.dependent,c domain_s.descendant)",
				"CREATE VIEW domain_s.survivor_view AS SELECT id,(v-1)::domain_s.dependent AS v FROM domain_s.survivor",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.survivor VALUES (1,1,1)", 1))
			want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop type domain_s.provider because other objects depend on it")
			want.Detail = "constraint provider_check depends on type domain_s.provider"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			t.Run("restrict", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("DROP DOMAIN domain_s.provider RESTRICT", want))
			})
			t.Run("retained_check", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 0::domain_s.dependent AS v FROM public.one", "dependent", "provider_check", true, false))
			})
			run(fixture.Select("SELECT id,v,c FROM domain_s.survivor", []string{"id", "v", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}}))
			*notices = nil
			run(fixture.Exec("DROP DOMAIN domain_s.provider CASCADE", 0))
			t.Run("notice", func(t *testing.T) {
				fixture.DomainReferenceNotice(t, notices, "drop cascades to constraint provider_check", "")
			})
			run(fixture.Exec("INSERT INTO domain_s.survivor VALUES (2,0,0)", 1))
			run(fixture.Select("SELECT id,v,c FROM domain_s.survivor ORDER BY id", []string{"id", "v", "c"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(2), int64(0), int64(0)}}))
			t.Run("own_child_check", func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, c, r, domainCheckFailure("SELECT 101::domain_s.descendant AS v FROM public.one", "descendant", "descendant_check", true, false))
			})
			run(fixture.Select("SELECT 0::domain_s.dependent AS v,0::domain_s.descendant AS c FROM public.one", []string{"v", "c"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(0)}}))
			run(fixture.Select("SELECT id,v FROM domain_s.survivor_view ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(-1)}}))
			run(fixture.Select("SELECT ('{0,NULL}'::domain_s.descendant[])[1] AS v,('{0,NULL}'::domain_s.descendant[])[2] AS n FROM public.one", []string{"v", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(0), nil}}))
		})
	})
	t.Run("base_and_check_dependency", func(t *testing.T) {
		domainReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
			for _, q := range []string{
				"CREATE DOMAIN domain_s.provider AS integer CHECK (VALUE>0)",
				"CREATE DOMAIN domain_s.basing AS domain_s.provider CHECK (VALUE::domain_s.provider>0)",
				"CREATE TABLE domain_s.base_survivor (id integer,v domain_s.basing)",
			} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO domain_s.base_survivor VALUES (7,1)", 1))
			*notices = nil
			run(fixture.Exec("DROP DOMAIN domain_s.provider CASCADE", 0))
			t.Run("notice", func(t *testing.T) {
				fixture.DomainReferenceNotice(t, notices, "drop cascades to 2 other objects", "drop cascades to type domain_s.basing\ndrop cascades to column v of table domain_s.base_survivor")
			})
			run(fixture.Select("SELECT * FROM domain_s.base_survivor", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.Error("SELECT NULL::domain_s.basing AS v FROM public.one", "42704"))
		})
	})
}
