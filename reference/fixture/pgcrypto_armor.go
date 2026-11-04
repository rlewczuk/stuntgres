package fixture

import (
	"database/sql"
	"testing"
)

// PgcryptoArmorZooka is a released pgcrypto regression vector, independent of
// the implementation under test.
const PgcryptoArmorZooka = "-----BEGIN PGP MESSAGE-----\nfoo: bar\n\nem9va2E=\n=D5cR\n-----END PGP MESSAGE-----\n"

func PgcryptoArmorHeaders(query string, want [][]any, args ...any) Step {
	s := Select(query, []string{"key", "value"}, []string{"TEXT", "TEXT"}, want, args...)
	s.Bag = true
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 2 {
			t.Fatal("header metadata", columns, err)
		}
		for i, name := range []string{"key", "value"} {
			if columns[i].Name() != name || columns[i].DatabaseTypeName() != "TEXT" {
				t.Fatal("header column", columns[i])
			}
			if nullable, known := columns[i].Nullable(); nullable || known {
				t.Fatal("computed source nullability", nullable, known)
			}
		}
	}
	return s
}

// PgcryptoArmorError requires every unlisted diagnostic field to be absent.
func PgcryptoArmorError(query, code, message string, args ...any) Step {
	s := ImplicitError(query, Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func PgcryptoArmorCorrupt(query string, args ...any) Step {
	return PgcryptoArmorError(query, "39000", "Corrupt ascii-armor", args...)
}
