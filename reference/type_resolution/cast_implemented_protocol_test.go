package reference_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres/reference/fixture"
)

func castProtocolOID(t *testing.T, f *fixture.ServerFixture, b fixture.Backend, fact castImplementedType) uint32 {
	t.Helper()
	if fact.oid != 0 {
		return uint32(fact.oid)
	}
	var oid int64
	q := "SELECT oid::int8 FROM pg_type WHERE oid=" + comparisonSQLString(castInventoryIdentity(fact)) + "::regtype"
	if err := b.DB.QueryRowContext(f.Ctx, q).Scan(&oid); err != nil {
		t.Fatal(err)
	}
	if oid <= 0 || oid > 4294967295 {
		t.Fatalf("invalid owned OID %d", oid)
	}
	return uint32(oid)
}
func castProtocolFields(t *testing.T, fields []pgconn.FieldDescription, oid uint32, typmod int32) {
	t.Helper()
	if len(fields) != 1 || fields[0].Name != "v" || fields[0].DataTypeOID != oid || fields[0].TypeModifier != typmod || fields[0].Format != 0 || fields[0].TableOID != 0 || fields[0].TableAttributeNumber != 0 {
		t.Fatalf("wire fields=%+v want expression v OID=%d typmod=%d format0", fields, oid, typmod)
	}
}
func castProtocolError(t *testing.T, err error, want *fixture.Diagnostic) {
	t.Helper()
	expected := *want
	expected.Severity, expected.SeverityUnlocalized = "ERROR", "ERROR"
	fixture.AssertDiagnostic(t, err, &expected)
}
