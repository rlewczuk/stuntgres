//go:build reference

package reference_test

import (
	"database/sql"
	"encoding/hex"
	"net/url"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNameExternalBoundaries(t *testing.T) {
	p := fixture.NewPair(t)
	if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE TABLE names(n NAME)"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Postgres.ExecContext(p.Ctx, "INSERT INTO names VALUES($1)", strings.Repeat("x", 64)); err != nil {
		t.Fatal("text input must clip", err)
	}
	dsn, err := url.Parse(fixture.DSN)
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
	if _, err = binary.ExecContext(p.Ctx, "INSERT INTO names VALUES($1)", []byte("MiXeDé")); err != nil {
		t.Fatal(err)
	}
	_, err = binary.ExecContext(p.Ctx, "INSERT INTO names VALUES($1)", []byte(strings.Repeat("x", 64)))
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42622", Message: "identifier too long", Detail: "Identifier must be less than 64 characters.", Context: "unnamed portal parameter $1"}
	fixture.AssertDiagnostic(t, err, &want)
	var encoded string
	if err = p.Postgres.QueryRowContext(p.Ctx, "SELECT encode(namesend(n),'hex') FROM names WHERE n='MiXeDé'").Scan(&encoded); err != nil || encoded != hex.EncodeToString([]byte("MiXeDé")) {
		t.Fatal(encoded, err)
	}
	var typeOID, typmod, columnCollation int64
	if err = p.Postgres.QueryRowContext(p.Ctx, `SELECT atttypid::bigint,atttypmod::bigint,attcollation::bigint FROM pg_attribute WHERE attrelid='names'::regclass AND attname='n'`).Scan(&typeOID, &typmod, &columnCollation); err != nil || typeOID != 19 || typmod != -1 || columnCollation != 950 {
		t.Fatal(typeOID, typmod, columnCollation, err)
	}
}
