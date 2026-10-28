package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func parserDDLCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, name, config string) {
	t.Helper()
	for _, s := range []fixture.Step{parserDDLTokens(name), fixture.Select("SELECT * FROM ts_token_type($1::text) ORDER BY tokid", []string{"tokid", "alias", "description"}, []string{"INT4", "TEXT", "TEXT"}, ftsParserDescriptors(), name), ftsConfigurationVector("SELECT to_tsvector($1::regconfig,'Word') AS normalized", "'word':1", config), fixture.Select("SELECT c.cfgparser=p.oid AS linked FROM pg_ts_config c JOIN pg_ts_parser p ON p.oid=c.cfgparser WHERE c.oid=$1::regconfig", []string{"linked"}, []string{"BOOL"}, [][]any{{true}}, config)} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
}
func TestReferenceFTSParserDDLControls(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "parser_controls_schema")
	var emitted []byte
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		if !b.PG {
			run(fixture.Exec(parserDDLCreate("pg_temp.parser_controls_temp"), 0))
		}
		defer parserDDLRestoreDefault(t, c)
		for _, q := range []string{"CREATE SCHEMA parser_controls_schema", parserDDLCreate("public.parser_controls"), "CREATE TEXT SEARCH PARSER public.parser_controls_headline (" + parserDDLMethods + ",HEADLINE=pg_catalog.prsd_headline)", "ALTER TEXT SEARCH PARSER pg_catalog.default RENAME TO parser_controls_seed", "ALTER TEXT SEARCH PARSER pg_catalog.parser_controls_seed SET SCHEMA parser_controls_schema"} {
			run(fixture.Exec(q, 0))
		}
		for _, q := range parserDDLConfigure("public.parser_controls_cfg", "public.parser_controls") {
			run(fixture.Exec(q, 0))
		}
		for _, q := range parserDDLConfigure("public.parser_controls_seed_cfg", "parser_controls_schema.parser_controls_seed") {
			run(fixture.Exec(q, 0))
		}
		check := func(p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
			parserDDLCheck(t, p, b, e, "public.parser_controls", "public.parser_controls_cfg")
			parserDDLCheck(t, p, b, e, "parser_controls_schema.parser_controls_seed", "public.parser_controls_seed_cfg")
			fixture.RunStep(t, p.Ctx, e, b.Recorder, parserDDLCallbacks("parser_controls", 0))
			fixture.RunStep(t, p.Ctx, e, b.Recorder, parserDDLCallbacks("parser_controls_headline", 3720))
		}
		check(p, b, c)
		if b.PG {
			if len(emitted) == 0 {
				t.Fatal("missing actual native dump")
			}
			for _, q := range []string{"DROP TEXT SEARCH CONFIGURATION public.parser_controls_cfg,public.parser_controls_seed_cfg", "DROP TEXT SEARCH PARSER public.parser_controls,public.parser_controls_headline", "ALTER TEXT SEARCH PARSER parser_controls_schema.parser_controls_seed SET SCHEMA pg_catalog", `ALTER TEXT SEARCH PARSER pg_catalog.parser_controls_seed RENAME TO "default"`, "DROP SCHEMA parser_controls_schema"} {
				run(fixture.Exec(q, 0))
			}
			if _, err := c.ExecContext(p.Ctx, string(emitted)); err != nil {
				t.Fatal("actual dump replay", err)
			}
			check(p, b, c)
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = bytes.Clone(dump.Bytes())
		text := string(emitted)
		if !strings.Contains(text, `"pg_catalog"."prsd_start"`) || strings.Contains(text, `ALTER TEXT SEARCH PARSER "public"."parser_controls" OWNER`) || strings.Contains(text, `CREATE TEXT SEARCH PARSER "parser_controls_schema"."parser_controls_seed"`) {
			t.Fatal("callback/owner/seed dump contract", text)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT * FROM ts_parse('public.parser_controls','Word')")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		metadata, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("same-driver Restore", err)
		}
		check(p, b, c)
		if !old.Next() {
			t.Fatal(old.Err())
		}
		var id int64
		var token string
		if err := old.Scan(&id, &token); err != nil || id != 1 || token != "Word" {
			t.Fatal(id, token, err)
		}
		if old.Next() || old.Err() != nil || metadata[0].DatabaseTypeName() != "INT4" {
			t.Fatal("retained rows", old.Err())
		}
		old.Close()
		redump := func(p *fixture.Pair) {
			t.Helper()
			var out bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &out); err != nil || !bytes.Equal(out.Bytes(), emitted) {
				t.Fatal("deterministic dump", err, out.String())
			}
		}
		redump(p)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(parserDDLWord("default"))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("Reset/Restore", err)
		}
		check(p, b, c)
		run(fixture.Exec("CREATE TABLE parser_controls_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO parser_controls_sentinel VALUES(42)", 1))
		retained, err := c.QueryContext(p.Ctx, "SELECT * FROM ts_parse('public.parser_controls','Word')")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		diagnostic := fixture.ControlsReferenceDiagnostic("42P17", "text search parser gettoken method is required")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, bytes.NewBufferString(text+"CREATE TEXT SEARCH PARSER public.parser_bad (START=pg_catalog.prsd_start);")), &diagnostic)
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(emitted)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		reading, stop := context.WithCancel(p.Ctx)
		reader := &fixture.EnumCancelReader{Reader: bytes.NewReader(emitted), Cancel: stop}
		err = stuntgres.Restore(reading, p.Native, reader)
		stop()
		if !errors.Is(err, context.Canceled) {
			t.Fatal("reader cancel", err)
		}
		check(p, b, c)
		run(fixture.Select("SELECT v FROM parser_controls_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		if err := retained.Scan(&id, &token); err != nil || id != 1 || token != "Word" {
			t.Fatal("failed Restore retained data", id, token, err)
		}
		retained.Close()
		var sourceOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_parser WHERE prsname='parser_controls'").Scan(&sourceOID); err != nil {
			t.Fatal(err)
		}
		fresh := fixture.NewFixture(t, false, true)
		fc, err := fresh.Memory.Conn(fresh.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		fb := fixture.Backend{DB: fresh.Memory, Recorder: fresh.MemoryRecord}
		var collision string
		for i := 0; ; i++ {
			collision = fmt.Sprintf("parser_collision_%d", i)
			fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, fixture.Exec(parserDDLCreate("pg_temp."+collision), 0))
			var oid int64
			if err := fc.QueryRowContext(fresh.Ctx, "SELECT oid::bigint FROM pg_ts_parser WHERE prsname=$1", collision).Scan(&oid); err != nil {
				t.Fatal(err)
			}
			if oid > sourceOID {
				t.Fatalf("collision overshot %d > %d", oid, sourceOID)
			}
			if oid == sourceOID {
				break
			}
		}
		if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(emitted)); err != nil {
			t.Fatal("fresh Driver Restore", err)
		}
		var targetOID int64
		if err := fc.QueryRowContext(fresh.Ctx, "SELECT oid::bigint FROM pg_ts_parser WHERE prsname='parser_controls'").Scan(&targetOID); err != nil || targetOID == sourceOID {
			t.Fatal("parser address not displaced", sourceOID, targetOID, err)
		}
		check(fresh, fb, fc)
		fixture.RunStep(t, fresh.Ctx, fc, fb.Recorder, parserDDLWord("pg_temp."+collision))
		redump(fresh)
	})
}
func TestReferenceFTSParserDDLLifecycle(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "parser_lifecycle_schema")
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE SCHEMA parser_lifecycle_schema", parserDDLCreate("parser_lifecycle_schema.parser_lifecycle"), "CREATE TEXT SEARCH CONFIGURATION public.parser_lifecycle_config (PARSER=parser_lifecycle_schema.parser_lifecycle)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("DROP SCHEMA parser_lifecycle_schema CASCADE", 0))
		run(fixture.Select("SELECT prsname FROM pg_ts_parser WHERE prsname='parser_lifecycle'", []string{"prsname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT cfgname FROM pg_ts_config WHERE cfgname='parser_lifecycle_config'", []string{"cfgname"}, []string{"NAME"}, nil))
		run(fixture.Exec(parserDDLCreate("public.parser_lifecycle"), 0))
		var parserOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_ts_parser WHERE prsname='parser_lifecycle'").Scan(&parserOID); err != nil {
			t.Fatal(err)
		}
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		if _, err := holder.ExecContext(p.Ctx, "BEGIN; ALTER TEXT SEARCH PARSER public.parser_lifecycle RENAME TO parser_held"); err != nil {
			t.Fatal(err)
		}
		defer holder.ExecContext(p.Ctx, "ROLLBACK")
		fixture.DefinitionActiveCancel(t, p, b, c, holder, "ALTER TEXT SEARCH PARSER public.parser_lifecycle RENAME TO parser_canceled", "object", "AccessExclusiveLock", 3601, parserOID, "", nil)
		if _, err := holder.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
			t.Fatal(err)
		}
		run(parserDDLWord("public.parser_lifecycle"))
		held, err := c.PrepareContext(p.Ctx, "SELECT * FROM ts_parse($1::text,$2::text)")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if !b.PG {
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if _, err := c.ExecContext(canceled, "ALTER TEXT SEARCH PARSER public.parser_lifecycle RENAME TO parser_canceled"); !errors.Is(err, context.Canceled) {
				t.Fatal("pre-cancel", err)
			}
		}
		rows, err := held.QueryContext(p.Ctx, "public.parser_lifecycle", "Word")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("held recovery", rows.Err())
		}
		var id int64
		var word string
		if err := rows.Scan(&id, &word); err != nil || id != 1 || word != "Word" {
			t.Fatal(id, word, err)
		}
	})
}
