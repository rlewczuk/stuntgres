package reference_test

import (
	"context"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegclassFeatureSmoke(t *testing.T) {
	t.Run("relations", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "rr_smoke_a", "rr_smoke_b")
		p := fixture.NewScenario(t)
		for _, backend := range p.Backends() {
			t.Run(backend.Name, func(t *testing.T) {
				notices := []fixture.Diagnostic{}
				_, c, recorder := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
				ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, recorder)
				var err error
				run := func(s fixture.Step) { fixture.RunStep(t, ctx, c, recorder, s) }
				for _, q := range []string{"CREATE SCHEMA rr_smoke_a", "CREATE SCHEMA rr_smoke_b", `CREATE TABLE rr_smoke_a."Quoted.Target"(v INT)`, `CREATE TABLE rr_smoke_b."Quoted.Target"(v INT)`, `SET search_path=rr_smoke_a,rr_smoke_b,public`, `CREATE TABLE public.rr_smoke_store(id INT,r regclass DEFAULT 'rr_smoke_a."Quoted.Target"',a regclass[])`, `CREATE VIEW public.rr_smoke_view AS SELECT 'rr_smoke_a."Quoted.Target"'::regclass AS r`} {
					run(fixture.Exec(q, 0))
				}
				var aID, bID int64
				if err = c.QueryRowContext(ctx, `SELECT 'rr_smoke_a."Quoted.Target"'::regclass::oid::bigint,'rr_smoke_b."Quoted.Target"'::regclass::oid::bigint`).Scan(&aID, &bID); err != nil || aID == bID {
					t.Fatal(aID, bID, err)
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.Select(`SELECT '"Quoted.Target"'::regclass AS r,'"Quoted.Target"'::regclass::oid::bigint AS id`, []string{"r", "id"}, []string{"REGCLASS", "INT8"}, [][]any{{`"Quoted.Target"`, aID}})
					s.Prepared = prepared
					run(s)
				}
				stmt, err := c.PrepareContext(ctx, "SELECT $1::regclass AS r")
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: "SELECT $1::regclass AS r"}
				for _, input := range []string{`"Quoted.Target"`, strconv.FormatInt(aID, 10)} {
					s := fixture.RegclassRows(held.SQL, "r", `"Quoted.Target"`)
					s.Args = []any{input}
					fixture.RunStep(t, ctx, held, recorder, s)
				}
				s := fixture.Exec(`INSERT INTO public.rr_smoke_store(id,a) VALUES(1,ARRAY[$1,NULL]::regclass[])`, 1, `rr_smoke_a."Quoted.Target"`)
				s.Prepared = true
				run(s)
				run(fixture.Exec("INSERT INTO public.rr_smoke_store(id,r,a) VALUES(2,NULL,ARRAY[]::regclass[])", 1))
				run(fixture.Select("SELECT id,r,a FROM public.rr_smoke_store ORDER BY id", []string{"id", "r", "a"}, []string{"INT4", "REGCLASS", "_REGCLASS"}, [][]any{{int64(1), `"Quoted.Target"`, `{"\"Quoted.Target\"",NULL}`}, {int64(2), nil, "{}"}}))
				run(fixture.Exec("CREATE TABLE public.rr_smoke_error(n INT,r regclass,a regclass[],CONSTRAINT rr_smoke_positive CHECK(n>0))", 0))
				run(fixture.Exec("INSERT INTO rr_smoke_error VALUES(1,0::regclass,'{0,NULL}')", 1))
				failure := fixture.ImplicitError("UPDATE rr_smoke_error SET n=0 RETURNING r,a", fixture.CheckViolation("rr_smoke_error", "rr_smoke_positive", "0, -, {-,NULL}"))
				failure.SelectRows, failure.Prepared = true, true
				failure.ErrorStage = "execute"
				run(failure)
				run(fixture.Select("SELECT n,r,a FROM rr_smoke_error", []string{"n", "r", "a"}, []string{"INT4", "REGCLASS", "_REGCLASS"}, [][]any{{int64(1), "-", "{-,NULL}"}}))
				for _, q := range []string{`ALTER TABLE rr_smoke_a."Quoted.Target" RENAME TO "Moved.Target"`, `ALTER TABLE rr_smoke_a."Moved.Target" SET SCHEMA rr_smoke_b`, `SET search_path=public`} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Select(`SELECT r::oid='rr_smoke_b."Moved.Target"'::regclass::oid AS same,a IS NULL AS array_null FROM rr_smoke_store WHERE id=1`, []string{"same", "array_null"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}}))
				run(fixture.RegclassRows("SELECT r FROM rr_smoke_view", "r", `rr_smoke_b."Moved.Target"`))
				run(fixture.Exec("INSERT INTO rr_smoke_store(id) VALUES(3)", 1))
				run(fixture.RegclassRows("SELECT r FROM rr_smoke_store WHERE id=3", "r", `rr_smoke_b."Moved.Target"`))
				run(regclassError("SELECT 'a..b'::regclass", "42602", "invalid name syntax", "8", ""))
				run(regclassError("SELECT 'rr_smoke_missing'::regclass", "42P01", `relation "rr_smoke_missing" does not exist`, "8", ""))
				detail := `default value for column r of table rr_smoke_store depends on table rr_smoke_b."Moved.Target"` + "\n" + `view rr_smoke_view depends on table rr_smoke_b."Moved.Target"`
				run(fixture.ErrorDiagnostic(`DROP TABLE rr_smoke_b."Moved.Target"`, regclassDropDiagnostic(`rr_smoke_b."Moved.Target"`, detail)))
				run(fixture.Select("SELECT count(*) AS n FROM rr_smoke_store", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
				notices = nil
				run(fixture.Exec(`DROP TABLE rr_smoke_b."Moved.Target" CASCADE`, 0))
				regclassNotice(t, notices, "drop cascades to 2 other objects", "drop cascades to default value for column r of table rr_smoke_store\ndrop cascades to view rr_smoke_view")
				run(fixture.Exec("INSERT INTO rr_smoke_store(id) VALUES(4)", 1))
				decimal := strconv.FormatInt(aID, 10)
				run(fixture.Select("SELECT id,r FROM rr_smoke_store ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(1), decimal}, {int64(2), nil}, {int64(3), decimal}, {int64(4), nil}}))
				run(regclassError("SELECT r FROM rr_smoke_view", "42P01", `relation "rr_smoke_view" does not exist`, "15", ""))
			})
		}
	})
	t.Run("logical_controls", regclassNativeControlScenario)
	t.Run("mixed_default_replay", TestReferenceRegclassMixedDefaultReplay)
	t.Run("cascaded_view_dependencies", TestReferenceRegclassCascadedViewDependencies)
	t.Run("drop_column_dependencies", TestReferenceRegclassDropColumnDependencies)
	t.Run("opaque_inline_default_replay", TestReferenceRegclassOpaqueInlineDefaultReplay)
}
