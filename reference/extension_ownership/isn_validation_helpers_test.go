package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Independent manual/REL_18_6 inputs. Short UPC/ISSN display exceptions belong
// to their existing codec contract; the full EAN projection still carries !.
var isnValidationBad = []string{"2222222222221", "1234567890", "9781234567890", "M123456780", "9790123456780", "12345670", "9771234567000", "123456789010"}
var isnValidationDigit = []string{"2", "X", "7", "5", "5", "9", "3", "2"}

func isnValidationScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, schema := range []string{"eo_old", "eo_new", "eo_other"} {
		fixture.SchemaReferenceCleanup(t, schema)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			defer isnValidationCleanup(t, c)
			body(t, p, b, c)
		})
	}
}

func isnValidationCleanup(t *testing.T, c *sql.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
	defer cancel()
	for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Error("ISN validation cleanup", q, err)
		}
	}
	var mode sql.NullString
	if err := c.QueryRowContext(ctx, "SELECT current_setting('isn.weak',true)").Scan(&mode); err != nil {
		t.Error("ISN setting cleanup lookup", err)
	} else if mode.Valid {
		if _, err := c.ExecContext(ctx, "RESET isn.weak"); err != nil {
			t.Error("ISN setting cleanup reset", err)
		}
	}
}

func isnValidationBool(q string, v any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{v}}, args...)
}

func isnValidationMode(run func(fixture.Step), on bool) {
	run(isnValidationBool("SELECT isn_weak($1::boolean) AS v", on, on))
}

func isnValidationSetting(run func(fixture.Step), on bool) {
	value := "off"
	if on {
		value = "on"
	}
	run(fixture.Select("SHOW isn.weak", []string{"isn.weak"}, []string{"TEXT"}, [][]any{{value}}))
}

func isnValidationOutput(i int) string {
	if i == 0 {
		return "222-222222222-2"
	}
	return isnConversionRoles[i].output
}

func isnValidationEAN(i int) string {
	switch i {
	case 0:
		return "222-222222222-2"
	case 1, 2:
		return "978-1-234-56789-7"
	case 3, 4:
		return "979-0-1234-5678-5"
	case 5, 6:
		return "977-1234-567-00-3"
	default:
		return "012-345678901-2"
	}
}

func isnValidationChecksum(i int, input string) fixture.Diagnostic {
	return isnConversionDiagnostic("22P02", fmt.Sprintf("invalid check digit for %s number: %q, should be %s", isnConversionInputFamily(isnConversionRoles[i].name), input, isnValidationDigit[i]))
}

func isnValidationQueryError(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := isnConversionRuntimeError(q, d, args...)
	s.SelectRows = true
	return s
}

func isnValidationValueStep(b fixture.Backend, i int, expression string, input any, marked, null bool) fixture.Step {
	q := "SELECT " + expression + " AS original,is_valid(" + expression + ") AS valid,make_valid(" + expression + ") AS repaired,is_valid(make_valid(" + expression + ")) AS repaired_valid,(" + expression + ")::ean13::text AS full,is_valid(" + expression + ") AS original_still_valid"
	original, repaired, full := any(isnValidationOutput(i)), any(isnValidationOutput(i)), any(isnValidationEAN(i))
	valid, repairedValid := any(!marked), any(true)
	if marked {
		if !(b.PG && (i == 5 || i == 7)) {
			original = isnValidationOutput(i) + "!"
		}
		full = isnValidationEAN(i) + "!"
	}
	if null {
		original, repaired, full, valid, repairedValid = nil, nil, nil, nil, nil
	}
	return fixture.Ean13Select(b, q, []string{"original", "valid", "repaired", "repaired_valid", "full", "original_still_valid"}, []string{strings.ToUpper(isnConversionRoles[i].name), "BOOL", strings.ToUpper(isnConversionRoles[i].name), "BOOL", "TEXT", "BOOL"}, [][]any{{original, valid, repaired, repairedValid, full, valid}}, nil, input)
}
