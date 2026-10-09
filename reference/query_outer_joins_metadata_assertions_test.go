package reference_test

import (
	"math"
	"strings"
	"testing"
)

type outerJoinsColumnMetadata struct {
	nullable, nullableKnown bool
	length                  int64
	lengthKnown             bool
	precision, scale        int64
	decimalKnown            bool
}

// pq does not expose declaration nullability. These are independently observed
// PostgreSQL adapter answers, not invented native nullability expectations.
var outerJoinsPostgresMetadata = map[string][]outerJoinsColumnMetadata{
	"SELECT * FROM narrow FULL JOIN wide USING(k)":                      {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM narrow LEFT JOIN wide USING(k)":                      {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM narrow RIGHT JOIN wide USING(k)":                     {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM narrow a FULL JOIN narrow b USING(k)":                {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM narrow a LEFT JOIN narrow b USING(k)":                {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM narrow a RIGHT JOIN narrow b USING(k)":               {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM typed_a FULL JOIN typed_b ON typed_a.k=typed_b.k":    {{false, false, 0, false, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 3, true, 0, 0, false}},
	"SELECT * FROM typed_a LEFT JOIN typed_b ON typed_a.k=typed_b.k":    {{false, false, 0, false, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 3, true, 0, 0, false}},
	"SELECT * FROM typed_a RIGHT JOIN typed_b ON typed_a.k=typed_b.k":   {{false, false, 0, false, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 0, false, 0, 0, false}, {false, false, 3, true, 0, 0, false}},
	"SELECT * FROM wide FULL JOIN narrow USING(k)":                      {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM wide LEFT JOIN narrow USING(k)":                      {{false, false, 0, false, 0, 0, false}},
	"SELECT * FROM wide RIGHT JOIN narrow USING(k)":                     {{false, false, 0, false, 0, 0, false}},
	"SELECT k,char_a.k,char_b.k FROM char_a FULL JOIN char_b USING(k)":  {{false, false, -5, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 5, true, 0, 0, false}},
	"SELECT k,char_a.k,char_b.k FROM char_a LEFT JOIN char_b USING(k)":  {{false, false, -5, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 5, true, 0, 0, false}},
	"SELECT k,char_a.k,char_b.k FROM char_a RIGHT JOIN char_b USING(k)": {{false, false, -5, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 5, true, 0, 0, false}},
	"SELECT k,char_a.k,char_c.k FROM char_a FULL JOIN char_c USING(k)":  {{false, false, 3, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}},
	"SELECT k,char_a.k,char_c.k FROM char_a LEFT JOIN char_c USING(k)":  {{false, false, 3, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}},
	"SELECT k,char_a.k,char_c.k FROM char_a RIGHT JOIN char_c USING(k)": {{false, false, 3, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}, {false, false, 3, true, 0, 0, false}},
}

var outerJoinsNativeMetadata = func() map[string][]outerJoinsColumnMetadata {
	out := make(map[string][]outerJoinsColumnMetadata)
	unknown := outerJoinsColumnMetadata{}
	notNull := outerJoinsColumnMetadata{nullableKnown: true}
	character := outerJoinsColumnMetadata{nullable: true, nullableKnown: true, length: 3, lengthKnown: true}
	for _, kind := range []string{"LEFT", "RIGHT", "FULL"} {
		narrowWide, wideNarrow, equal := unknown, unknown, unknown
		if kind == "RIGHT" {
			narrowWide = notNull
		}
		if kind == "LEFT" {
			wideNarrow = notNull
		}
		if kind != "FULL" {
			equal = notNull
		}
		out["SELECT * FROM narrow "+kind+" JOIN wide USING(k)"] = []outerJoinsColumnMetadata{narrowWide}
		out["SELECT * FROM wide "+kind+" JOIN narrow USING(k)"] = []outerJoinsColumnMetadata{wideNarrow}
		out["SELECT * FROM narrow a "+kind+" JOIN narrow b USING(k)"] = []outerJoinsColumnMetadata{equal}
		physical := []outerJoinsColumnMetadata{notNull, notNull, {nullableKnown: true, length: 3, lengthKnown: true}, notNull, notNull, {nullableKnown: true, length: 3, lengthKnown: true}}
		for i := range physical {
			if kind == "FULL" || kind == "LEFT" && i >= 3 || kind == "RIGHT" && i < 3 {
				physical[i].nullable = true
			}
		}
		out["SELECT * FROM typed_a "+kind+" JOIN typed_b ON typed_a.k=typed_b.k"] = physical
		for _, right := range []string{"char_b", "char_c"} {
			leftKey, rightKey := character, character
			if right == "char_b" {
				rightKey.length = 5
			}
			merged := character
			if right == "char_b" {
				merged = outerJoinsColumnMetadata{length: math.MaxInt64, lengthKnown: true}
			}
			if kind == "FULL" {
				merged.nullable, merged.nullableKnown = false, false
			}
			out["SELECT k,char_a.k,"+right+".k FROM char_a "+kind+" JOIN "+right+" USING(k)"] = []outerJoinsColumnMetadata{merged, leftKey, rightKey}
		}
	}
	return out
}()

func outerJoinsAssertMetadata(t *testing.T, p *referencePair, x referenceExecutor, recorder *referenceRecorder, query string, prepared bool) {
	t.Helper()
	outcome := referenceRowQuery(p.ctx, x, query, prepared)
	if outcome.stmt != nil {
		defer outcome.stmt.Close()
	}
	if outcome.rows != nil {
		defer outcome.rows.Close()
	}
	if outcome.err != nil {
		t.Fatal(outcome.err)
	}
	columns, err := outcome.rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	want, ok := outerJoinsPostgresMetadata[query]
	if recorder.rawBackend != referenceRawPostgres {
		want, ok = outerJoinsNativeMetadata[query]
	}
	if !ok || len(columns) != len(want) {
		t.Fatalf("missing metadata expectation: %s", query)
	}
	for i, col := range columns {
		nullable, nk := col.Nullable()
		length, lk := col.Length()
		precision, scale, dk := col.DecimalSize()
		got := outerJoinsColumnMetadata{nullable, nk, length, lk, precision, scale, dk}
		if got != want[i] {
			t.Errorf("%s column %d metadata got %#v want %#v", query, i, got, want[i])
		}
	}
}

func outerJoinsAssertViewDefinition(t *testing.T, name, definition string) {
	t.Helper()
	// PostgreSQL and native canonical whitespace/projections differ. Require the
	// observed semantic kind and frozen condition, while result bags prove meaning.
	words := strings.ToUpper(strings.Join(strings.Fields(strings.NewReplacer("(", " ", ")", " ", ";", " ", "\"", " ").Replace(definition)), " "))
	upper := strings.ToUpper(name)
	if upper == "NESTED" {
		if !strings.Contains(words, "V_FULL_NATURAL") {
			t.Fatal("nested view lost its source", definition)
		}
		return
	}
	kind := "FULL"
	if strings.Contains(upper, "LEFT") || upper == "NO_KEYS" {
		kind = "LEFT"
	}
	if strings.Contains(upper, "RIGHT") {
		kind = "RIGHT"
	}
	words = strings.ReplaceAll(words, " OUTER JOIN ", " JOIN ")
	if !strings.Contains(words, kind+" JOIN ") || strings.Contains(words, "NATURAL") || strings.Contains(words, "CROSS JOIN") {
		t.Fatal("view lost analyzed outer kind", definition)
	}
	if upper == "NO_KEYS" {
		if !strings.Contains(words, "ON TRUE") {
			t.Fatal("zero-key outer view lost preservation", definition)
		}
	} else if strings.HasSuffix(upper, "_ON") {
		if !strings.Contains(words, " ON ") || !strings.Contains(words, "=") {
			t.Fatal("view lost ON predicate", definition)
		}
	} else if !strings.Contains(words, " USING ") {
		t.Fatal("view lost frozen USING keys", definition)
	}
}
