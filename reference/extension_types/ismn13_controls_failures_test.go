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

func ismn13ControlBadStream() (string, fixture.Diagnostic) {
	prefix := "CREATE EXTENSION isn; CREATE TABLE ismn13_staged(e ean13,v ismn13); INSERT INTO ismn13_staged VALUES('9780123456786','M123456785'); INSERT INTO ismn13_staged VALUES('9780123456786',"
	want := ean13Diagnostic("22P02", `invalid check digit for ISMN number: "M123456780", should be 5`)
	want.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
	return prefix + "'M123456780');", want
}

// Every destination has its own driver and displaced identities, not source
// descriptor pointers or coincidentally identical allocator positions.
func ismn13ControlTarget(t *testing.T, p *fixture.Pair, c *sql.Conn, script []byte) (*fixture.Pair, fixture.Backend, *sql.Conn) {
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
	return target, tb, conn
 }

// Each failure owns a fresh fixture and its unchanged 30-second context. Slow
// inventories must not consume a single shared deadline across serial cases.
func ismn13FailedReplacement(t *testing.T, kind string) {
	ean13Scenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		if b.PG {
			// Restore is a native API; actual bad SQL stream behavior and actual
			// emitted SQL replay remain in the shared roundtrip phase.
			return
		}
		ismn13ControlSetup(t, p, b, c)
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		target, tb, conn := ismn13ControlTarget(t, p, c, dump.Bytes())
		retainedIdentities := map[string][4]int64{}
		for _, typ := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13"} {
			retainedIdentities[typ] = isbnIdentityStamp(t, target, conn, typ)
		}
		bad, want := ismn13ControlBadStream()
		retained, columns := isbnRetain(t, target, conn, ismn13ControlQuery)
		defer retained.Close()
		ctx := target.Ctx
		var reader io.Reader = strings.NewReader(bad)
		switch kind {
		case "missing_member", "duplicate_slot", "wrong_type_role", "wrong_output_signature", "wrong_version", "isbn13_missing_member", "isbn13_duplicate_slot", "isbn13_wrong_type_role", "isbn13_wrong_output_signature", "ismn_missing_member", "ismn_duplicate_slot", "ismn_wrong_type_role", "ismn_wrong_output_signature", "ismn13_missing_member", "ismn13_duplicate_slot", "ismn13_wrong_type_role", "ismn13_wrong_input_name", "ismn13_wrong_output_signature":
			reader = strings.NewReader(isbnInvalidOwnerDump(t, dump.String(), kind))
		case "canceled":
			var cancel context.CancelFunc
			ctx, cancel = context.WithCancel(ctx)
			cancel()
			reader = bytes.NewReader(dump.Bytes())
		case "reader":
			reader = io.MultiReader(bytes.NewReader(dump.Bytes()), fixture.TypeLifecycleErrorReader{io.ErrUnexpectedEOF})
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
		case "ismn13_wrong_input_name":
			// Procedure labels are mutable: valid metadata shape reaches
			// the importer, which rejects the mismatching installed identity.
			var state interface{ SQLState() string }
			if err == nil || err.Error() != "stuntgres: mismatching extension ownership identity" || errors.As(err, &state) {
				t.Fatalf("wrong input identity Restore must return exact ordinary control error: %v", err)
			}
		default:
			var state interface{ SQLState() string }
			if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
				t.Fatalf("invalid owner Restore must return ordinary control error: %v", err)
			}
		}
		ismn13ControlVerify(t, target, tb, conn)
		for typ, identity := range retainedIdentities {
			if got := isbnIdentityStamp(t, target, conn, typ); got != identity {
				t.Fatalf("failed Restore replaced %s identity: %v, want %v", typ, got, identity)
			}
		}
		isbnConsumeRetained(t, retained, columns, ismn13ControlRows, "INT4", "EAN13", "ISMN13")
		var after bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), after.Bytes()) {
			t.Fatal("failed Restore changed deterministic dump")
		}
	})
}
