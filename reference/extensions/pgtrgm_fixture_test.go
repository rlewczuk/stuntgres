package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// All expected values are independently derived from the PG18 manual and
// REL_18_6 sources, under the fixture's UTF8/C profile, not native execution.
// Each callback owns a genuinely new physical connection, including in PG mode;
// DISCARD ALL and resetting public do not unload a backend's C libraries.
func pgTrgmCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *[]fixture.Diagnostic)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"trgm_reader", "trgm_owner"})
	fixture.SchemaReferenceCleanup(t, "trgm_addon")
	fixture.SchemaReferenceCleanup(t, "trgm_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = recorder
			body(t, p, b, c, &notices)
		})
	}
}

func pgTrgmRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) func(fixture.Step) {
	t.Helper()
	return func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
}

func pgTrgmLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// lib/pq parses PostgreSQL's shortest float4 decimal; native exports the exact
// widened binary32. Specify both transport contracts, not a tolerance/parity.
func pgTrgmReal(pg bool, n, d int) float64 {
	v := float32(n) / float32(d)
	return pgTrgmFloat(pg, v)
}

func pgTrgmFloat(pg bool, v float32) float64 {
	if !pg {
		return float64(v)
	}
	f, err := strconv.ParseFloat(strconv.FormatFloat(float64(v), 'g', -1, 32), 64)
	if err != nil {
		panic(err)
	}
	return f
}

// Independent distinct-set counts for the same written inputs word,two words:
// ordinary4/11, forward word4/5, reverse word2/5, strict4/7, reverse strict4/11.
// Subtract only after rounding the score to float4.
func pgTrgmDistanceValues(b fixture.Backend) []any {
	values := make([]any, 0, 5)
	for _, ratio := range [][2]int{{4, 11}, {4, 5}, {2, 5}, {4, 7}, {4, 11}} {
		score := float32(ratio[0]) / float32(ratio[1])
		values = append(values, pgTrgmFloat(b.PG, 1-score))
	}
	return values
}

func pgTrgmDistanceFrame(b fixture.Backend) fixture.Step {
	var parts, names, types []string
	var values []any
	distances := pgTrgmDistanceValues(b)
	i := 0
	for _, op := range pgTrgmOperators {
		if op.result != "FLOAT4" {
			continue
		}
		parts = append(parts,
			"$1::text OPERATOR(trgm_addon."+op.token+") $2::text AS distance_"+strconv.Itoa(i),
			"trgm_addon."+op.routine+"($1,$2) AS backing_"+strconv.Itoa(i))
		names = append(names, "distance_"+strconv.Itoa(i), "backing_"+strconv.Itoa(i))
		types = append(types, "FLOAT4", "FLOAT4")
		values = append(values, distances[i], distances[i])
		i++
	}
	return pgTrgmSelect(b, "SELECT "+strings.Join(parts, ","), names, types, [][]any{values}, "word", "two words")
}

func pgTrgmSelect(b fixture.Backend, q string, names, types []string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, names, types, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != len(types) {
			t.Fatal("result descriptors", columns, err)
		}
		for i, c := range columns {
			if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
				t.Fatalf("column %d: %s %s", i, c.Name(), c.DatabaseTypeName())
			}
			var want reflect.Type
			switch types[i] {
			case "FLOAT4":
				want = reflect.TypeFor[float64]()
				if b.PG {
					want = reflect.TypeFor[float32]()
				}
			case "BOOL":
				want = reflect.TypeFor[bool]()
			case "_TEXT":
				want = reflect.TypeFor[string]()
				if b.PG {
					want = reflect.TypeFor[interface{}]()
				}
			}
			if want != nil && c.ScanType() != want {
				t.Fatalf("%s ScanType=%v want %v", c.Name(), c.ScanType(), want)
			}
			if nullable, known := c.Nullable(); known || nullable {
				t.Fatalf("computed %s nullability=(%v,%v), want unknown", c.Name(), nullable, known)
			}
			if _, known := c.Length(); known {
				t.Fatalf("computed %s unexpected length", c.Name())
			}
			if _, _, known := c.DecimalSize(); known {
				t.Fatalf("computed %s unexpected decimal size", c.Name())
			}
		}
	}
	return s
}

func pgTrgmScalar(b fixture.Backend, q, name, typ string, value any, args ...any) fixture.Step {
	return pgTrgmSelect(b, q, []string{name}, []string{typ}, [][]any{{value}}, args...)
}

func pgTrgmArray(elements ...string) string {
	parts := make([]string, len(elements))
	for i, s := range elements {
		parts[i] = s
		if s == "" || strings.EqualFold(s, "NULL") || strings.ContainsAny(s, " \t\r\n{},\"\\") {
			parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
		}
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func pgTrgmFailure(q string, d fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, d, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func pgTrgmNotices(t *testing.T, notices *[]fixture.Diagnostic, want ...fixture.Diagnostic) {
	t.Helper()
	for i := range want {
		if want[i].Severity == "" {
			want[i].Severity, want[i].SeverityUnlocalized = "WARNING", "WARNING"
		}
	}
	if len(*notices) != len(want) {
		t.Fatalf("notices=%#v want=%#v", *notices, want)
	}
	for i := range want {
		if !reflect.DeepEqual((*notices)[i], want[i]) {
			t.Fatalf("notice %d=%#v want=%#v", i, (*notices)[i], want[i])
		}
	}
	*notices = nil
}

var pgTrgmSettings = []struct{ name, boot, op string }{
	{"pg_trgm.similarity_threshold", "0.3", "%"},
	{"pg_trgm.word_similarity_threshold", "0.6", "<%"},
	{"pg_trgm.strict_word_similarity_threshold", "0.5", "<<%"},
}

func pgTrgmSettingValue(run func(fixture.Step), name string, value any) {
	run(fixture.Select("SELECT current_setting($1,true)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{value}}, name))
}

func pgTrgmLoaded(run func(fixture.Step), loaded bool) {
	count := int64(0)
	if loaded {
		count = 3
	}
	run(fixture.Select("SELECT count(*) FROM pg_settings WHERE name IN ('pg_trgm.similarity_threshold','pg_trgm.word_similarity_threshold','pg_trgm.strict_word_similarity_threshold')", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
}

func pgTrgmActivate(run func(fixture.Step), b fixture.Backend) {
	run(pgTrgmScalar(b, "SELECT similarity('cat','cat')", "similarity", "FLOAT4", float64(1)))
}

func pgTrgmInstalledElsewhere(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE EXTENSION pg_trgm", 0))
}

func pgTrgmBadSetting(name, value string) fixture.Diagnostic {
	return fixture.Diagnostic{Code: "22023", Message: fmt.Sprintf("invalid value for parameter %q: %q", name, value)}
}
