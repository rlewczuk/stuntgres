package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIssn13Controls(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
	issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		bad, want := issn13ControlBadStream()
		if b.PG {
			s := fixture.ErrorDiagnostic(bad, want)
			s.ErrorStage = "execute"
			run(s)
		} else {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='issn13_staged'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		issn13ControlSetup(t, p, b, c)
		issn13ControlVerify(t, p, b, c)
		// TEXT keeps the public result descriptor stable across generated type
		// OID replacement, while the retained plan must rebind the new column.
		heldText := ean13Held(t, p, c, "SELECT v::text AS v FROM issn13_control ORDER BY id")
		heldValues := fixture.Select(heldText.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"977-1234-567-00-3"}, {"977-1234-567-00-3!"}, {nil}, {"977-1234-567-89-8"}, {"977-1234-567-89-8!"}})
		fixture.RunStep(t, p.Ctx, heldText, b.Recorder, heldValues)
		run(fixture.Exec("DROP TABLE issn13_control,issn13_required", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		issn13ControlSetup(t, p, b, c)
		issn13ControlVerify(t, p, b, c)
		fixture.RunStep(t, p.Ctx, heldText, b.Recorder, heldValues)
		if b.PG {
			return
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO issn13_control VALUES(99,'1400000000007','12345679')", 1))
		var dump bytes.Buffer
		err := stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
		if err != nil || rollbackErr != nil {
			t.Fatalf("Dump = %v; rollback = %v", err, rollbackErr)
		}
		if strings.Contains(dump.String(), "140000000000") {
			t.Fatal("Dump leaked private values")
		}
		ext, consumer := strings.Index(dump.String(), "CREATE EXTENSION"), strings.Index(dump.String(), "CREATE TABLE")
		if ext < 0 || consumer < ext || strings.Contains(dump.String(), "CREATE TYPE") || strings.Contains(dump.String(), "CREATE FUNCTION") {
			t.Fatal("Dump must install isn before consumers without standalone members", dump.String())
		}
		for _, literal := range []string{"9771234567003", "9771234567003!", "9771234567898", "9771234567898!"} {
			if !strings.Contains(dump.String(), ean13Literal(literal)) {
				t.Fatalf("Dump lost full ISSN payload %q: %s", literal, dump.String())
			}
		}
		old, metadata := isbnRetain(t, p, c, issn13ControlQuery)
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn','_issn','issn13','_issn13')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		issn13ControlVerify(t, p, b, c)
		fixture.RunStep(t, p.Ctx, heldText, b.Recorder, heldValues)
		isbnConsumeRetained(t, old, metadata, issn13ControlRows, "INT4", "EAN13", "ISSN13")
		target, tb, conn := issn13ControlTarget(t, p, c, dump.Bytes())
		issn13ControlVerify(t, target, tb, conn)
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("same-target Restore changed deterministic dump")
		}
		if p.Postgres != nil {
			isbnReplay(t, p, dump.String(), nil, func(pg *sql.Conn, pb fixture.Backend) {
				issn13ControlVerify(t, p, pb, pg)
			})
		}
	})
	})
	for _, kind := range []string{"invalid_literal", "issn13_missing_member", "issn13_duplicate_slot", "issn13_wrong_type_role", "issn13_wrong_output_signature", "wrong_version", "canceled", "reader"} {
		t.Run(kind, func(t *testing.T) {
			issn13FailedReplacement(t, kind)
		})
	}
}

func TestReferenceIssn13FeatureSmoke(t *testing.T) {
	issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		setup := func() {
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE issn13_smoke(id integer NOT NULL,v issn13)", 0))
			run(fixture.Exec("INSERT INTO issn13_smoke VALUES(1,'12345679')", 1))
			insert := ean13Held(t, p, c, "INSERT INTO issn13_smoke VALUES($1,$2)")
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 2, []byte("12345670!")))
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 3, nil))
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 4, "9771234567898"))
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 5, "9771234567898!"))
		}
		expected := [][]any{{int64(1), "977-1234-567-00-3"}, {int64(2), "977-1234-567-00-3!"}, {int64(3), nil}, {int64(4), "977-1234-567-89-8"}, {int64(5), "977-1234-567-89-8!"}}
		verify := func(pair *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
			s := fixture.Ean13Select(backend, "SELECT id,v FROM issn13_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISSN13"}, expected, []bool{false, true})
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			held := ean13Held(t, pair, conn, s.Query)
			fixture.RunStep(t, pair.Ctx, held, backend.Recorder, s)
			isbnIdentity(t, pair, backend, conn, "public")
		}
		setup()
		verify(p, b, c)
		held := ean13Held(t, p, c, "INSERT INTO issn13_smoke VALUES(6,$1),(7,$2)")
		stamps := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
			stamps[typ] = isbnIdentityStamp(t, p, c, typ)
		}
		want := isbnBoundDiagnostic("22P02", `invalid check digit for ISSN number: "12345670", should be 9`, "12345670", "$2")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, want, "12345679", []byte("12345670")))
		verify(p, b, c)
		for typ, stamp := range stamps {
			if got := isbnIdentityStamp(t, p, c, typ); got != stamp {
				t.Fatalf("failed insert replaced %s identity: %v, want %v", typ, got, stamp)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 2, "12345679", "12345670!"))
		run(fixture.Ean13Select(b, "SELECT id,v FROM issn13_smoke WHERE id>5 ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISSN13"}, [][]any{{int64(6), "977-1234-567-00-3"}, {int64(7), "977-1234-567-00-3!"}}, []bool{false, true}))
		run(fixture.Exec("DELETE FROM issn13_smoke WHERE id>5", 2))
		run(fixture.Exec("DROP TABLE issn13_smoke", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		setup()
		verify(p, b, c)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, metadata := isbnRetain(t, p, c, "SELECT id,v AS e,v FROM issn13_smoke ORDER BY id")
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(p, b, c)
		isbnConsumeRetained(t, old, metadata, [][]any{{int64(1), "977-1234-567-00-3", "977-1234-567-00-3"}, {int64(2), "977-1234-567-00-3!", "977-1234-567-00-3!"}, {int64(3), nil, nil}, {int64(4), "977-1234-567-89-8", "977-1234-567-89-8"}, {int64(5), "977-1234-567-89-8!", "977-1234-567-89-8!"}}, "INT4", "ISSN13", "ISSN13")
		target := fixture.NewFixture(t, false, true)
		conn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		tb := fixture.Backend{Recorder: target.MemoryRecord, DB: target.Memory}
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE TABLE issn13_smoke_spacer(v integer)", 0))
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
			source, destination := isbnIdentityStamp(t, p, c, typ), isbnIdentityStamp(t, target, conn, typ)
			for i := range source {
				if source[i] == destination[i] {
					t.Fatalf("%s smoke destination identity %d not displaced", typ, i)
				}
			}
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(target, tb, conn)
		if p.Postgres != nil {
			isbnReplay(t, p, dump.String(), nil, func(pg *sql.Conn, pb fixture.Backend) {
				verify(p, pb, pg)
			})
		}
	})
}