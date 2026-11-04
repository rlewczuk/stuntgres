package reference_test

import (
	"database/sql"
	"slices"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func sepgsqlInitializationAttachments(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, audit *sepgsqlInitializationAudit) {
	run := func(step fixture.Step) {
		defer func() {
			if t.Failed() {
				t.Logf("sepgsql attachment statement: %s", step.Query)
			}
		}()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
	}
	admin := func() { run(sepgsqlBool("SELECT sepgsql_setcon(NULL) AS changed", "changed", true)) }
	for _, query := range []string{
		"CREATE FUNCTION init_default() RETURNS text LANGUAGE sql VOLATILE AS 'SELECT sepgsql_getcon()'",
		"CREATE FUNCTION init_domain(v text) RETURNS boolean LANGUAGE sql VOLATILE AS 'SELECT v = sepgsql_getcon()'",
		"CREATE DOMAIN init_guarded AS text CONSTRAINT init_subject CHECK(init_domain(VALUE))",
		"CREATE TABLE init_attached(id integer,v init_guarded DEFAULT init_default())",
		// PostgreSQL permits the declaration's author to assert IMMUTABLE;
		// these real user routines exercise existing index expression owners.
		// This is not a claim that getcon itself is immutable or index-safe.
		"CREATE FUNCTION init_key(v text) RETURNS text LANGUAGE sql IMMUTABLE STRICT AS $$SELECT v || '/' || sepgsql_getcon()$$",
		"CREATE FUNCTION init_pred(v text) RETURNS boolean LANGUAGE sql IMMUTABLE STRICT AS 'SELECT v = sepgsql_getcon()'",
		"CREATE INDEX init_attachment_index ON init_attached(init_key(v::text)) WHERE init_pred(v::text)",
		"CREATE FUNCTION init_when(v text) RETURNS boolean LANGUAGE sql VOLATILE AS 'SELECT v = sepgsql_getcon()'",
		"CREATE FUNCTION init_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v := sepgsql_getcon(); RETURN NEW; END$$",
		"CREATE TRIGGER init_before BEFORE INSERT ON init_attached FOR EACH ROW WHEN(init_when(NEW.v::text)) EXECUTE FUNCTION init_trigger()",
		"CREATE FUNCTION init_cursor(v text) RETURNS text LANGUAGE sql VOLATILE AS $$SELECT v || '/' || sepgsql_getcon()$$",
	} {
		run(fixture.Exec(query, 0))
	}
	for _, signature := range []string{"init_default()", "init_domain(text)", "init_key(text)", "init_pred(text)", "init_when(text)", "init_trigger()", "init_cursor(text)"} {
		sepgsqlInitializationLabel(run, signature, sepgsqlS)
	}
	const insert = "INSERT INTO init_attached(id) VALUES($1) RETURNING id,v::text AS value"
	held := fixture.IdentityHeld(t, p.Ctx, c, insert)
	for id := int64(1); id <= 3; id++ {
		run(sepgsqlSet(sepgsqlR))
		audit.reset()
		step := fixture.Select(insert, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{id, sepgsqlT}}, id)
		if id == 1 {
			run(step)
		} else if id == 2 {
			run(fixture.Prepared(step))
		} else {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
		}
		sepgsqlInitializationEntered(t, audit, "init_default", "init_domain", "init_pred", "init_key", "init_when", "init_trigger")
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
	}
	for i, deniedSource := range []struct{ signature, label, permission string }{
		{"init_default()", sepgsqlInitDenied, "execute"},
		{"init_domain(text)", sepgsqlInitDenied, "execute"},
		{"init_pred(text)", sepgsqlInitDenied, "execute"},
		{"init_key(text)", sepgsqlInitDenied, "execute"},
		{"init_when(text)", sepgsqlInitDenied, "execute"},
		// Direct trigger dispatch has no executor execute hook. Relabeling
		// to a label with no transition would run its body as R, producing
		// an ordinary domain failure. InitStrict instead selects the genuine
		// R->T trusted transition whose entrypoint permission R lacks.
		{"init_trigger()", sepgsqlInitStrict, "entrypoint"},
	} {
		signature := deniedSource.signature
		sepgsqlInitializationLabel(run, signature, deniedSource.label)
		run(sepgsqlSet(sepgsqlR))
		audit.reset()
		denied := sepgsqlDenied(insert)
		denied.SelectRows, denied.Args = true, []any{int64(10 + i)}
		// Same retained INSERT is reexecuted across actual default/domain,
		// index predicate/key and WHEN/dispatch constructor lifetimes.
		func() {
			defer func() {
				if t.Failed() {
					t.Logf("sepgsql attachment denied source: %s (%s)", signature, deniedSource.permission)
				}
			}()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		}()
		name, _, _ := strings.Cut(signature, "(")
		found := false
		for _, event := range audit.snapshot() {
			if event.Subject == sepgsqlR && event.Target == deniedSource.label && event.Class == "db_procedure" &&
				strings.HasSuffix(event.Name, "."+name) && slices.Contains(event.Required, deniedSource.permission) &&
				slices.Contains(event.Denied, deniedSource.permission) {
				found = true
			}
			if deniedSource.permission == "entrypoint" && event.Class == "db_procedure" {
				if strings.HasSuffix(event.Name, "."+name) && slices.Contains(event.Required, "execute") {
					t.Fatal("direct trigger acquired a fictional executor execute hook")
				}
				if event.Subject == sepgsqlR && strings.HasSuffix(event.Name, ".sepgsql_getcon") {
					t.Fatal("denied trigger initialized its body under the caller")
				}
			}
		}
		if !found {
			t.Fatalf("missing actual %s denial for %s", deniedSource.permission, signature)
		}
		run(sepgsqlCount("SELECT count(*) FROM init_attached", int64(3+i)))
		run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
		admin()
		sepgsqlInitializationLabel(run, signature, sepgsqlS)
		run(sepgsqlSet(sepgsqlR))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(insert, []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(10 + i), sepgsqlT}}, int64(10+i)))
		admin()
	}
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlCount("SELECT count(*) FROM init_attached", 9))
	const declaration = "DECLARE init_cursor_live NO SCROLL CURSOR FOR SELECT id,init_cursor(v::text) AS value FROM init_attached WHERE id<=3 ORDER BY id"
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec(declaration, 0))
	run(fixture.Select("FETCH FORWARD 1 FROM init_cursor_live", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), sepgsqlT + "/" + sepgsqlT}}))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	run(fixture.Select("FETCH FORWARD 1 FROM init_cursor_live", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), sepgsqlT + "/" + sepgsqlT}}))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	admin()
	sepgsqlInitializationLabel(run, "init_cursor(text)", sepgsqlInitDenied)
	run(sepgsqlSet(sepgsqlR))
	run(sepgsqlDenied("FETCH FORWARD 1 FROM init_cursor_live"))
	run(fixture.Exec("ROLLBACK", 0))
	// Rollback also restores the label, but cannot retain the dead FETCH's
	// borrowed owner in a later cursor attachment.
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec(declaration, 0))
	run(fixture.Select("FETCH ALL FROM init_cursor_live", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), sepgsqlT + "/" + sepgsqlT}, {int64(2), sepgsqlT + "/" + sepgsqlT}, {int64(3), sepgsqlT + "/" + sepgsqlT}}))
	run(fixture.Exec("CLOSE init_cursor_live", 0))
	run(fixture.Exec("COMMIT", 0))

	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE init_cursor_nested NO SCROLL CURSOR FOR WITH init_cursor_rows AS MATERIALIZED (SELECT id,init_cursor(v::text) AS value FROM init_attached WHERE id<=3 ORDER BY id) SELECT id,value FROM init_cursor_rows", 0))
	run(fixture.Select("FETCH FORWARD 1 FROM init_cursor_nested", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), sepgsqlT + "/" + sepgsqlT}}))
	run(fixture.Select("FETCH ALL FROM init_cursor_nested", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), sepgsqlT + "/" + sepgsqlT}, {int64(3), sepgsqlT + "/" + sepgsqlT}}))
	run(fixture.Exec("CLOSE init_cursor_nested", 0))
	run(fixture.Exec("COMMIT", 0))

	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("DECLARE init_cursor_held NO SCROLL CURSOR WITH HOLD FOR SELECT id,init_cursor(v::text) AS value FROM init_attached WHERE id<=3 ORDER BY id", 0))
	run(fixture.Select("FETCH FORWARD 1 FROM init_cursor_held", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), sepgsqlT + "/" + sepgsqlT}}))
	run(fixture.Exec("COMMIT", 0))
	admin()
	sepgsqlInitializationLabel(run, "init_cursor(text)", sepgsqlInitDenied)
	run(sepgsqlSet(sepgsqlR))
	// Genuine hold promotion owns rows, not a live function/authorization
	// callback. Changing the function afterward cannot reexecute it here.
	run(fixture.Select("FETCH ALL FROM init_cursor_held", []string{"id", "value"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), sepgsqlT + "/" + sepgsqlT}, {int64(3), sepgsqlT + "/" + sepgsqlT}}))
	run(fixture.Exec("CLOSE init_cursor_held", 0))
	run(sepgsqlText("SELECT sepgsql_getcon() AS context", "context", sepgsqlR))
	admin()
	sepgsqlInitializationLabel(run, "init_cursor(text)", sepgsqlS)
}
