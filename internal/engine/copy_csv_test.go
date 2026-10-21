package engine

import (
	"bufio"
	"reflect"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func csvUnitOptions(t *testing.T, q string) copyOptions {
	t.Helper()
	statements, err := parser.Parse(q)
	if err != nil {
		t.Fatal(err)
	}
	o, err := parseCopyOptions(statements[0].(*parser.Copy))
	if err != nil {
		t.Fatal(err)
	}
	return o
}
func TestCopyCSVOptions(t *testing.T) {
	for _, q := range []string{"COPY t FROM STDIN WITH(FORMAT csv)", "COPY t FROM STDIN WITH(QUOTE '''',FORMAT csv)", "COPY t FROM STDIN WITH(ESCAPE '!',QUOTE '''',FORMAT csv)"} {
		o := csvUnitOptions(t, q)
		if o.format != copyFormatCSV || o.delimiter != ',' || o.null != "" {
			t.Fatal(o)
		}
		quote, escape := byte('"'), byte('"')
		if strings.Contains(q, "QUOTE") {
			quote = '\''
			escape = quote
		}
		if strings.Contains(q, "ESCAPE") {
			escape = '!'
		}
		if o.quote != quote || o.escape != escape {
			t.Fatal(o)
		}
	}
	o := csvUnitOptions(t, "COPY t FROM STDIN WITH(FORMAT text)")
	if o.format == copyFormatCSV || o.delimiter != '\t' || o.null != `\N` {
		t.Fatal(o)
	}
}
func TestCopyCSVFraming(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
	}{
		{"", nil}, {"\n", []string{""}}, {"\"a\nb\"\nc", []string{"\"a\nb\"", "c"}},
		{"\"a\r\nb\"\r\nc\r\n", []string{"\"a\r\nb\"", "c"}},
		{"\"" + strings.Repeat("é", 40000) + "\"", []string{"\"" + strings.Repeat("é", 40000) + "\""}},
	} {
		c := copyCursor{options: copyOptions{format: copyFormatCSV, quote: '"', escape: '"'}, reader: bufio.NewReaderSize(strings.NewReader(tc.input), 16)}
		var got []string
		for {
			present, err := c.readLine()
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			got = append(got, string(c.line))
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("records %#v want %#v", got, tc.want)
		}
	}
	t.Run("EstablishedCRLeavesLF", func(t *testing.T) {
		c := copyCursor{options: copyOptions{format: copyFormatCSV, quote: '"', escape: '"'}, reader: bufio.NewReaderSize(strings.NewReader("1,ok\r2,x\r\n"), 16)}
		for i, want := range []string{"1,ok", "2,x"} {
			present, err := c.readLine()
			if err != nil || !present || string(c.line) != want || c.number != int64(i+1) {
				t.Fatalf("record %q line %d present %v err %v", c.line, c.number, present, err)
			}
		}
		present, err := c.readLine()
		record, ok := err.(*sqlstate.Error)
		if present || !ok || record.Code != "22P04" || record.Message != "unquoted newline found in data" || record.Hint != "Use quoted CSV field to represent newline." || c.number != 3 || c.lineValid {
			t.Fatalf("leftover LF line %d present %v valid %v err %#v", c.number, present, c.lineValid, err)
		}
	})
	t.Run("EscapedRawByteHistory", func(t *testing.T) {
		for _, tc := range []struct {
			input  string
			escape byte
			lines  []int64
		}{
			{"1,ok\n\"bad\n\nvalue\",x\n", '\n', []int64{1, 4}},
			{"1,ok\n\"bad\n\"value\",x\n", '\n', []int64{1, 3}},
			{"1,ok\r\"bad\r\rvalue\",x\r", '\r', []int64{1, 4}},
			{"\"bad\r\rvalue\",x\r", '\r', []int64{3}},
			{"\"bad\n\nvalue\",x\n", '\n', []int64{1}},
			{"1,ok\n2,\"good\n\nvalue\"\nbad,x\n", '\n', []int64{1, 4, 5}},
		} {
			c := copyCursor{options: copyOptions{format: copyFormatCSV, quote: '"', escape: tc.escape}, reader: bufio.NewReaderSize(strings.NewReader(tc.input), 16)}
			for _, line := range tc.lines {
				present, err := c.readLine()
				if err != nil || !present || c.number != line {
					t.Fatalf("input %q line %d want %d present %v err %v", tc.input, c.number, line, present, err)
				}
			}
			if present, err := c.readLine(); err != nil || present {
				t.Fatal(present, err)
			}
		}
	})
	t.Run("EqualNewlineQuote", func(t *testing.T) {
		for _, tc := range []struct {
			quote      byte
			input, raw string
			line       int64
		}{
			{'\r', "\rbad\r\rvalue\r", "\rbad", 2},
			{'\n', "\nbad\n\nvalue\n", "\nbad", 1},
		} {
			c := copyCursor{options: copyOptions{format: copyFormatCSV, quote: tc.quote, escape: tc.quote}, reader: bufio.NewReaderSize(strings.NewReader(tc.input), 16)}
			present, err := c.readLine()
			if err != nil || !present || string(c.line) != tc.raw || c.number != tc.line {
				t.Fatalf("quote %q raw %q line %d present %v err %v", tc.quote, c.line, c.number, present, err)
			}
			err = c.splitCSV(false)
			record, ok := err.(*sqlstate.Error)
			if !ok || record.Code != "22P04" || record.Message != "unterminated CSV quoted field" {
				t.Fatalf("quote %q split err %#v", tc.quote, err)
			}
		}
	})
	t.Run("DistinctEscapeParityReset", func(t *testing.T) {
		for _, tc := range []struct{ input, value string }{
			{"'a!!!'b',tail\n", "a!'b"},
			{"'a!!!!',tail\n", "a!!"},
			{"'a!x'b,tail\n", "a!xb"},
		} {
			c := copyCursor{options: copyOptions{format: copyFormatCSV, quote: '\'', escape: '!', delimiter: ','}, reader: bufio.NewReaderSize(strings.NewReader(tc.input), 16), targets: make([]copyTarget, 2)}
			present, err := c.readLine()
			if err != nil || !present || string(c.line) != strings.TrimSuffix(tc.input, "\n") {
				t.Fatalf("raw %q present %v err %v", c.line, present, err)
			}
			if err = c.splitCSV(false); err != nil {
				t.Fatal(err)
			}
			if len(c.fields) != 2 || string(c.fields[0].decoded) != tc.value || string(c.fields[1].decoded) != "tail" {
				t.Fatal(c.fields)
			}
		}
	})
}
func TestCopyCSVFields(t *testing.T) {
	for _, tc := range []struct {
		input        string
		want         []string
		quoted, null []bool
	}{
		{",\"\",a\"b\"c,\"a\" b", []string{"", "", "abc", "a b"}, []bool{false, true, true, true}, []bool{true, false, false, false}},
		{"\"a\"\"b\",\\n", []string{"a\"b", `\n`}, []bool{true, false}, []bool{false, false}},
		{"\"" + strings.Repeat("x", 90000) + "\",tail", []string{strings.Repeat("x", 90000), "tail"}, []bool{true, false}, []bool{false, false}},
	} {
		c := copyCursor{options: copyOptions{format: copyFormatCSV, delimiter: ',', quote: '"', escape: '"'}, line: []byte(tc.input), targets: make([]copyTarget, len(tc.want))}
		if err := c.split(true); err != nil {
			t.Fatal(err)
		}
		if len(c.fields) != len(tc.want) {
			t.Fatal(c.fields)
		}
		for i, f := range c.fields {
			if string(f.decoded) != tc.want[i] || f.quoted != tc.quoted[i] || f.isNull != tc.null[i] {
				t.Fatalf("field %d=%+v", i, f)
			}
		}
	}
}
func TestCopyCSVOutput(t *testing.T) {
	o := copyOptions{format: copyFormatCSV, delimiter: ',', quote: '"', escape: '"'}
	for _, tc := range []struct {
		text, want    string
		force, single bool
	}{
		{"", "\"\"", false, true}, {"x,y", "\"x,y\"", false, false}, {"a\"b", "\"a\"\"b\"", false, false},
		{"line\nbreak", "\"line\nbreak\"", false, false}, {`\.`, `"\."`, false, true}, {`\.`, `\.`, false, false}, {"plain", "\"plain\"", true, false},
	} {
		if got := string(appendCopyCSV(nil, tc.text, o, tc.force, tc.single)); got != tc.want {
			t.Fatalf("%q want %q", got, tc.want)
		}
	}
	o.quote = '\''
	o.escape = '!'
	if got := string(appendCopyCSV(nil, "a'b!c", o, false, true)); got != "'a!'b!!c'" {
		t.Fatal(got)
	}
	if got := string(appendCopyCSV(nil, "a!b", o, false, true)); got != "a!b" {
		t.Fatal(got)
	}
}
func TestCopyCSVLifecycle(t *testing.T) {
	t.Run("OwnerReservation", func(t *testing.T) { copyOwnerReservation(t, copyFormatCSV) })
	t.Run("OperationExpiry", func(t *testing.T) { copyTimeoutTest(t, true) })
	t.Run("FinalCancellationVeto", func(t *testing.T) { copyFinalCancellationVeto(t, true) })
}
