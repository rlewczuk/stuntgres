package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsmn13Controls(t *testing.T) {
	t.Run("roundtrip", func(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		bad, want := ismn13ControlBadStream()
		if b.PG {
			s := fixture.ErrorDiagnostic(bad, want)
			s.ErrorStage = "execute"
			run(s)
		} else {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ismn13_staged'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		ismn13ControlSetup(t, p, b, c)
		ismn13ControlVerify(t, p, b, c)
		run(fixture.Exec("DROP TABLE ismn13_control,ismn13_required", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		ismn13ControlSetup(t, p, b, c)
		ismn13ControlVerify(t, p, b, c)
		if b.PG {
			return
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO ismn13_control VALUES(99,'1400000000007','M999999997')", 1))
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
		old, metadata := isbnRetain(t, p, c, ismn13ControlQuery)
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn','_issn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ismn13ControlVerify(t, p, b, c)
		isbnConsumeRetained(t, old, metadata, ismn13ControlRows, "INT4", "EAN13", "ISMN13")
		ismn13ControlTarget(t, p, c, dump.Bytes())
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("same-target Restore changed deterministic dump")
		}
		if p.Postgres != nil {
			isbnReplay(t, p, dump.String(), nil, func(pg *sql.Conn, pb fixture.Backend) {
				ismn13ControlVerify(t, p, pb, pg)
			})
		}
	})
	})
	for _, kind := range []string{"invalid_literal", "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version", "isbn13_missing_member", "isbn13_duplicate_slot", "isbn13_wrong_type_role", "isbn13_wrong_output_signature", "ismn_missing_member", "ismn_duplicate_slot", "ismn_wrong_type_role", "ismn_wrong_output_signature", "ismn13_missing_member", "ismn13_duplicate_slot", "ismn13_wrong_type_role", "ismn13_wrong_input_name", "ismn13_wrong_output_signature", "canceled", "reader"} {
		t.Run(kind, func(t *testing.T) {
			ismn13FailedReplacement(t, kind)
		})
	}
}

func TestReferenceIsmn13FeatureSmoke(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		setup := func() {
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE ismn13_smoke(id integer NOT NULL,v ismn13)", 0))
			run(fixture.Exec("INSERT INTO ismn13_smoke VALUES(1,'9790123456785')", 1))
			insert := ean13Held(t, p, c, "INSERT INTO ismn13_smoke VALUES($1,$2)")
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 2, []byte("M123456780!")))
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 3, nil))
		}
		expected := [][]any{{int64(1), "979-0-1234-5678-5"}, {int64(2), "979-0-1234-5678-5!"}, {int64(3), nil}}
		verify := func(pair *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
			s := fixture.Ean13Select(backend, "SELECT id,v FROM ismn13_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISMN13"}, expected, []bool{false, true})
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			held := ean13Held(t, pair, conn, s.Query)
			fixture.RunStep(t, pair.Ctx, held, backend.Recorder, s)
			isbnIdentity(t, pair, backend, conn, "public")
		}
		setup()
		verify(p, b, c)
		held := ean13Held(t, p, c, "INSERT INTO ismn13_smoke VALUES(4,$1),(5,$2)")
		stamps := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
			stamps[typ] = isbnIdentityStamp(t, p, c, typ)
		}
		want := isbnBoundDiagnostic("22P02", `invalid check digit for ISMN number: "M123456780", should be 5`, "M123456780", "$2")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, want, "M123456785", []byte("M123456780")))
		verify(p, b, c)
		for typ, stamp := range stamps {
			if got := isbnIdentityStamp(t, p, c, typ); got != stamp {
				t.Fatalf("failed insert replaced %s identity: %v, want %v", typ, got, stamp)
			}
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(held.SQL, 2, "M123456785", "M123456780!"))
		run(fixture.Ean13Select(b, "SELECT id,v FROM ismn13_smoke WHERE id>3 ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISMN13"}, [][]any{{int64(4), "979-0-1234-5678-5"}, {int64(5), "979-0-1234-5678-5!"}}, []bool{false, true}))
		run(fixture.Exec("DELETE FROM ismn13_smoke WHERE id>3", 2))
		run(fixture.Exec("DROP TABLE ismn13_smoke", 0))
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
		old, metadata := isbnRetain(t, p, c, "SELECT id,v AS e,v FROM ismn13_smoke ORDER BY id")
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(p, b, c)
		isbnConsumeRetained(t, old, metadata, [][]any{{int64(1), "979-0-1234-5678-5", "979-0-1234-5678-5"}, {int64(2), "979-0-1234-5678-5!", "979-0-1234-5678-5!"}, {int64(3), nil, nil}}, "INT4", "ISMN13", "ISMN13")
		target := fixture.NewFixture(t, false, true)
		conn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		tb := fixture.Backend{Recorder: target.MemoryRecord, DB: target.Memory}
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE TABLE ismn13_smoke_spacer(v integer)", 0))
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
