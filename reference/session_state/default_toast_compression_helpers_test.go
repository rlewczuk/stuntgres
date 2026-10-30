package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const toastSetting = "default_toast_compression"
const toastBoundary = "pg_column_compression physical TOAST compression inspection is unsupported in embedded mode"

func toastScenario(t *testing.T, check func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			check(t, p, backend, c)
		})
	}
}

func toastRun(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
}

func toastText(pg bool, q, column string, value any) fixture.Step {
	s := fixture.Select(q, []string{column}, []string{"TEXT"}, [][]any{{value}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		settingsTextMetadata(t, rows, pg, strings.HasPrefix(q, "SELECT"))
	}
	return s
}

func toastInquiryText(q string, value any) fixture.Step {
	s := fixture.Select(q, []string{"compression"}, []string{"TEXT"}, [][]any{{value}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 {
			t.Fatal(types, err)
		}
		column := types[0]
		if column.DatabaseTypeName() != "TEXT" || column.ScanType() != reflect.TypeFor[string]() {
			t.Fatal("inquiry TEXT carrier", column)
		}
		// An ordinary computed inquiry has no top-level nullability claim.
		// This differs from SHOW/current_setting's settings-specific metadata.
		if nullable, known := column.Nullable(); nullable || known {
			t.Fatal("computed inquiry nullability", nullable, known)
		}
		if length, known := column.Length(); length != math.MaxInt64 || !known {
			t.Fatal("inquiry TEXT length", length, known)
		}
		if precision, scale, known := column.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("inquiry TEXT decimal metadata", precision, scale, known)
		}
	}
	return s
}

func toastState(pg bool, value, source string) []fixture.Step {
	return []fixture.Step{
		toastText(pg, "SHOW default_toast_compression", toastSetting, value),
		toastText(pg, "SELECT current_setting('default_toast_compression')", "current_setting", value),
		fixture.Select("SELECT setting,source FROM pg_settings WHERE name='default_toast_compression'", []string{"setting", "source"}, []string{"TEXT", "TEXT"}, [][]any{{value, source}}),
	}
}

func toastFailure(q, code, message string) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
	s.SelectRows = strings.HasPrefix(q, "SELECT")
	return s
}

func toastInvalid(q, value string) fixture.Step {
	return fixture.ImplicitError(q, fixture.Diagnostic{
		Code: "22023", Message: `invalid value for parameter "default_toast_compression": "` + value + `"`,
		Hint: "Available values: pglz, lz4.",
	})
}

func toastPayload() string { return strings.Repeat("toast-compressible-", 2048) }

func toastRows(table string, count int) fixture.Step {
	rows := make([][]any, count)
	for i := range rows {
		rows[i] = []any{int64(i + 1), toastPayload()}
	}
	return fixture.Select("SELECT id,payload FROM "+table+" ORDER BY id", []string{"id", "payload"}, []string{"INT4", "TEXT"}, rows)
}

func toastPolicy(table, compression string) fixture.Step {
	return fixture.Select("SELECT attstorage,attcompression FROM pg_attribute WHERE attrelid='"+table+"'::regclass AND attname='payload'", []string{"attstorage", "attcompression"}, []string{"CHAR", "CHAR"}, [][]any{{"x", compression}})
}

func toastDemand(pg bool, q string, value any, args ...any) fixture.Step {
	if !pg {
		s := toastFailure(q, "0A000", toastBoundary)
		s.Args = args
		s.ErrorStage = "execute"
		return s
	}
	return fixture.Select(q, []string{"compression"}, []string{"TEXT"}, [][]any{{value}}, args...)
}

func toastHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	statement, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal("prepare without physical demand", err)
	}
	defer statement.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: statement, SQL: s.Query}, b.Recorder, s)
}
