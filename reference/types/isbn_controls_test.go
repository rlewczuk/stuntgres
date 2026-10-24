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

func TestReferenceIsbnControls(t *testing.T) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		prefix := "CREATE EXTENSION isn; CREATE TABLE isbn_staged(e ean13,v isbn); INSERT INTO isbn_staged VALUES('9780123456786','123456789X'); INSERT INTO isbn_staged VALUES('9780123456786',"
		bad := prefix + "'1234567890');"
		want := ean13Diagnostic("22P02", isbnFailures[0].message)
		want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		if b.PG {
			s := fixture.ErrorDiagnostic(bad, want)
			s.ErrorStage = "execute"
			run(s)
		} else {
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
		}
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='isbn_staged'", []string{"relname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		isbnControlSetup(t, p, b, c)
		isbnControlVerify(t, p, b, c)
		if b.PG {
			run(fixture.Exec("DROP TABLE isbn_control,isbn_required", 0))
			run(fixture.Exec("DROP EXTENSION isn", 0))
			isbnControlSetup(t, p, b, c)
			isbnControlVerify(t, p, b, c)
			return
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO isbn_control VALUES(99,'1400000000007','9791000000008')", 1))
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
		old, metadata := isbnRetain(t, p, c, isbnControlQuery)
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='isn'", []string{"extname"}, []string{"NAME"}, [][]any{}))
		run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='public'::regnamespace AND typname IN ('ean13','_ean13','isbn','_isbn','isbn13','_isbn13','ismn','_ismn')", []string{"typname"}, []string{"NAME"}, [][]any{}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		isbnControlVerify(t, p, b, c)
		isbnConsumeRetained(t, old, metadata, isbnControlRows)
		target := fixture.NewFixture(t, false, true)
		conn, err := target.Memory.Conn(target.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		tb := fixture.Backend{Recorder: target.MemoryRecord, DB: target.Memory}
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE TABLE isbn_allocation_spacer(v integer)", 0))
		fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
		// Displace and prove all four scalar, four companion and eight codec
		// identities before Restore, not just the old manifest's member zero.
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn"} {
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
		isbnControlVerify(t, target, tb, conn)
		retainedIdentities := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn"} {
			retainedIdentities[typ] = isbnIdentityStamp(t, target, conn, typ)
		}
		// Every failed replacement preserves exact values, column descriptors,
		// and the previous installed identity graph, including eager old Rows.
		for _, kind := range []string{"invalid_literal", "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version", "canceled", "reader"} {
			t.Run(kind, func(t *testing.T) {
				retained, columns := isbnRetain(t, target, conn, isbnControlQuery)
				defer retained.Close()
				ctx := target.Ctx
				var reader io.Reader = strings.NewReader(bad)
				switch kind {
				case "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version":
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
				isbnControlVerify(t, target, tb, conn)
				for typ, identity := range retainedIdentities {
					if got := isbnIdentityStamp(t, target, conn, typ); got != identity {
						t.Fatalf("failed Restore replaced %s identity: %v, want %v", typ, got, identity)
					}
				}
				isbnConsumeRetained(t, retained, columns, isbnControlRows)
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
				isbnControlVerify(t, p, pb, pg)
			})
		}
	})
}
