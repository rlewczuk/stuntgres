package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceIsmnControls(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		prefix := "CREATE EXTENSION isn; CREATE TABLE ismn_staged(e ean13,v ismn); INSERT INTO ismn_staged VALUES('9780123456786','M123456785'); INSERT INTO ismn_staged VALUES('9780123456786',"
		bad := prefix + "'M123456780');"
		want := ean13Diagnostic("22P02", `invalid check digit for ISMN number: "M123456780", should be 5`)
		want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		if b.PG {
			s := fixture.ErrorDiagnostic(bad, want)
			s.ErrorStage = "execute"
			run(s)
		} else {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='ismn_staged'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		ismnControlSetup(t, p, b, c)
		ismnControlVerify(t, p, b, c)
		run(fixture.Exec("DROP TABLE ismn_control,ismn_required", 0))
		run(fixture.Exec("DROP EXTENSION isn", 0))
		ismnControlSetup(t, p, b, c)
		ismnControlVerify(t, p, b, c)
		if b.PG {
			return
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO ismn_control VALUES(99,'1400000000007','M999999997')", 1))
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
		old, metadata := isbnRetain(t, p, c, ismnControlQuery)
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn','ismn13','_ismn13','issn','_issn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ismnControlVerify(t, p, b, c)
		isbnConsumeRetained(t, old, metadata, ismnControlRows, "INT4", "EAN13", "ISMN")
		target := fixture.NewFixture(t, false, true)
		conn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		tb := fixture.Backend{Recorder: target.MemoryRecord, DB: target.Memory}
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE TABLE isbn_allocation_spacer(v integer)", 0))
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
		// Displace and prove all six scalar, six companion and twelve codec
		// identities before Restore, not just the old manifest's member zero.
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn"} {
			var source, dest [4]int64
			q := "SELECT oid,typarray,typinput::oid,typoutput::oid FROM pg_type WHERE oid=" + ean13Literal(typ) + "::regtype::oid"
			if err := c.QueryRowContext(p.Ctx, q).Scan(&source[0], &source[1], &source[2], &source[3]); err != nil {
				t.Fatal(err)
			}
			if err := conn.QueryRowContext(target.Ctx, q).Scan(&dest[0], &dest[1], &dest[2], &dest[3]); err != nil {
				t.Fatal(err)
			}
			for i := range source {
				if source[i] == dest[i] {
					t.Fatalf("destination %s role %d was not displaced", typ, i)
				}
			}
		}
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		ismnControlVerify(t, target, tb, conn)
		retainedIdentities := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn"} {
			retainedIdentities[typ] = isbnIdentityStamp(t, target, conn, typ)
		}
		// Every failed replacement preserves exact values, column descriptors,
		// and the previous installed identity graph, including eager old Rows.
		for _, kind := range []string{"invalid_literal", "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version", "isbn13_missing_member", "isbn13_duplicate_slot", "isbn13_wrong_type_role", "isbn13_wrong_output_signature", "ismn_missing_member", "ismn_duplicate_slot", "ismn_wrong_type_role", "ismn_wrong_output_signature", "canceled", "reader"} {
			t.Run(kind, func(t *testing.T) {
				retained, columns := isbnRetain(t, target, conn, ismnControlQuery)
				defer retained.Close()
				ctx := target.Ctx
				var reader io.Reader = strings.NewReader(bad)
				switch kind {
				case "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version", "isbn13_missing_member", "isbn13_duplicate_slot", "isbn13_wrong_type_role", "isbn13_wrong_output_signature", "ismn_missing_member", "ismn_duplicate_slot", "ismn_wrong_type_role", "ismn_wrong_output_signature":
					reader = strings.NewReader(isbnInvalidOwnerDump(t, dump.String(), kind))
				case "canceled":
					var cancel context.CancelFunc
					ctx, cancel = context.WithCancel(ctx)
					cancel()
					reader = bytes.NewReader(dump.Bytes())
				case "reader":
					reader = io.MultiReader(bytes.NewReader(dump.Bytes()), typeLifecycleErrorReader{io.ErrUnexpectedEOF})
				}
				err := stuntgres.Restore(ctx, target.Native, reader)
				switch kind {
				case "invalid_literal":
					fixture.AssertDiagnostic(t, err, &want)
				case "canceled":
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled Restore = %v", err)
					}
				case "reader":
					if !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("reader Restore = %v", err)
					}
				default:
					var state interface{ SQLState() string }
					if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
						t.Fatalf("invalid owner Restore must return ordinary control error: %v", err)
					}
				}
				ismnControlVerify(t, target, tb, conn)
				for typ, identity := range retainedIdentities {
					if got := isbnIdentityStamp(t, target, conn, typ); got != identity {
						t.Fatalf("failed Restore replaced %s identity: %v, want %v", typ, got, identity)
					}
				}
				isbnConsumeRetained(t, retained, columns, ismnControlRows, "INT4", "EAN13", "ISMN")
				var after bytes.Buffer
				if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(dump.Bytes(), after.Bytes()) {
					t.Fatal("failed Restore changed deterministic dump")
				}
			})
		}
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("same-target Restore changed deterministic dump")
		}
		if p.Postgres != nil {
			isbnReplay(t, p, dump.String(), nil, func(pg *sql.Conn, pb fixture.Backend) {
				ismnControlVerify(t, p, pb, pg)
			})
		}
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
			s := ean13Select(backend, "SELECT id,v FROM ismn_smoke ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISMN"}, expected, []bool{false, true})
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
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn"} {
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
		run(ean13Select(b, "SELECT id,v FROM ismn_smoke WHERE id>3 ORDER BY id", []string{"id", "v"}, []string{"INT4", "ISMN"}, [][]any{{int64(4), "M-1234-5678-5"}, {int64(5), "M-1234-5678-5!"}}, []bool{false, true}))
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
