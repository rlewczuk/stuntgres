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

func issn13ControlBadStream() (string, fixture.Diagnostic) {
	prefix := "CREATE EXTENSION isn; CREATE TABLE issn13_staged(e ean13,v issn13); INSERT INTO issn13_staged VALUES('9780123456786','12345679'); INSERT INTO issn13_staged VALUES('9780123456786',"
	want := ean13Diagnostic("22P02", `invalid check digit for ISSN number: "12345670", should be 9`)
	want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
	return prefix + "'12345670');", want
}

// Every destination has its own driver and displaced identities, not source
// descriptor pointers or coincidentally identical allocator positions.
func issn13ControlTarget(t *testing.T, p *fixture.Pair, c *sql.Conn, script []byte) (*fixture.Pair, fixture.Backend, *sql.Conn) {
	target := fixture.NewFixture(t, false, true)
	conn, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	tb := fixture.Backend{Recorder: target.MemoryRecord, DB: target.Memory}
	fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE TABLE isbn_allocation_spacer(v integer)", 0))
	fixture.RunStep(t, target.Ctx, conn, tb.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
	// Displace and prove all seven scalar, seven companion and fourteen codec
	// identities before Restore, not just the old manifest's member zero.
	for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
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
	if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	issn13ControlVerify(t, target, tb, conn)
	return target, tb, conn
 }

// Each failure owns a fresh fixture and its unchanged 30-second context. Slow
// inventories must not consume a single shared deadline across serial cases.
func issn13FailedReplacement(t *testing.T, kind string) {
	issn13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			// Restore is a native API; actual bad SQL stream behavior and actual
			// emitted SQL replay remain in the shared roundtrip phase.
			return
		}
		issn13ControlSetup(t, p, b, c)
		issn13ControlVerify(t, p, b, c)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		target, tb, conn := issn13ControlTarget(t, p, c, dump.Bytes())
		retainedIdentities := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
			retainedIdentities[typ] = isbnIdentityStamp(t, target, conn, typ)
		}
		var extension int64
		if err := conn.QueryRowContext(target.Ctx, "SELECT oid FROM pg_extension WHERE extname='isn'").Scan(&extension); err != nil {
			t.Fatal(err)
		}
		bad, want := issn13ControlBadStream()
		retained, columns := isbnRetain(t, target, conn, issn13ControlQuery)
		defer retained.Close()
		ctx := target.Ctx
		var reader io.Reader = strings.NewReader(bad)
		switch kind {
		case "issn13_missing_member", "issn13_duplicate_slot", "issn13_wrong_type_role", "issn13_wrong_output_signature", "wrong_version":
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
		issn13ControlVerify(t, target, tb, conn)
		for typ, identity := range retainedIdentities {
			if got := isbnIdentityStamp(t, target, conn, typ); got != identity {
				t.Fatalf("failed Restore replaced %s identity: %v, want %v", typ, got, identity)
			}
		}
		var afterExtension int64
		if err := conn.QueryRowContext(target.Ctx, "SELECT oid FROM pg_extension WHERE extname='isn'").Scan(&afterExtension); err != nil || afterExtension != extension {
			t.Fatalf("failed Restore replaced extension identity: %d, want %d; %v", afterExtension, extension, err)
		}
		isbnConsumeRetained(t, retained, columns, issn13ControlRows, "INT4", "EAN13", "ISSN13")
		var after bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), after.Bytes()) {
			t.Fatal("failed Restore changed deterministic dump")
		}
	})
}