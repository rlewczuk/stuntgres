package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

const mcvItemsArgument = "(SELECT d.stxdmcv FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxname=$1 AND NOT d.stxdinherit)"
const mcvItemsQuery = "SELECT m.* FROM pg_mcv_list_items(" + mcvItemsArgument + ") m ORDER BY m.index"

var mcvItemsNames = []string{"index", "values", "nulls", "frequency", "base_frequency"}
var mcvItemsTypes = []string{"INT4", "_TEXT", "_BOOL", "FLOAT8", "FLOAT8"}

type mcvItemsExpected struct {
	values              []any // nil is an actual SQL NULL array element, not the string NULL.
	frequency, baseline float64
}

type mcvItemsObserved struct {
	index               int64
	values              []sql.NullString
	nulls               []bool
	frequency, baseline float64
	rawValues, rawNulls string
}

var mcvItemsFour = []mcvItemsExpected{
	{[]any{"1", "1"}, .5, .25},
	{[]any{"2", "2"}, .25, .0625},
	{[]any{nil, nil}, .25, .0625},
}
var mcvItemsFive = []mcvItemsExpected{
	{[]any{"1", "1"}, .4, .16},
	{[]any{"2", "2"}, .4, .16},
	{[]any{nil, nil}, .2, .04},
}

// Expected array text follows the released array output grammar, independently
// of native type codecs and of the analyzed binary-image decoder.
func mcvItemsArray(values []any) string {
	parts := make([]string, len(values))
	for i, value := range values {
		if value == nil {
			parts[i] = "NULL"
			continue
		}
		text := value.(string)
		if text == "" || strings.EqualFold(text, "NULL") || strings.ContainsAny(text, "{},\"\\ \t\r\n") {
			text = `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(text) + `"`
		}
		parts[i] = text
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func mcvItemsRead(t *testing.T, ctx context.Context, s ndistinctScenario, name string) []mcvItemsObserved {
	t.Helper()
	var rows *sql.Rows
	var err error
	if s.prepared {
		stmt, prepareErr := s.conn.PrepareContext(ctx, mcvItemsQuery)
		if prepareErr != nil {
			t.Fatal(prepareErr)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(ctx, name)
	} else {
		rows, err = s.conn.QueryContext(ctx, mcvItemsQuery, name)
	}
	if err != nil {
		t.Fatal(err)
	}
	return mcvItemsConsume(t, rows, s.pg)
}

func mcvItemsConsume(t *testing.T, rows *sql.Rows, pg bool) []mcvItemsObserved {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, mcvItemsNames) {
		t.Fatalf("item names %v: %v", names, err)
	}
	meta, err := rows.ColumnTypes()
	if err != nil || len(meta) != 5 {
		t.Fatal(meta, err)
	}
	for i, typ := range mcvItemsTypes {
		if meta[i].DatabaseTypeName() != typ {
			t.Fatalf("item column %d type %q want %q", i, meta[i].DatabaseTypeName(), typ)
		}
	}
	var out []mcvItemsObserved
	for rows.Next() {
		var item mcvItemsObserved
		var values, nulls any
		if err := rows.Scan(&item.index, &values, &nulls, &item.frequency, &item.baseline); err != nil {
			t.Fatal(err)
		}
		for _, raw := range []any{values, nulls} {
			if pg {
				if _, ok := raw.([]byte); !ok {
					t.Fatalf("pq array carrier %T want []byte", raw)
				}
			} else if _, ok := raw.(string); !ok {
				t.Fatalf("native array carrier %T want string", raw)
			}
		}
		item.rawValues = fixture.Normalize(values).(string)
		item.rawNulls = fixture.Normalize(nulls).(string)
		if err := pq.Array(&item.values).Scan(values); err != nil {
			t.Fatal(err)
		}
		if err := pq.Array(&item.nulls).Scan(nulls); err != nil {
			t.Fatal(err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func mcvItemsAssert(t *testing.T, got []mcvItemsObserved, want ...mcvItemsExpected) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("items %#v want %#v", got, want)
	}
	seen := make([]bool, len(want))
	for position, item := range got {
		if item.index != int64(position) {
			t.Fatalf("list index %d at position %d", item.index, position)
		}
		match := -1
		for i, expected := range want {
			if !seen[i] && item.rawValues == mcvItemsArray(expected.values) {
				match = i
				break
			}
		}
		if match < 0 {
			t.Fatalf("unexpected/duplicate item %#v", item)
		}
		seen[match] = true
		expected := want[match]
		if len(item.values) != len(expected.values) || len(item.nulls) != len(expected.values) {
			t.Fatalf("array dimensions %#v", item)
		}
		flags := make([]string, len(expected.values))
		for i, value := range expected.values {
			null := value == nil
			if item.nulls[i] != null || item.values[i].Valid == null || (!null && item.values[i].String != value.(string)) {
				t.Fatalf("item dimension %d: %#v want %#v", i, item, value)
			}
			flags[i] = "f"
			if null {
				flags[i] = "t"
			}
		}
		if item.rawNulls != "{"+strings.Join(flags, ",")+"}" || math.Abs(item.frequency-expected.frequency) > 1e-14 || math.Abs(item.baseline-expected.baseline) > 1e-14 {
			t.Fatalf("item %#v want %#v", item, expected)
		}
	}
}

func mcvItemsCheck(t *testing.T, s ndistinctScenario, name string, want ...mcvItemsExpected) {
	t.Helper()
	mcvItemsAssert(t, mcvItemsRead(t, s.pair.Ctx, s, name), want...)
}

func mcvItemsTypeSetup(s ndistinctScenario, typ, literal string) {
	s.run(fixture.Exec("CREATE TABLE mcv_item_type(a "+typ+",b INT)", 0))
	s.run(fixture.Exec("CREATE STATISTICS mcv_item_type_s(mcv) ON a,b FROM mcv_item_type", 0))
	s.run(fixture.Exec("INSERT INTO mcv_item_type VALUES("+literal+",1),("+literal+",1)", 2))
	s.run(fixture.Exec("ANALYZE mcv_item_type", 0))
}
