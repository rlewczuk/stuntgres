package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsmnControls(t *testing.T) {
	runISNControlPhases(t, isnControlPhases{
		name: "ismn", valid: "M123456785", invalid: "M123456780",
		message: `invalid check digit for ISMN number: "M123456780", should be 5`, private: "M999999997",
		query: ismnControlQuery, rows: ismnControlRows,
		setup: ismnControlSetup, verify: ismnControlVerify,
		failures: []string{"invalid_literal", "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version", "isbn13_missing_member", "isbn13_duplicate_slot", "isbn13_wrong_type_role", "isbn13_wrong_output_signature", "ismn_missing_member", "ismn_duplicate_slot", "ismn_wrong_type_role", "ismn_wrong_output_signature", "canceled", "reader"},
	})
}

func TestReferenceIsmnFeatureSmoke(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		setup := func() {
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE ismn_smoke(id integer NOT NULL,v ismn)", 0))
			run(fixture.Exec("INSERT INTO ismn_smoke VALUES(1,'9790123456785')", 1))
			insert := ean13Held(t, p, c, "INSERT INTO ismn_smoke VALUES($1,$2)")
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 2, []byte("M123456780!")))
			fixture.RunStep(t, p.Ctx, insert, b.Recorder, fixture.Exec(insert.SQL, 1, 3, nil))
		}
		expected := [][]any{{int64(1), "M-1234-5678-5"}, {int64(2), "M-1234-5678-5!"}, {int64(3), nil}}
		verify := func(pair *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
			s := fixture.Ean13Select(backend, "SELECT id,v FROM ismn_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISMN"}, expected, []bool{false, true})
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, s)
			fixture.RunStep(t, pair.Ctx, conn, backend.Recorder, fixture.Prepared(s))
			held := ean13Held(t, pair, conn, s.Query)
			fixture.RunStep(t, pair.Ctx, held, backend.Recorder, s)
			isbnIdentity(t, pair, backend, conn, "public")
		}
		setup()
		verify(p, b, c)
		held := ean13Held(t, p, c, "INSERT INTO ismn_smoke VALUES(4,$1),(5,$2)")
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
		run(fixture.Ean13Select(b, "SELECT id,v FROM ismn_smoke WHERE id>3 ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISMN"}, [][]any{{int64(4), "M-1234-5678-5"}, {int64(5), "M-1234-5678-5!"}}, []bool{false, true}))
		run(fixture.Exec("DELETE FROM ismn_smoke WHERE id>3", 2))
		run(fixture.Exec("DROP TABLE ismn_smoke", 0))
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
		old, metadata := isbnRetain(t, p, c, "SELECT id,v AS e,v FROM ismn_smoke ORDER BY id")
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(p, b, c)
		isbnConsumeRetained(t, old, metadata, [][]any{{int64(1), "M-1234-5678-5", "M-1234-5678-5"}, {int64(2), "M-1234-5678-5!", "M-1234-5678-5!"}, {int64(3), nil, nil}}, "INT4", "ISMN", "ISMN")
		if p.Postgres != nil {
			isbnReplay(t, p, dump.String(), nil, func(pg *sql.Conn, pb fixture.Backend) {
				verify(p, pb, pg)
			})
		}
	})
}
