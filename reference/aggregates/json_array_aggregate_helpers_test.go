package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Root pinned the core PG18.0 contract; additional independently specified
// boundary/coercion witnesses require fresh root proof in both modes.
func jsonArrayAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func jsonArrayAggregateSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE ja_input(id int,g text,v int,j json,b jsonb)", 0),
		fixture.Exec(`INSERT INTO ja_input VALUES (1,'a',2,' {"z":1,"a":1,"a":2} ','{"z":1,"a":1,"a":2}'),(2,'a',NULL,'null','null'),(3,'a',1,NULL,NULL),(4,'b',NULL,NULL,NULL)`, 4),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func jsonArrayAggregateRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	onlyJSON := len(s.Types) > 0
	for _, typ := range s.Types {
		onlyJSON = onlyJSON && (typ == "JSON" || typ == "JSONB")
	}
	// Restrict computed nullability to aggregate projections, not CTAS columns.
	if s.CheckMetadata == nil && onlyJSON && strings.HasPrefix(s.Query, "SELECT json") {
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			jsonArrayAggregateMetadata(t, rows, b.PG, s.Columns, s.Types, nil)
		}
	}
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		})
	}
}

func jsonArrayAggregateSelect(q, typ string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{typ}, [][]any{{value}}, args...)
}

func jsonArrayAggregatePosition(q, token string) string {
	index := strings.Index(q, token)
	if index < 0 {
		panic("missing diagnostic token " + token)
	}
	return fixture.Position(utf8.RuneCountInString(q[:index]) + 1)
}

func jsonArrayAggregateError(t *testing.T, p *fixture.Pair, b fixture.Backend, q string, d fixture.Diagnostic, bind bool, args ...any) {
	t.Helper()
	// Both discarded-row Exec and Query must surface the complete diagnosis.
	for _, query := range []bool{true, false} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{true: "query", false: "exec"}[query]+"/"+map[bool]string{true: "prepared", false: "direct"}[prepared], func(t *testing.T) {
				s := fixture.ErrorDiagnostic(q, d, args...)
				s.SelectRows, s.Prepared, s.ErrorStage = query, prepared, "execute"
				if prepared && bind && !strings.HasPrefix(q, "CREATE ") {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			})
		}
	}
}

// Public metadata is inspected before Next, not inferred from a returned cell.
func jsonArrayAggregateMetadata(t *testing.T, rows *sql.Rows, pg bool, names, types []string, lengths []int64) {
	t.Helper()
	jsonArrayAggregateMetadataContract(t,rows,pg,names,types,lengths,false,false)
}

// Stored view output has source-schema authority: bindViewOutput deliberately
// publishes nullable/known columns. pq does not expose that server descriptor.
func jsonArrayAggregateViewMetadata(t *testing.T,rows *sql.Rows,pg bool,names,types []string,lengths []int64) {
	t.Helper()
	jsonArrayAggregateMetadataContract(t,rows,pg,names,types,lengths,!pg,!pg)
}

func jsonArrayAggregateMetadataContract(t *testing.T,rows *sql.Rows,pg bool,names,types []string,lengths []int64,nullable,nullableKnown bool) {
	t.Helper()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, names) {
		t.Fatalf("columns=%v want=%v err=%v", columns, names, err)
	}
	cts, err := rows.ColumnTypes()
	if err != nil || len(cts) != len(types) {
		t.Fatalf("metadata=%v err=%v", cts, err)
	}
	for i, ct := range cts {
		wantScan := reflect.TypeFor[string]()
		if types[i] == "BYTEA" {
			wantScan = reflect.TypeFor[[]byte]()
		} else if pg && (types[i] == "JSON" || types[i] == "JSONB" || types[i] == "BPCHAR") {
			wantScan = reflect.TypeFor[any]()
		}
		if ct.Name() != names[i] || ct.DatabaseTypeName() != types[i] || ct.ScanType() != wantScan {
			t.Fatalf("column%d=%q/%q/%v want=%q/%q/%v", i, ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), names[i], types[i], wantScan)
		}
		if n, known := ct.Nullable(); n!=nullable || known!=nullableKnown {
			t.Fatalf("column%d Nullable=%v,%v want=%v,%v", i, n, known,nullable,nullableKnown)
		}
		if n, scale, known := ct.DecimalSize(); known {
			t.Fatalf("column%d DecimalSize=%d,%d want unknown", i, n, scale)
		}
		length, known := ct.Length()
		wantLength, wantKnown := int64(0), false
		if types[i] == "TEXT" || (pg && types[i] == "BYTEA") {
			wantLength, wantKnown = math.MaxInt64, true
		} else if lengths != nil && lengths[i] >= 0 {
			wantLength, wantKnown = lengths[i], true
		}
		if known != wantKnown || (known && length != wantLength) {
			t.Fatalf("column%d Length=%d,%v want=%d,%v", i, length, known, wantLength, wantKnown)
		}
	}
}
