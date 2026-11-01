package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Literal goldens come from the released pgcrypto regression vectors and RFC
// 4880's CRC24 witness, independently of the implementation under test.
const (
	pgcryptoArmorEmpty   = "-----BEGIN PGP MESSAGE-----\n\n=twTO\n-----END PGP MESSAGE-----\n"
	pgcryptoArmorTest    = "-----BEGIN PGP MESSAGE-----\n\ndGVzdA==\n=+G7Q\n-----END PGP MESSAGE-----\n"
	pgcryptoArmorZooka   = "-----BEGIN PGP MESSAGE-----\nfoo: bar\n\nem9va2E=\n=D5cR\n-----END PGP MESSAGE-----\n"
	pgcryptoArmorDigits  = "-----BEGIN PGP MESSAGE-----\n\nMTIzNDU2Nzg5\n=Ic8C\n-----END PGP MESSAGE-----\n"
	pgcryptoArmorLong    = "-----BEGIN PGP MESSAGE-----\n\nMDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWYwMTIzNDU2Nzg5YWJjZGVmCjAxMjM0NTY3\nODlhYmNkZWYwMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1Njc4OWFiY2RlZg==\n=JFw5\n-----END PGP MESSAGE-----\n"
	pgcryptoArmorBadBody = "-----BEGIN PGP MESSAGE-----\nComment: a\nVersion: b\n\n!not-base64!\n=ZZZZ\n-----END PGP MESSAGE-----\n"
)

func pgcryptoArmorBoth(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, s fixture.Step) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		step := s
		if prepared {
			name = "prepared"
			step = fixture.Prepared(step)
		}
		t.Run(name, func(t *testing.T) {
			t.Helper()
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("SQL=%s args=%#v", step.Query, step.Args)
				}
			})
			fixture.RunStep(t, p.Ctx, c, r, step)
		})
	}
}

func pgcryptoArmorText(query, want string, args ...any) fixture.Step {
	return fixture.Select(query, []string{"armor"}, []string{"TEXT"}, [][]any{{want}}, args...)
}

func pgcryptoArmorBytes(query string, want []byte, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"dearmor"}, []string{"BYTEA"}, [][]any{{string(want)}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 {
			t.Fatal("BYTEA column metadata", columns, err)
		}
		if columns[0].ScanType() != reflect.TypeOf([]byte{}) {
			t.Fatal("BYTEA scan type", columns[0].ScanType())
		}
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		t.Helper()
		var raw any
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		value, ok := raw.([]byte)
		if !ok || !bytes.Equal(value, want) {
			t.Fatalf("raw BYTEA %T %x, want []byte %x", raw, raw, want)
		}
	}
	return s
}

func pgcryptoArmorHeaders(query string, want [][]any, args ...any) fixture.Step {
	s := fixture.Select(query, []string{"key", "value"}, []string{"TEXT", "TEXT"}, want, args...)
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

// Remaining diagnostic fields are independently expected absent. The mandatory
// pinned-oracle checkpoint must resolve that expectation and execution stages.
func pgcryptoArmorError(query, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func pgcryptoArmorCorrupt(query string, args ...any) fixture.Step {
	return pgcryptoArmorError(query, "39000", "Corrupt ascii-armor", args...)
}
