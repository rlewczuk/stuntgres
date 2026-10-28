package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func pgTriggerNoEvaluation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, q := range []string{"CREATE TABLE pgt_eval(a integer)", "CREATE TABLE pgt_eval_log(a integer)", "CREATE FUNCTION pgt_eval_condition(v integer) RETURNS boolean LANGUAGE plpgsql VOLATILE AS $$BEGIN INSERT INTO pgt_eval_log VALUES(v); RETURN v>0; END$$", "CREATE FUNCTION pgt_eval_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER checked BEFORE UPDATE ON pgt_eval FOR EACH ROW WHEN(pgt_eval_condition(OLD.a)) EXECUTE FUNCTION pgt_eval_fn()"} {
				pgTriggerExec(t, p, b, b.DB, q)
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgt_eval VALUES(1)", 1))
			pgTriggerLive(t, p, b, b.DB, "pgt_eval", [][]any{{"checked", int64(19), "O", int64(0), "", "", true}})
			for range 2 {
				pgTriggerBoth(t, p, b, fixture.Select("SELECT pg_get_expr(tgqual,tgrelid) AS expression FROM pg_trigger WHERE tgrelid='pgt_eval'::regclass", []string{"expression"}, []string{"TEXT"}, [][]any{{"pgt_eval_condition(a)"}}))
				for _, prepared := range []bool{false, true} {
					query := "SELECT tgqual FROM pg_trigger WHERE tgrelid='pgt_eval'::regclass"
					var rows *sql.Rows
					var err error
					if prepared {
						stmt, e := b.DB.PrepareContext(p.Ctx, query)
						if e != nil {
							t.Fatal(e)
						}
						rows, err = stmt.QueryContext(p.Ctx)
						defer stmt.Close()
					} else {
						rows, err = b.DB.QueryContext(p.Ctx, query)
					}
					if err != nil {
						t.Fatal(err)
					}
					types, err := rows.ColumnTypes()
					if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "PG_NODE_TREE" {
						t.Fatal("typed opaque tree", types, err)
					}
					if !rows.Next() {
						t.Fatal("missing actual WHEN", rows.Err())
					}
					var raw string
					if err := rows.Scan(&raw); err != nil || raw == "" {
						t.Fatal("missing structural output", raw, err)
					}
					if rows.Next() || rows.Err() != nil {
						t.Fatal("extra/failed tree row", rows.Err())
					}
					rows.Close()
				}
			}
			pgTriggerBoth(t, p, b, fixture.Select("SELECT count(*) AS n FROM pgt_eval_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("UPDATE pgt_eval SET a=a", 1))
			pgTriggerBoth(t, p, b, fixture.Select("SELECT a FROM pgt_eval_log", []string{"a"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}
