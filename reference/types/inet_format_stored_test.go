package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETFormatStored(t *testing.T) {
	inetFormatScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE nf_stored(id integer,ip inet,net cidr,h text GENERATED ALWAYS AS (host(ip)) STORED,a text GENERATED ALWAYS AS (abbrev(net)) STORED,f integer DEFAULT family(INET '::1'),CONSTRAINT nf_family CHECK (family(ip) IN (4,6)))", 0),
			fixture.Exec("CREATE VIEW nf_saved AS SELECT id,abbrev(ip) AS a,host(ip) AS h,text(ip) AS t,family(ip) AS f,masklen(ip) AS m,broadcast(ip) AS b,inet_same_family(ip,INET '::1') AS s,abbrev(net) AS c FROM nf_stored", 0),
			fixture.Exec("INSERT INTO nf_stored(id,ip,net) VALUES(1,'192.168.1.5/24','10.1'),(2,'2001:db8::1/113','2001:4f8:3:ba::/64'),(3,NULL,NULL)", 3),
			fixture.Exec("UPDATE nf_stored SET ip='192.168.1.6/24' WHERE id=1", 1),
			fixture.Exec("UPDATE nf_stored SET ip='192.168.1.5/24' WHERE id=1", 1),
		} {
			run(s)
		}
		checks := []fixture.Step{
			fixture.Select("SELECT h,a,f FROM nf_stored ORDER BY id", []string{"h", "a", "f"}, []string{"TEXT", "TEXT", "INT4"}, [][]any{{"192.168.1.5", "10.1/16", int64(6)}, {"2001:db8::1", "2001:4f8:3:ba/64", int64(6)}, {nil, nil, int64(6)}}),
			fixture.Select("SELECT a,h,t,f,m,b,s,c FROM nf_saved ORDER BY id", []string{"a", "h", "t", "f", "m", "b", "s", "c"}, []string{"TEXT", "TEXT", "TEXT", "INT4", "INT4", "INET", "BOOL", "TEXT"}, [][]any{{"192.168.1.5/24", "192.168.1.5", "192.168.1.5/24", int64(4), int64(24), "192.168.1.255/24", false, "10.1/16"}, {"2001:db8::1/113", "2001:db8::1", "2001:db8::1/113", int64(6), int64(113), "2001:db8::7fff/113", true, "2001:4f8:3:ba/64"}, {nil, nil, nil, nil, nil, nil, nil, nil}}),
		}
		for _, s := range checks {
			inetFormatBoth(t, p, b, s)
		}
		if b.PG {
			return
		} // Only native Reset/Dump/Restore controls lack a PG API.
		old, err := b.DB.QueryContext(p.Ctx, checks[1].Query)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
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
		retained := checks[1]
		retained.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarAssertDescription(t, columns, retained.Columns, retained.Types)
		}
		retained.CheckRaw = func(t *testing.T, column, row int, value any) bool {
			fixture.AssertRawValue(t, false, retained.Types[column], value)
			return true
		}
		fixture.RangeConsumeHeld(t, old, retained)
		for _, s := range checks {
			inetFormatBoth(t, p, b, s)
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		for _, s := range checks {
			inetFormatBoth(t, target, tb, s)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("symbolic replay changed", err)
		}
		script := "CREATE TABLE replacement(h text DEFAULT host(INET 'bad'));"
		d := inetDiagnostic("inet", "bad", false)
		d.Position = fixture.Position(strings.Index(script, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)), &d)
		for _, s := range checks {
			inetFormatBoth(t, target, tb, s)
		}
		if p.Postgres != nil {
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal(err)
			}
			pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
			for _, s := range checks {
				inetFormatBoth(t, p, pg, s)
			}
			// Leave the oracle empty for its shared scenario body below.
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
