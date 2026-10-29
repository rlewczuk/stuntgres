package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

var rewriteTriggerNames = []string{"et_rw", "et_rw_guard", "et_rw_disabled", "et_rw_end"}

type rewriteCase struct {
	t *testing.T
	p *fixture.Pair
	b fixture.Backend
	c *sql.Conn
}

func rewriteScenario(t *testing.T, body func(rewriteCase)) {
	t.Helper()
	eventTriggerScenario(t, rewriteTriggerNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		body(rewriteCase{t, p, b, c})
	})
}

func (d rewriteCase) run(s fixture.Step) { d.t.Helper(); fixture.RunStep(d.t, d.p.Ctx, d.c, d.b.Recorder, s) }
func (d rewriteCase) exec(queries ...string) {
	d.t.Helper()
	for _, q := range queries { d.run(fixture.Exec(q, 0)) }
}
func (d rewriteCase) alter(q string, prepared bool) {
	d.t.Helper()
	s := fixture.Exec(q, 0)
	s.Prepared = prepared
	d.run(s)
}
func (d rewriteCase) setup() {
	d.t.Helper()
	d.exec(
		"CREATE TABLE public.et_rw_audit(target oid,reason integer,tag text,same_oid boolean,same_reason boolean)",
		"CREATE FUNCTION public.et_rw_read() RETURNS oid LANGUAGE SQL AS $$SELECT pg_event_trigger_table_rewrite_oid()$$",
		"CREATE FUNCTION public.et_rw_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO public.et_rw_audit SELECT pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason(),TG_TAG,public.et_rw_read()=pg_event_trigger_table_rewrite_oid(),pg_event_trigger_table_rewrite_reason()=pg_event_trigger_table_rewrite_reason(); END$$",
	)
	eventTriggerCreate(d.t, d.p, d.b, d.c, "et_rw", "table_rewrite", "'ALTER TABLE','ALTER TYPE'", "public.et_rw_fn")
}
func (d rewriteCase) audit(table string, reason int64, tag string) {
	d.t.Helper()
	rows := [][]any{}
	if reason != 0 { rows = [][]any{{true, reason, tag, true, true}} }
	s := fixture.Select("SELECT target='"+table+"'::regclass AS correct_target,reason,tag,same_oid,same_reason FROM public.et_rw_audit", []string{"correct_target", "reason", "tag", "same_oid", "same_reason"}, []string{"BOOL", "INT4", "TEXT", "BOOL", "BOOL"}, rows)
	d.run(s)
	d.run(fixture.Prepared(s))
}
func rewriteProtocol(name string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("39P03", name+"() can only be called in a table_rewrite event trigger function")
}
func (d rewriteCase) outside() {
	d.t.Helper()
	for _, name := range []string{"pg_event_trigger_table_rewrite_oid", "pg_event_trigger_table_rewrite_reason"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT "+name+"()", rewriteProtocol(name))
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			d.run(s)
		}
	}
}
