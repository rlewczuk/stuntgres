package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTriggerBuiltinsFeatureSmoke(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "tb_smoke")
	triggerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		userOperatorExec(t, p, b, c, "CREATE SCHEMA tb_smoke",
			"CREATE TABLE tb_smoke.explicit(id integer PRIMARY KEY,title text,body text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_smoke.explicit FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','pg_catalog.simple','title','body')",
			"CREATE TRIGGER z BEFORE UPDATE ON tb_smoke.explicit FOR EACH ROW EXECUTE FUNCTION pg_catalog.suppress_redundant_updates_trigger()",
			"CREATE TEXT SEARCH DICTIONARY tb_smoke.reject (TEMPLATE=pg_catalog.simple,Accept=false)",
			"CREATE TEXT SEARCH CONFIGURATION tb_smoke.live (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION tb_smoke.live ALTER MAPPING FOR asciiword WITH tb_smoke.reject",
			"CREATE TABLE tb_smoke.row_config(id integer PRIMARY KEY,cfg regconfig,title text,v tsvector)",
			"CREATE TRIGGER a BEFORE INSERT OR UPDATE ON tb_smoke.row_config FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger_column('v','cfg','title')")
		run(builtinVectorRows("INSERT INTO tb_smoke.explicit VALUES(1,'The Fat','Rats rats',NULL) RETURNING id,v::text AS vector", []any{int64(1), "'fat':2 'rats':3,4 'the':1"}))
		q := "UPDATE tb_smoke.explicit SET title=$1 WHERE id=1 RETURNING id,v::text AS vector"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		call := func(text string, rows ...[]any) {
			s := builtinVectorRows(q, rows...)
			s.Args = []any{text}
			s.CheckMetadata = builtinReturningMetadata(b)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		call("The Fat")
		call("Big", []any{int64(1), "'big':1 'rats':2,3"})
		run(fixture.Exec("UPDATE tb_smoke.explicit SET v='manual:9' WHERE id=1", 1))
		run(fixture.Exec("UPDATE tb_smoke.explicit SET id=id WHERE id=1", 0))
		call("Big", []any{int64(1), "'big':1 'rats':2,3"})
		run(builtinVectorRows("INSERT INTO tb_smoke.row_config VALUES(1,'tb_smoke.live','Rats',NULL) RETURNING id,v::text AS vector", []any{int64(1), ""}))
		rq := "UPDATE tb_smoke.row_config SET title=$1 WHERE id=1 RETURNING id,v::text AS vector"
		rs, err := c.PrepareContext(p.Ctx, rq)
		if err != nil {
			t.Fatal(err)
		}
		defer rs.Close()
		rowCall := func() {
			s := builtinVectorRows(rq, []any{int64(1), "'rats':1"})
			s.Args = []any{"Rats"}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: rs, SQL: rq}, b.Recorder, s)
		}
		userOperatorExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION tb_smoke.live ALTER MAPPING FOR asciiword WITH pg_catalog.simple")
		rowCall()
		userOperatorExec(t, p, b, c, "CREATE TABLE tb_smoke.bad(id integer,title text,v tsvector)", "CREATE TRIGGER a BEFORE INSERT ON tb_smoke.bad FOR EACH ROW EXECUTE FUNCTION pg_catalog.tsvector_update_trigger('v','simple','title')")
		run(fixture.ErrorDiagnostic("INSERT INTO tb_smoke.bad VALUES(1,'Rats',NULL)", fixture.ControlsReferenceDiagnostic("22023", `text search configuration name "simple" must be schema-qualified`)))
		run(fixture.Select("SELECT count(*) AS n FROM tb_smoke.bad", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		for _, tc := range []struct{ function, code, message string }{
			{"suppress_redundant_updates_trigger", "39P01", "suppress_redundant_updates_trigger: must be called as trigger"},
			{"tsvector_update_trigger", "XX000", "tsvector_update_trigger: not fired by trigger manager"},
			{"tsvector_update_trigger_column", "XX000", "tsvector_update_trigger: not fired by trigger manager"},
		} {
			run(fixture.ErrorDiagnostic("SELECT pg_catalog."+tc.function+"()", fixture.ControlsReferenceDiagnostic(tc.code, tc.message)))
		}
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_smoke.explicit", []any{int64(1), "'big':1 'rats':2,3"}))
		call("Big")
		call("Small", []any{int64(1), "'rats':2,3 'small':1"})
		rowCall()
		bad := dump.String() + ";CREATE TABLE tb_smoke.staged(n integer);CREATE TRIGGER bad BEFORE INSERT ON tb_smoke.explicit FOR EACH ROW EXECUTE FUNCTION tb_smoke.missing();"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("late invalid Restore accepted")
		}
		call("Small")
		run(builtinVectorRows("SELECT id,v::text AS vector FROM tb_smoke.explicit", []any{int64(1), "'rats':2,3 'small':1"}))
		rowCall()
	})
}
