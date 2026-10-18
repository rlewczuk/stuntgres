package unicode16

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func corpusString(t *testing.T, s string) string {
	t.Helper()
	var b strings.Builder
	for _, field := range strings.Fields(s) {
		n, err := strconv.ParseUint(field, 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		b.WriteRune(rune(n))
	}
	return b.String()
}

// Expected columns come exclusively from the unmodified official Unicode16
// NormalizationTest corpus, never from our generated tables or implementation.
func TestUnicode16Conformance(t *testing.T) {
	file, err := os.Open("testdata/NormalizationTest.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	part1 := false
	listed := make(map[rune]bool)
	rows := 0
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "@") {
			part1 = strings.HasPrefix(line, "@Part1")
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 5 {
			t.Fatalf("malformed corpus row: %s", line)
		}
		var c [5]string
		for i := range c {
			c[i] = corpusString(t, fields[i])
		}
		if part1 && utf8.RuneCountInString(c[0]) == 1 {
			r, _ := utf8.DecodeRuneInString(c[0])
			listed[r] = true
		}
		expected := [4][5]int{{1, 1, 1, 3, 3}, {2, 2, 2, 4, 4}, {3, 3, 3, 3, 3}, {4, 4, 4, 4, 4}}
		for form := range 4 {
			for i, input := range c {
				want := c[expected[form][i]]
				got, err := Normalize(context.Background(), input, Form(form))
				if err != nil || got != want {
					t.Fatalf("row %d form %d column %d: got %U want %U err %v", rows, form, i, []rune(got), []rune(want), err)
				}
				normalized, err := IsNormalized(context.Background(), input, Form(form))
				if err != nil || normalized != (input == want) {
					t.Fatalf("row %d form %d column %d predicate %v want %v err %v", rows, form, i, normalized, input == want, err)
				}
				again, err := Normalize(context.Background(), got, Form(form))
				if err != nil || again != got {
					t.Fatalf("idempotence row %d form %d: %v", rows, form, err)
				}
			}
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if rows < 19000 {
		t.Fatalf("incomplete corpus: %d rows", rows)
	}
	// Header conformance requirement: every assigned scalar absent from Part1 is
	// unchanged in every form. Assignment is independently read from pinned UCD,
	// including First/Last ranges, not Go's host Unicode version.
	ucd, err := os.Open("data/UnicodeData.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer ucd.Close()
	scanner = bufio.NewScanner(ucd)
	var first rune
	assigned := 0
	check := func(r rune) {
		if listed[r] || !utf8.ValidRune(r) {
			return
		}
		assigned++
		s := string(r)
		for form := range 4 {
			got, err := Normalize(context.Background(), s, Form(form))
			if err != nil || got != s {
				t.Fatalf("unlisted assigned %U form %d: %U %v", r, form, []rune(got), err)
			}
		}
	}
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ";")
		if len(fields) < 3 {
			t.Fatal("malformed UCD row")
		}
		n, err := strconv.ParseUint(fields[0], 16, 32)
		if err != nil {
			t.Fatal(err)
		}
		r := rune(n)
		if strings.HasSuffix(fields[1], ", First>") {
			first = r
			continue
		}
		if strings.HasSuffix(fields[1], ", Last>") {
			for code := first; code <= r; code++ {
				check(code)
			}
			continue
		}
		check(r)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if assigned < 200000 {
		t.Fatalf("incomplete assignment coverage: %d", assigned)
	}
}

func TestUnicode16Boundaries(t *testing.T) {
	for _, tc := range []struct {
		input      string
		form       Form
		output     string
		normalized bool
	}{
		{"가ᆧ", NFC, "가ᆧ", true}, {"가ᆧ", NFKC, "가ᆧ", true},
		{"가ᆧ", NFC, "가ᆧ", false}, {"가ᆧ", NFKC, "가ᆧ", false},
		{"가ᆧ", NFD, "가ᆧ", false}, {"가ᆧ", NFKD, "가ᆧ", false},
		{"가ᆧᆧ", NFC, "가ᆧᆧ", true}, {"가́ᆧ", NFC, "가́ᆧ", true},
		{"Ḍ̇", NFC, "Ḍ̇", false}, {"̈́", NFC, "̈́", false},
		{"a̅́", NFC, "a̅́", true}, {"ﬃ", NFKC, "ffi", false},
		{"á́", NFD, "á́", true}, {"\U0001E030", NFKC, "а", false},
	} {
		got, err := Normalize(context.Background(), tc.input, tc.form)
		if err != nil || got != tc.output {
			t.Fatalf("%U form %d: %U want %U err %v", []rune(tc.input), tc.form, []rune(got), []rune(tc.output), err)
		}
		normalized, err := IsNormalized(context.Background(), tc.input, tc.form)
		if err != nil || normalized != tc.normalized {
			t.Fatal(tc, normalized, err)
		}
	}
	// Stable equal-CCC ordering and no stream-safe CGJ insertion, even beyond30.
	input := "a" + strings.Repeat("́", 4096)
	got, err := Normalize(context.Background(), input, NFD)
	if err != nil || got != input || strings.ContainsRune(got, '\u034f') {
		t.Fatal("long nonstarter run changed", err)
	}
	input = "a" + strings.Repeat("̕", 4096) + strings.Repeat("̀", 4096)
	want := "a" + strings.Repeat("̀", 4096) + strings.Repeat("̕", 4096)
	got, err = Normalize(context.Background(), input, NFD)
	if err != nil || got != want {
		t.Fatal("unbounded canonical ordering", err)
	}
	for _, s := range []string{"NFC", "nFd", "nfKc", "NFKD"} {
		if _, ok := ParseForm(s); !ok {
			t.Fatal(s)
		}
	}
	for _, s := range []string{" NFC", "NFC ", "bad", "", "NFKC", "NFKD"} {
		if _, ok := ParseForm(s); ok {
			t.Fatal(s)
		}
	}
}

type cancellationProbe struct {
	context.Context
	remaining int
}

func (c *cancellationProbe) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestUnicode16Cancellation(t *testing.T) {
	for _, input := range []string{strings.Repeat("a", 10000), strings.Repeat("ﬃ", 10000), "a" + strings.Repeat("̀̕", 10000)} {
		c := &cancellationProbe{Context: context.Background(), remaining: 3}
		if _, err := Normalize(c, input, NFKD); !errors.Is(err, context.Canceled) {
			t.Fatal("normalization cancellation", err)
		}
		c = &cancellationProbe{Context: context.Background(), remaining: 3}
		if _, err := IsNormalized(c, input, NFC); !errors.Is(err, context.Canceled) {
			t.Fatal("predicate cancellation", err)
		}
	}
	for form := range 4 {
		for _, input := range []string{"ASCII plain text", "🙂"} {
			allocations := testing.AllocsPerRun(100, func() {
				got, err := Normalize(context.Background(), input, Form(form))
				if err != nil || got != input {
					panic("normalization fast path")
				}
			})
			if allocations != 0 {
				t.Fatalf("normalized fast path allocates: %g", allocations)
			}
		}
	}
}

func TestUnicode16ReviewForms(t *testing.T) {
	for _, tc := range []struct {
		spelling string
		form     Form
	}{
		{"NFC", NFC}, {"nFc", NFC}, {"NFD", NFD}, {"NfD", NFD},
		{"NFKC", NFKC}, {"nfKc", NFKC}, {"NFKD", NFKD}, {"NfKd", NFKD},
	} {
		if got, ok := ParseForm(tc.spelling); !ok || got != tc.form {
			t.Fatal(tc, got, ok)
		}
	}
	for _, spelling := range []string{"NFKC", "NFKD", "nfKc", "nfKd", "", " NFC", "NFC "} {
		if form, ok := ParseForm(spelling); ok {
			t.Fatal("non-ASCII or padded form accepted", spelling, form)
		}
	}
}
