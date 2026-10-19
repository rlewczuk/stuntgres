package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func inetMaskStoredSetup() []fixture.Step {
	return []fixture.Step{
		fixture.Exec("CREATE TABLE nm_stored(id integer,ip inet,net cidr,hm inet GENERATED ALWAYS AS (hostmask(ip)) STORED,nm inet GENERATED ALWAYS AS (netmask(ip)) STORED,n cidr GENERATED ALWAYS AS (network(ip)) STORED,si inet GENERATED ALWAYS AS (set_masklen(ip,16)) STORED,sc cidr GENERATED ALWAYS AS (set_masklen(net,16)) STORED,d cidr DEFAULT network(INET '10.1.2.3/8'),CONSTRAINT nm_prefix CHECK (masklen(network(ip))<=24))", 0),
		fixture.Exec("CREATE VIEW nm_saved AS SELECT id,hostmask(ip) AS hm,netmask(ip) AS nm,network(ip) AS n,set_masklen(ip,16) AS si,set_masklen(net,16) AS sc,inet_merge(ip,ip) AS m FROM nm_stored", 0),
		fixture.Exec("INSERT INTO nm_stored(id,ip,net) VALUES(1,'192.168.1.5/24','192.168.1.0/24'),(2,'2001:db8::ffff/24','2001:d00::/24'),(3,NULL,NULL)", 3),
	}
}

func inetMaskStoredChecks() []fixture.Step {
	rows := [][]any{
		{"0.0.0.255", "255.255.255.0", "192.168.1.0/24", "192.168.1.5/16", "192.168.0.0/16", "192.168.1.0/24"},
		{"0:ff:ffff:ffff:ffff:ffff:ffff:ffff", "ffff:ff00::", "2001:d00::/24", "2001:db8::ffff/16", "2001::/16", "2001:d00::/24"},
		{nil, nil, nil, nil, nil, nil},
	}
	return []fixture.Step{
		fixture.Select("SELECT hm,nm,n,si,sc,m FROM nm_saved ORDER BY id", inetMaskNames, inetMaskTypes, rows),
		fixture.Select("SELECT hm,nm,n,si,sc,d FROM nm_stored ORDER BY id", []string{"hm", "nm", "n", "si", "sc", "d"}, inetMaskTypes, [][]any{
			{rows[0][0], rows[0][1], rows[0][2], rows[0][3], rows[0][4], "10.0.0.0/8"},
			{rows[1][0], rows[1][1], rows[1][2], rows[1][3], rows[1][4], "10.0.0.0/8"},
			{nil, nil, nil, nil, nil, "10.0.0.0/8"},
		}),
	}
}

func TestReferenceINETMaskStored(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, s := range inetMaskStoredSetup() {
			run(s)
		}
		checks := inetMaskStoredChecks()
		for _, s := range checks {
			inetFormatBoth(t, p, b, s)
		}
		run(fixture.Exec("UPDATE nm_stored SET ip='192.168.1.6/24' WHERE id=1", 1))
		inetFormatBoth(t, p, b, fixture.Select("SELECT si FROM nm_stored WHERE id=1", []string{"si"}, []string{"INET"}, [][]any{{"192.168.1.6/16"}}))
		run(fixture.Exec("UPDATE nm_stored SET ip='192.168.1.5/24' WHERE id=1", 1))
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "nm_stored" violates check constraint "nm_prefix"`)
		d.Schema = "public"
		d.Table = "nm_stored"
		d.Constraint = "nm_prefix"
		// Exact row Detail is a pinned-oracle discovery hypothesis, not native output.
		d.Detail = "Failing row contains (4, 1.2.3.4, 1.2.3.4/32, 0.0.0.0, 255.255.255.255, 1.2.3.4/32, 1.2.3.4/16, 1.2.0.0/16, 10.0.0.0/8)."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO nm_stored(id,ip,net) VALUES(4,'1.2.3.4','1.2.3.4/32')", d)
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			s = fixture.ErrorDiagnostic("UPDATE nm_stored SET ip=set_masklen(ip,129)", inetMaskProvider("invalid mask length: 129"))
			s.Prepared = prepared
			s.ErrorStage = "execute"
			run(s)
			for _, s := range checks {
				inetFormatBoth(t, p, b, s)
			}
		}
		if b.PG {
			return
		} // Native controls alone have no PostgreSQL API counterpart.
		old, err := b.DB.QueryContext(p.Ctx, checks[0].Query)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := old.Close(); err != nil {
				t.Error(err)
			}
		}()
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
		retained := checks[0]
		retained.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			c, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			fixture.ScalarAssertDescription(t, c, retained.Columns, retained.Types)
		}
		retained.CheckRaw = func(t *testing.T, col, row int, v any) bool {
			fixture.AssertRawValue(t, false, retained.Types[col], v)
			return true
		}
		rangeConsumeHeld(t, old, retained)
		for _, s := range checks {
			inetFormatBoth(t, p, b, s)
		}
		target := fixture.NewFixture(t, false, true) // A genuinely independent Driver.
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		tb := fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}
		for _, s := range checks {
			inetFormatBoth(t, target, tb, s)
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("unstable symbolic replay", err)
		}
		script := "CREATE TABLE nm_prefix_valid(v integer);CREATE TABLE nm_bad(v inet DEFAULT hostmask(INET 'bad'));"
		bad := inetDiagnostic("inet", "bad", false)
		bad.Position = fixture.Position(strings.Index(script, "'bad'") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(script)), &bad)
		for _, s := range checks {
			inetFormatBoth(t, target, tb, s)
		}
		fixture.RunStep(t, target.Ctx, tb.DB, tb.Recorder, fixture.Exec("INSERT INTO nm_stored(id,ip,net) VALUES(5,'10.2.3.4/8','10.0.0.0/8')", 1))
		inetFormatBoth(t, target, tb, fixture.Select("SELECT n,si,sc,d FROM nm_stored WHERE id=5", []string{"n", "si", "sc", "d"}, []string{"CIDR", "INET", "CIDR", "CIDR"}, [][]any{{"10.0.0.0/8", "10.2.3.4/16", "10.0.0.0/16", "10.0.0.0/8"}}))
		if p.Postgres != nil {
			for _, q := range []string{"DROP SCHEMA public CASCADE; CREATE SCHEMA public", dump.String()} {
				if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
					t.Fatal(err)
				}
			}
			pg := fixture.Backend{DB: p.Postgres, Recorder: p.PostgresRecord, PG: true}
			for _, s := range checks {
				inetFormatBoth(t, p, pg, s)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}

func TestReferenceINETMaskFeatureSmoke(t *testing.T) {
	inetMaskScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		inetFormatBoth(t, p, b, fixture.Select(inetMaskManual, inetMaskNames, inetMaskTypes, inetMaskManualRows))
		inetFormatBoth(t, p, b, fixture.Select("SELECT hostmask(INET '2001:db8::ffff/113') AS hm,netmask(INET '2001:db8::ffff/113') AS nm,network(INET '2001:db8::ffff/113') AS n", inetMaskNames[:3], inetMaskTypes[:3], [][]any{{"::7fff", "ffff:ffff:ffff:ffff:ffff:ffff:ffff:8000", "2001:db8::8000/113"}}))
		q := "SELECT hostmask(NULL::inet) AS hm,netmask(NULL::inet) AS nm,network(NULL::inet) AS n,set_masklen(NULL::inet,16) AS si,set_masklen(NULL::cidr,16) AS sc,inet_merge(NULL::inet,INET '::1') AS m"
		inetFormatBoth(t, p, b, inetMaskMetadataStep(b, q, inetMaskNames, inetMaskTypes, [][]any{{nil, nil, nil, nil, nil, nil}}))
		inetFormatBoth(t, p, b, inetMaskMetadataStep(b, q+" WHERE FALSE", inetMaskNames, inetMaskTypes, nil))
		for _, tc := range []struct {
			q           string
			valid, bad  []any
			typ, w, msg string
		}{
			{"SELECT set_masklen($1::inet,$2::integer) AS v", []any{"1.2.3.4", 24}, []any{"1.2.3.4", 33}, "INET", "1.2.3.4/24", "invalid mask length: 33"},
			{"SELECT inet_merge($1::inet,$2::inet) AS v", []any{"::1", "::2"}, []any{"1.2.3.4", "::1"}, "CIDR", "::/126", "cannot merge addresses from different families"},
		} {
			stmt, err := b.DB.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: tc.q}
			good := fixture.Select(tc.q, []string{"v"}, []string{tc.typ}, [][]any{{tc.w}}, tc.valid...)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
			bad := fixture.ErrorDiagnostic(tc.q, inetMaskProvider(tc.msg), tc.bad...)
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		}
		for _, s := range inetMaskStoredSetup() {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, s := range inetMaskStoredChecks() {
			inetFormatBoth(t, p, b, s)
		}
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			for _, s := range inetMaskStoredChecks() {
				inetFormatBoth(t, p, b, s)
			}
		}
	})
}
