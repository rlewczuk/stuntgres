//go:build reference

package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceNameExternalBoundaries(t *testing.T) {
	p := newReferencePair(t)
	var oid, length, array, collation int64
	var byval bool
	var alignment, category, input, output, receive, send string
	err := p.postgres.QueryRowContext(p.ctx, `SELECT oid::bigint,typlen::bigint,typbyval,typalign::text,typcategory::text,typinput::text,typoutput::text,typreceive::text,typsend::text,typcollation::bigint,typarray::bigint FROM pg_type WHERE typname='name'`).Scan(&oid, &length, &byval, &alignment, &category, &input, &output, &receive, &send, &collation, &array)
	if err != nil || oid != 19 || length != 64 || byval || alignment != "c" || category != "S" || input != "namein" || output != "nameout" || receive != "namerecv" || send != "namesend" || collation != 950 || array != 1003 {
		t.Fatal(oid, length, byval, alignment, category, input, output, receive, send, collation, array, err)
	}
	if _, err = p.postgres.ExecContext(p.ctx, "CREATE TABLE names(n NAME)"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.postgres.ExecContext(p.ctx, "INSERT INTO names VALUES($1)", strings.Repeat("x", 64)); err != nil {
		t.Fatal("text input must clip", err)
	}
	dsn, err := url.Parse(referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	options := dsn.Query()
	options.Set("binary_parameters", "yes")
	dsn.RawQuery = options.Encode()
	binary, err := sql.Open("postgres", dsn.String())
	if err != nil {
		t.Fatal(err)
	}
	defer binary.Close()
	if _, err = binary.ExecContext(p.ctx, "INSERT INTO names VALUES($1)", []byte("MiXeDé")); err != nil {
		t.Fatal(err)
	}
	_, err = binary.ExecContext(p.ctx, "INSERT INTO names VALUES($1)", []byte(strings.Repeat("x", 64)))
	want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42622", Message: "identifier too long", Detail: "Identifier must be less than 64 characters.", Context: "unnamed portal parameter $1"}
	referenceAssertDiagnostic(t, err, &want)
	var encoded string
	if err = p.postgres.QueryRowContext(p.ctx, "SELECT encode(namesend(n),'hex') FROM names WHERE n='MiXeDé'").Scan(&encoded); err != nil || encoded != hex.EncodeToString([]byte("MiXeDé")) {
		t.Fatal(encoded, err)
	}
	var typeOID, typmod, columnCollation int64
	if err = p.postgres.QueryRowContext(p.ctx, `SELECT atttypid::bigint,atttypmod::bigint,attcollation::bigint FROM pg_attribute WHERE attrelid='names'::regclass AND attname='n'`).Scan(&typeOID, &typmod, &columnCollation); err != nil || typeOID != 19 || typmod != -1 || columnCollation != 950 {
		t.Fatal(typeOID, typmod, columnCollation, err)
	}
}

func TestReferenceNameDump(t *testing.T) {
	p := newReferencePair(t)
	for _, q := range []string{`CREATE TABLE "MixedObject"(id INT,n NAME)`, "CREATE TABLE empty(n NAME)", "CREATE TABLE nulls(n NAME)", "INSERT INTO nulls VALUES(NULL)"} {
		if _, err := p.native.ExecContext(p.ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	long := strings.Repeat("a", 61) + "éZ"
	values := []any{"MiXeD", "O'Brien\\tail\n🙂", long, " x ", "", nil}
	want := [][]any{{int64(1), "MiXeD"}, {int64(2), "O'Brien\\tail\n🙂"}, {int64(3), strings.Repeat("a", 61) + "é"}, {int64(4), " x "}, {int64(5), ""}, {int64(6), nil}}
	for i, v := range values {
		if _, err := p.native.ExecContext(p.ctx, `INSERT INTO "MixedObject" VALUES($1,$2)`, i+1, v); err != nil {
			t.Fatal(err)
		}
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if _, err := p.postgres.ExecContext(p.ctx, dump.String()); err != nil {
		t.Fatal("native NAME dump replay", err)
	}
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.postgres, p.postgresRecord}, {p.memory, p.memoryRecord}} {
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect(`SELECT * FROM "MixedObject" ORDER BY id`, []string{"id", "n"}, []string{"INT4", "NAME"}, want))
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT n FROM nulls", []string{"n"}, []string{"NAME"}, [][]any{{nil}}))
		referenceRunStep(t, p.ctx, backend.db, backend.record, referenceSelect("SELECT n FROM empty", []string{"n"}, []string{"NAME"}, [][]any{}))
	}
}
