package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceTSTemplateControls(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		original := tsTemplateLinked(t, p, c, r, pg)
		tsTemplateCapability(t, p, c, r)
		if pg { return }

		dump := extensionDump(t, p.Ctx, p.Native)
		if dump == "" || !strings.Contains(dump, "CREATE EXTENSION") { t.Fatal("missing emitted extension dump") }
		saved, err := c.QueryContext(p.Ctx, tsTemplateQuery, "unaccent")
		if err != nil { t.Fatal(err) }
		defer saved.Close()
		tsTemplateMetadata(t, saved, false)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
		tsTemplateRead(t, p, c, false, false)
		run(fixture.Select("SELECT * FROM pg_catalog.pg_ts_template WHERE FALSE", tsTemplateNames, tsTemplateTypes, nil))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil { t.Fatal(err) }
		tsTemplateLinked(t, p, c, r, false)
		tsTemplateCapability(t, p, c, r)
		if tsTemplateConsume(t, saved, false, true) != original { t.Fatal("retained template changed across Reset/Restore") }
		if extensionDump(t, p.Ctx, p.Native) != dump { t.Fatal("restored template changed symbolic dump") }

		committed := tsTemplateRead(t, p, c, false, true)
		handlers := tsTemplateNumeric(t, p, c)
		preserved := func() {
			t.Helper()
			if tsTemplateLinked(t, p, c, r, false) != committed || tsTemplateNumeric(t, p, c) != handlers { t.Fatal("failed replacement changed template/helper identities") }
			tsTemplateCapability(t, p, c, r)
			if extensionDump(t, p.Ctx, p.Native) != dump { t.Fatal("failed replacement changed emitted dump") }
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump+"\nCREATE EXTENSION unaccent SCHEMA ext_addon;\n")); err == nil { t.Fatal("duplicate extension replay accepted") }
		preserved()
		ctx, cancel := context.WithCancel(p.Ctx)
		defer cancel()
		err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
		if !errors.Is(err, context.Canceled) { t.Fatal("reader cancellation", err) }
		var state interface{ SQLState() string }
		if errors.As(err, &state) { t.Fatal("ordinary cancellation acquired SQLSTATE", err) }
		preserved()
		ctx, cancelQuery := context.WithCancel(p.Ctx)
		cancelQuery()
		rows, err := c.QueryContext(ctx, tsTemplateQuery, "unaccent")
		if rows != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) { t.Fatal("pre-canceled template read", rows, err) }
		preserved()
	})
}
