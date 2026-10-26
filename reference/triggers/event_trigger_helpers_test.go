package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Database-global objects must be retired before the pair resets public, even
// when a callback deliberately prevents ordinary DDL from succeeding.
func eventTriggerCleanup(t *testing.T, db *sql.DB, names []string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, name := range names {
		q := `DROP EVENT TRIGGER IF EXISTS "` + strings.ReplaceAll(name, `"`, `""`) + `" CASCADE`
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Errorf("event trigger cleanup %q: %v", name, err)
		}
	}
}

func eventTriggerScenario(t *testing.T, names []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			// Register before declarations; cleanup uses another borrower after all
			// held statements and transaction owners have unwound.
			t.Cleanup(func() { eventTriggerCleanup(t, b.DB, names) })
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				c.ExecContext(ctx, "ROLLBACK")
			}()
			body(t, p, b, c)
		})
	}
}

func eventTriggerAuditSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE et_audit(seq integer,trigger_name text,event text,tag text)",
		"CREATE FUNCTION et_record(n integer,label text,ev text,command text) RETURNS integer LANGUAGE SQL AS $$INSERT INTO public.et_audit VALUES(n,label,ev,command); SELECT 1$$",
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
	}
}

func eventTriggerFunction(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string, seq int, body string) {
	t.Helper()
	q := fmt.Sprintf("CREATE OR REPLACE FUNCTION %s() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE result integer; BEGIN SELECT public.et_record(%d,'%s',TG_EVENT,TG_TAG) INTO result; %s END$$", name, seq, name, body)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
}

func eventTriggerCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name, event, tags, function string) {
	t.Helper()
	filter := ""
	if tags != "" {
		filter = " WHEN TAG IN (" + tags + ")"
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EVENT TRIGGER "+name+" ON "+event+filter+" EXECUTE FUNCTION "+function+"()", 0))
}

func eventTriggerAudit(rows ...[]any) fixture.Step {
	return fixture.Select("SELECT seq,trigger_name,event,tag FROM et_audit ORDER BY seq,trigger_name,event,tag", []string{"seq", "trigger_name", "event", "tag"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, rows)
}

func eventTriggerRow(seq int64, name, event, tag string) []any { return []any{seq, name, event, tag} }

func eventTriggerCount(query string, n int64) fixture.Step {
	return fixture.Select(query, []string{"n"}, []string{"INT8"}, [][]any{{n}})
}

func eventTriggerDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}

func eventTriggerFailure(function, message string) fixture.Diagnostic {
	d := eventTriggerDiagnostic("P0001", message)
	d.Context = "PL/pgSQL function " + function + "() line 1 at RAISE"
	return d
}

func eventTriggerNotice(function, message string) fixture.Diagnostic {
	d := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message}
	d.Context = "PL/pgSQL function " + function + "() line 1 at RAISE"
	return d
}

func eventTriggerPhases(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tag string) {
	t.Helper()
	eventTriggerAuditSetup(t, p, b, c)
	for i, phase := range []string{"ddl_command_start", "sql_drop", "table_rewrite", "ddl_command_end"} {
		fn := fmt.Sprintf("et_phase_%d", i+1)
		eventTriggerFunction(t, p, b, c, fn, i+1, "RETURN;")
		filter := tag
		if phase == "table_rewrite" {
			var rewriteTags []string
			for _, candidate := range strings.Split(tag, ",") {
				switch candidate = strings.TrimSpace(candidate); candidate {
				case "'ALTER TABLE'", "'ALTER TYPE'", "'ALTER MATERIALIZED VIEW'":
					rewriteTags = append(rewriteTags, candidate)
				}
			}
			filter = strings.Join(rewriteTags, ",")
			if filter == "" {
				filter = "'ALTER TABLE'"
			}
		}
		eventTriggerCreate(t, p, b, c, fn, phase, filter, fn)
	}
}

var eventTriggerPhaseNames = []string{"et_phase_1", "et_phase_2", "et_phase_3", "et_phase_4"}
