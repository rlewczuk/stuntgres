package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

const arrayInspectInput = "[-2:-1][3:5]={{1,NULL,3},{4,5,6}}"
const arrayInspectHint = "No function matches the given name and argument types. You might need to add explicit type casts."

func arrayInspectScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func arrayInspectBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
}

func arrayInspectTuple(a, d string) string {
	return "array_dims(" + a + ") AS dims,array_length(" + a + "," + d + ") AS len,array_lower(" + a + "," + d + ") AS lo,array_upper(" + a + "," + d + ") AS hi,array_ndims(" + a + ") AS rank,cardinality(" + a + ") AS count"
}

func arrayInspectSelect(q string, rows [][]any, pg bool, args ...any) fixture.Step {
	s := fixture.Select(q, []string{"dims", "len", "lo", "hi", "rank", "count"}, []string{"TEXT", "INT4", "INT4", "INT4", "INT4", "INT4"}, rows, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		jsonTypeMetadataCheck(t, rows, pg, s.Columns, s.Types)
	}
	s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
		values := make([]any, len(s.Types))
		dest := make([]any, len(values))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			fixture.AssertRawValue(t, pg, s.Types[i], value)
		}
	}
	return s
}

// Stored view attributes are nullable source columns, not computed projections.
// pq independently reports unknown nullability; native retains the view schema.
func arrayInspectViewSelect(q string, rows [][]any, pg bool) fixture.Step {
	s := arrayInspectSelect(q, rows, pg)
	return jsonTypeWithMetadata(s, pg,
		jsonTypeSourceColumn{index: 0, nullable: true},
		jsonTypeSourceColumn{index: 1, nullable: true},
		jsonTypeSourceColumn{index: 2, nullable: true},
		jsonTypeSourceColumn{index: 3, nullable: true},
		jsonTypeSourceColumn{index: 4, nullable: true},
		jsonTypeSourceColumn{index: 5, nullable: true})
}

func arrayInspectDiagnostic(q, token, code, message, hint string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	d.Hint = hint
	if token != "" {
		at := strings.Index(q, token)
		if at < 0 {
			panic("missing diagnostic token")
		}
		d.Position = fixture.Position(utf8.RuneCountInString(q[:at]) + 1)
	}
	return d
}

func arrayInspectDiagnosticBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, d fixture.Diagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, d, args...)
		s.Prepared, s.SelectRows = prepared, true
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}
