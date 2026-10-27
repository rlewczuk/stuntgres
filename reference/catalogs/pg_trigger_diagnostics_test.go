package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGTriggerDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct{ query, position string }{{"SELECT missing FROM pg_catalog.pg_trigger", "8"}, {"SELECT /*é*/ missing FROM pg_catalog.pg_trigger", "14"}} {
				for _, tail := range []string{"", " WHERE FALSE"} {
					for _, prepared := range []bool{false,true} {
						s := fixture.ErrorDiagnostic(tc.query+tail, fixture.Diagnostic{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42703", Message:`column "missing" does not exist`, Position:tc.position})
						s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
						if prepared { s.ErrorStage = "prepare" }
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					}
				}
			}
			pgTriggerExec(t,p,b,b.DB,"CREATE TABLE pgt_expr(a integer)")
			pgTriggerExec(t,p,b,b.DB,"CREATE TABLE pgt_context(renamed integer)")
			pgTriggerExec(t,p,b,b.DB,"CREATE FUNCTION pgt_expr_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$")
			for _, tc := range []struct{name, condition string}{{"constant","true"},{"null_condition","NULL::boolean"},{"old","OLD.a > 0"},{"new","NEW.a > 0"},{"mixed","OLD.a < NEW.a"},{"absent",""}} {
				when := ""
				if tc.condition != "" { when = " WHEN ("+tc.condition+")" }
				pgTriggerExec(t,p,b,b.DB,"CREATE TRIGGER "+tc.name+" BEFORE UPDATE ON pgt_expr FOR EACH ROW"+when+" EXECUTE FUNCTION pgt_expr_fn()")
			}
			// The nonexistent relation is established, never inferred from an allocator.
			pgTriggerBoth(t,p,b,fixture.Select("SELECT oid FROM pg_class WHERE oid=4294967295",[]string{"oid"},[]string{"OID"},[][]any{}))
			for _, overload := range []string{"",",false",",true"} {
				for _, rel := range []string{"0","'pgt_expr'::regclass","'pgt_context'::regclass","4294967295::oid"} {
					query := fmt.Sprintf("SELECT pg_get_expr(tgqual,%s%s) AS expression FROM pg_trigger WHERE tgrelid='pgt_expr'::regclass AND tgname=$1::name",rel,overload)
					stmt, err := b.DB.PrepareContext(p.Ctx,query)
					if err != nil { t.Fatal("consumer must prepare before inspecting actual carrier",err) }
					held := fixture.HeldStatement{Executor:b.DB,Statement:stmt,SQL:query}
					for _, name := range []string{"constant","null_condition","old","new","mixed","absent"} {
						message := ""
						if name=="old" || name=="new" || name=="mixed" {
							if rel=="0" { message="expression contains variables" } else if name!="old" { message="expression contains variables of more than one relation" }
						}
						if message!="" {
							s := fixture.ErrorDiagnostic(query,fixture.Diagnostic{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:message},name)
							s.SelectRows,s.ErrorStage=true,"execute"
							fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
							fixture.RunStep(t,p.Ctx,held,b.Recorder,s)
							continue
						}
						var want any
						if name!="absent" && rel!="4294967295::oid" {
							switch name {
							case "constant": want="true"
							case "null_condition": want="NULL::boolean"
							case "old":
								label := "a"
								if rel=="'pgt_context'::regclass" { label="renamed" }
								want="("+label+" > 0)"
								if overload==",true" { want=label+" > 0" }
							}
						}
						s := fixture.Select(query,[]string{"expression"},[]string{"TEXT"},[][]any{{want}},name)
						fixture.PgDescriptionRows(t,p,b,s)
						fixture.PgDescriptionExecutorRows(t,p,b,held,s)
					}
					stmt.Close()
				}
			}
		})
	}
}
