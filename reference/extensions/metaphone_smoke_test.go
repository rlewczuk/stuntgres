package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceMetaphoneFeatureSmoke(t *testing.T) {
	metaphoneCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE SCHEMA phon_smoke", 0))
		run(fixture.Exec("CREATE EXTENSION fuzzystrmatch WITH SCHEMA phon_smoke VERSION '1.2'", 0))
		codes := fixture.Select("SELECT phon_smoke.metaphone('GUMBO',4),phon_smoke.dmetaphone('gumbo'),phon_smoke.dmetaphone_alt('gumbo')", []string{"metaphone", "dmetaphone", "dmetaphone_alt"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"KM", "KMP", "KMP"}})
		run(codes)
		run(fixture.Prepared(codes))
		run(fixture.Select("SELECT phon_smoke.metaphone('AX',2),phon_smoke.dmetaphone('Smith'),phon_smoke.dmetaphone_alt('Smith')", []string{"metaphone", "dmetaphone", "dmetaphone_alt"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"AK", "SM0", "XMT"}}))
		run(fixture.Select("SELECT phon_smoke.metaphone('',0),phon_smoke.metaphone(NULL::text,4),phon_smoke.dmetaphone(''),phon_smoke.dmetaphone_alt(NULL::text)", []string{"metaphone", "metaphone", "dmetaphone", "dmetaphone_alt"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"", nil, "", nil}}))
		run(fixture.Exec("CREATE TABLE phon_names(id integer,name text,primary_code text GENERATED ALWAYS AS (phon_smoke.dmetaphone(name)) STORED,alternate_code text GENERATED ALWAYS AS (phon_smoke.dmetaphone_alt(name)) STORED)", 0))
		run(fixture.Exec("INSERT INTO phon_names(id,name) VALUES(1,'Smith'),(2,'Schmidt'),(3,''),(4,NULL)", 4))
		run(fixture.Exec("CREATE VIEW phon_view AS SELECT id,primary_code,alternate_code FROM phon_names", 0))
		view := fixture.Select("SELECT id,primary_code,alternate_code FROM phon_view ORDER BY id", []string{"id", "primary_code", "alternate_code"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "SM0", "XMT"}, {int64(2), "XMT", "SMT"}, {int64(3), "", ""}, {int64(4), nil, nil}})
		run(view)
		run(fixture.Prepared(view))
		run(fixture.Select("SELECT id FROM phon_names WHERE phon_smoke.dmetaphone_alt(name)='SMT' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
		q := "SELECT phon_smoke.dmetaphone($1)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ source, want string }{{"Smith", "SM0"}, {"Schmidt", "XMT"}, {"gumbo", "KMP"}} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, metaphoneSelect(q, "dmetaphone", tc.want, tc.source))
		}
		mq := "SELECT phon_smoke.metaphone($1,$2)"
		ms, err := c.PrepareContext(p.Ctx, mq)
		if err != nil {
			t.Fatal(err)
		}
		defer ms.Close()
		mh := fixture.HeldStatement{Executor: c, Statement: ms, SQL: mq}
		fixture.RunStep(t, p.Ctx, mh, b.Recorder, metaphoneFailure(mq, "2200F", "output cannot be empty string", "gumbo", 0))
		fixture.RunStep(t, p.Ctx, mh, b.Recorder, metaphoneSelect(mq, "metaphone", "K", "gumbo", 1))
		run(fixture.Exec("SET search_path TO pg_catalog", 0))
		run(codes)
		run(fixture.Exec("RESET search_path", 0))
		var owner string
		if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		metaphoneInventory(t, p, b, c, "phon_smoke", owner, false)
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		run(view)
		run(codes)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, metaphoneSelect(q, "dmetaphone", "SM0", "Smith"))
		fixture.RunStep(t, p.Ctx, mh, b.Recorder, metaphoneSelect(mq, "metaphone", "K", "gumbo", 1))
		metaphoneInventory(t, p, b, c, "phon_smoke", owner, false)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE EXTENSION fuzzystrmatch VERSION 'missing';")); err == nil {
			t.Fatal("bad restore accepted")
		}
		run(view)
		run(codes)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, metaphoneSelect(q, "dmetaphone", "KMP", "gumbo"))
	})
}
