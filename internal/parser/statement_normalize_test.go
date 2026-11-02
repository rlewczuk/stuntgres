package parser

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestPgStatStatementsNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, source, want   string
		anchor               string
		highest              int
		constants, utilities []string
		lists                [][2]string
		legacyStrings        bool
	}{
		{name: "negative list end", source: "SELECT k IN (11,-222)", lists: [][2]string{{"11", "-222"}}, want: "SELECT k IN ($1 /*, ... */)"},
		{name: "signed numeric token", source: "SELECT ARRAY[11,-1.25e-10]", lists: [][2]string{{"11", "-1.25e-10"}}, want: "SELECT ARRAY[$1 /*, ... */]"},
		{name: "comment before numeric token", source: "SELECT ARRAY[11,- /* sign */ 222]", lists: [][2]string{{"11", "- /*"}}, want: "SELECT ARRAY[$1 /*, ... */]"},
		{name: "positive list end", source: "SELECT ARRAY[11,+222]", lists: [][2]string{{"11", "+222"}}, want: "SELECT ARRAY[$1 /*, ... */]"},
		{name: "grouped and cast list operands", source: "SELECT ARRAY[(11),CAST(-222 AS integer)],333", lists: [][2]string{{"11", "CAST"}}, constants: []string{"333"}, want: "SELECT ARRAY[$1 /*, ... */],$2"},
		{name: "full parameter cast list", source: "SELECT $3::integer,11 IN ($1::integer,$2::integer)", lists: [][2]string{{"$1", "$2"}}, constants: []string{"$3", "11"}, want: "SELECT $1::integer,$2 IN ($3 /*, ... */)"},
		{name: "list interior comments", source: "SELECT ARRAY[/* first */ 11,-222 /* last */]", lists: [][2]string{{"11", "-222"}}, want: "SELECT ARRAY[$1 /*, ... */]"},
		{name: "negative scalar", source: "SELECT -222", constants: []string{"-222"}, want: "SELECT $1"},
		{name: "typed unary operand", source: "SELECT -222::integer", constants: []string{"222"}, want: "SELECT -$1::integer"},
		{name: "highest parameter", source: "SELECT $7,222,'雪'", highest: 7, constants: []string{"222", "'雪'"}, want: "SELECT $7,$8,$9"},
		{name: "first spelling", source: " \tSELECT /* 雪 */ 222 AS \"Label\" -- tail\n ", constants: []string{"222"}, want: "SELECT /* 雪 */ $1 AS \"Label\" -- tail"},
		{name: "duplicate and overlapping locations", source: "SELECT ARRAY[11,-222],333", constants: []string{"11", "11", "-222", "333"}, lists: [][2]string{{"11", "-222"}}, want: "SELECT ARRAY[$1 /*, ... */],$2"},
		{name: "utility name", source: "DEALLOCATE \"snow 雪\"", utilities: []string{"\"snow 雪\""}, want: "DEALLOCATE $1"},
		{name: "unicode literal end", source: "SELECT U&'d\\0061t' UESCAPE '\\',222", constants: []string{"U&'", "222"}, want: "SELECT $1,$2"},
		{name: "escape string", source: `SELECT E'a\'b',222`, constants: []string{`E'a`, "222"}, want: "SELECT $1,$2"},
		{name: "captured legacy string", source: `SELECT 'a\'b',222`, constants: []string{`'a`, "222"}, legacyStrings: true, want: "SELECT $1,$2"},
		{name: "dollar string", source: "SELECT $body$a;b$body$,222", constants: []string{"$body$", "222"}, want: "SELECT $1,$2"},
		{name: "middle batch", source: "SELECT 11; \n SELECT /* keep */ 222; SELECT 333", anchor: "222", constants: []string{"11", "222", "333"}, want: "SELECT /* keep */ $1"},
		{name: "rule action semicolons", source: "CREATE RULE r AS ON INSERT TO t DO (SELECT 11; SELECT 222); SELECT 333", anchor: "CREATE", constants: []string{"11", "222", "333"}, want: "CREATE RULE r AS ON INSERT TO t DO (SELECT $1; SELECT $2)"},
		{name: "atomic body semicolons", source: "CREATE FUNCTION f() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT CASE WHEN true THEN 11 ELSE 222 END; SELECT 333; END; SELECT 444", anchor: "CREATE", constants: []string{"11", "222", "333", "444"}, want: "CREATE FUNCTION f() RETURNS integer LANGUAGE SQL BEGIN ATOMIC SELECT CASE WHEN true THEN $1 ELSE $2 END; SELECT $3; END"},
		{name: "no replacements", source: " \tSELECT current_user \n", want: "SELECT current_user"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offset := func(text string) int {
				t.Helper()
				at := strings.Index(tc.source, text)
				if at < 0 {
					t.Fatalf("missing test location %q", text)
				}
				return at
			}
			var constants, utilities []int
			var lists [][2]int
			for _, text := range tc.constants {
				constants = append(constants, offset(text))
			}
			for _, text := range tc.utilities {
				utilities = append(utilities, offset(text))
			}
			for _, bounds := range tc.lists {
				lists = append(lists, [2]int{offset(bounds[0]), offset(bounds[1])})
			}
			anchor := 0
			if tc.anchor != "" {
				anchor = offset(tc.anchor)
			}
			options := Options{StandardConformingStrings: !tc.legacyStrings}
			got, _ := NormalizeStatementText(tc.source, anchor, tc.highest, constants, lists, utilities, options)
			if got != tc.want {
				t.Fatalf("normalized %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPgStatStatementsNormalizationRaw(t *testing.T) {
	for _, tc := range []struct {
		source, anchor, want string
	}{
		{" \tSELECT /* 雪 */ 222 AS \"Label\" -- tail\n ", "222", "SELECT /* 雪 */ 222 AS \"Label\" -- tail"},
		{"SELECT 11; \n SELECT /* keep */ 222; SELECT 333", "222", "SELECT /* keep */ 222"},
		{"SELECT $body$a;b$body$,222", "222", "SELECT $body$a;b$body$,222"},
		{"CREATE RULE r AS ON INSERT TO t DO (SELECT 11; SELECT 222); SELECT 333", "CREATE", "CREATE RULE r AS ON INSERT TO t DO (SELECT 11; SELECT 222)"},
		{" \tSET ROLE \"snow 雪\"; SELECT 333", "SET", "SET ROLE \"snow 雪\""},
	} {
		t.Run(tc.anchor+"/"+tc.want, func(t *testing.T) {
			_, raw := NormalizeStatementText(tc.source, strings.Index(tc.source, tc.anchor), 0, nil, nil, nil, Options{StandardConformingStrings: true})
			if raw != tc.want {
				t.Fatalf("clean raw representative %q, want %q", raw, tc.want)
			}
		})
	}
}

func TestPgStatStatementsNormalizationLargeMultirow(t *testing.T) {
	// Thousands of genuine VALUES operands exercise the selected-offset path.
	// Exact output, not elapsed time, is the regression contract.
	const rows = 2048
	var source, want strings.Builder
	source.WriteString("SELECT 999; \n\t")
	anchor := source.Len()
	const prefix = "INSERT /* large 雪 */ INTO t(p,k,v,n) VALUES\n"
	source.WriteString(prefix)
	want.WriteString(prefix)
	locations := make([]int, 0, 3*rows)
	for row := range rows {
		if row > 0 {
			source.WriteString(",\n")
			want.WriteString(",\n")
		}
		source.WriteString("($7,")
		locations = append(locations, source.Len())
		fmt.Fprintf(&source, "%d,", row+101)
		locations = append(locations, source.Len())
		fmt.Fprintf(&source, "'雪 row %d',", row)
		locations = append(locations, source.Len())
		fmt.Fprintf(&source, "- /* sign */ %d)", row+201)
		fmt.Fprintf(&want, "($7,$%d,$%d,$%d)", 8+3*row, 9+3*row, 10+3*row)
	}
	raw := source.String()[anchor:]
	source.WriteString(" \t; SELECT 555")
	// Descending locations must not require a sorted caller, and must not be
	// sorted in place by the normalization owner.
	slices.Reverse(locations)
	before := slices.Clone(locations)
	got, gotRaw := NormalizeStatementText(source.String(), anchor, 7, locations, nil, nil, Options{StandardConformingStrings: true})
	if got != want.String() {
		t.Fatalf("large multirow normalized text differs: got %d bytes, want %d", len(got), want.Len())
	}
	if gotRaw != raw {
		t.Fatalf("large multirow raw source differs: got %d bytes, want %d", len(gotRaw), len(raw))
	}
	if !slices.Equal(locations, before) {
		t.Fatal("normalization mutated caller constant locations")
	}
}

func TestPgStatStatementsNormalizationSelectedOffsets(t *testing.T) {
	for _, tc := range []struct {
		name, source, anchor, raw, want string
		highest                         int
		constants, utilities            []string
		lists                           [][2]string
		canceled                        bool
	}{
		{
			name:   "mixed unsorted duplicates and overlapping spans",
			source: "SELECT 999; \n SELECT $7,ARRAY[11,- /* sign */ 222],+ /* plus */ 333,\"snow 雪\",444; SELECT 555",
			anchor: "SELECT $7", highest: 7,
			constants: []string{"444", "555", "+ /* plus */", "222", "11", "- /* sign */", "11", "999"},
			utilities: []string{"\"snow 雪\"", "444", "11", "\"snow 雪\"", "+ /* plus */"},
			lists:     [][2]string{{"11", "- /* sign */"}},
			raw:       "SELECT $7,ARRAY[11,- /* sign */ 222],+ /* plus */ 333,\"snow 雪\",444",
			want:      "SELECT $7,ARRAY[$8 /*, ... */],$9,$10,$11",
		},
		{
			name:      "signed nonnumeric lookahead remains selectable",
			source:    " \tSELECT -/* keep */\"snow 雪\",444 \n",
			constants: []string{"444", "-/*", "444"},
			utilities: []string{"\"snow 雪\"", "\"snow 雪\""},
			raw:       "SELECT -/* keep */\"snow 雪\",444",
			want:      "SELECT $1/* keep */$2,$3",
		},
		{
			name:      "clean raw fallback for unmatched offsets",
			source:    " \tSELECT /* 雪 */ current_user \n",
			constants: []string{"雪", "user"},
			raw:       "SELECT /* 雪 */ current_user",
			want:      "SELECT /* 雪 */ current_user",
		},
		{
			name:      "cancellation retains cleaned raw fallback",
			source:    " \tSELECT /* 雪 */ 11,222 \n",
			constants: []string{"222", "11", "222"},
			utilities: []string{"11"},
			canceled:  true,
			raw:       "SELECT /* 雪 */ 11,222",
			want:      "SELECT /* 雪 */ 11,222",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			offset := func(text string) int {
				t.Helper()
				at := strings.Index(tc.source, text)
				if at < 0 {
					t.Fatalf("missing test location %q", text)
				}
				return at
			}
			var constants, utilities []int
			var lists [][2]int
			for _, text := range tc.constants {
				constants = append(constants, offset(text))
			}
			for _, text := range tc.utilities {
				utilities = append(utilities, offset(text))
			}
			for _, bounds := range tc.lists {
				lists = append(lists, [2]int{offset(bounds[0]), offset(bounds[1])})
			}
			beforeConstants, beforeUtilities, beforeLists := slices.Clone(constants), slices.Clone(utilities), slices.Clone(lists)
			options := Options{StandardConformingStrings: true}
			if tc.canceled {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				options.Context = ctx
			}
			anchor := 0
			if tc.anchor != "" {
				anchor = offset(tc.anchor)
			}
			got, raw := NormalizeStatementText(tc.source, anchor, tc.highest, constants, lists, utilities, options)
			if got != tc.want || raw != tc.raw {
				t.Fatalf("normalized/raw = %q / %q, want %q / %q", got, raw, tc.want, tc.raw)
			}
			if !slices.Equal(constants, beforeConstants) || !slices.Equal(utilities, beforeUtilities) || !slices.Equal(lists, beforeLists) {
				t.Fatal("normalization mutated caller constant, utility or list locations")
			}
		})
	}
}
