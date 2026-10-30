package functions

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Model only the released C's two slice operations, using the independently
// transcribed test manifest and ordinary string operations. In particular the
// second replacement retains the first inserted tail, rather than reducing
// restoration alternatives to one Boolean choice. No kernel helper supplies
// expected matches, cursor positions or resulting text.
func greekExpectedRestorations(prefix, replacement, exact, suffixes, vowels string) string {
	cursor := len(prefix)
	c, size := utf8.DecodeLastRuneInString(prefix)
	if prefix != "" && strings.ContainsRune(vowels, c) {
		cursor -= size
	} else if suffix := testGreekLongest(prefix, suffixes); suffix != "" {
		cursor -= len(suffix)
	}
	inserted := ""
	if cursor != len(prefix) {
		inserted = replacement
	}
	for _, exception := range strings.Fields(exact) {
		if prefix[:cursor] == exception {
			return prefix[:cursor] + replacement + inserted
		}
	}
	return prefix + inserted
}

func greekTestCursorOverlaps(t *testing.T) {
	manifest := greekStageManifest()
	for _, stage := range manifest {
		if stage.name != "4" && stage.name != "5b-short" && stage.name != "5c-short" {
			continue
		}
		t.Run(stage.name, func(t *testing.T) {
			exact, suffixes, replacement := stage.exceptions[0].stems, "", "ικ"
			firstEndings := []string{""}
			switch stage.name {
			case "5b-short":
				replacement = "αν"
				firstEndings = append(firstEndings, strings.Fields("αγανε ησανε ουσανε ιοντανε ιοτανε ιουντανε οντανε οτανε ουντανε ηκανε ηθηκανε")...)
			case "5c-short":
				replacement = "ετ"
				exact, suffixes = stage.exceptions[1].stems, stage.exceptions[0].stems
				firstEndings = append(firstEndings, "ησετε")
			}
			// Every exact exception crossed with every Greek vowel, excluded
			// upsilon for v2, consonant neighbors, and an empty consumed span.
			spans := []string{"", "α", "ε", "η", "ι", "ο", "υ", "ω", "ξ", "θ"}
			// Step5c also crosses every exact exception with all 31 suffix
			// predicates. Their longest matches may span a constructed boundary.
			spans = append(spans, strings.Fields(suffixes)...)
			for _, exception := range strings.Fields(exact) {
				for _, boundary := range []string{exception, "σσσ" + exception, exception + "ξ"} {
					for _, span := range spans {
						prefix := boundary + span
						want := greekExpectedRestorations(prefix, replacement, exact, suffixes, stage.vowels)
						for _, ending := range strings.Fields(stage.endings) {
							input := prefix + ending
							for _, incoming := range []bool{false, true} {
								for _, first := range firstEndings {
									expected := want
									if stage.name == "5b-short" && first == "" {
										long := testGreekLongest(input, strings.Join(firstEndings, " "))
										if long != "" {
											expected = strings.TrimSuffix(input, long)
											if expected == "τρ" || expected == "τσ" {
												expected += "αγαν"
											}
											if strings.HasSuffix(expected, "ανε") {
												expected = greekExpectedRestorations(strings.TrimSuffix(expected, "ανε"), replacement, exact, suffixes, stage.vowels)
											}
										}
									}
									greekAssertStage(t, stage.run, input+first, expected, incoming, false)
									if first != "" {
										// Initial deletion remains when the following
										// required arm is blocked by a consonant.
										greekAssertStage(t, stage.run, input+"ξ"+first, input+"ξ", incoming, false)
									}
								}
								for _, tail := range []string{"!", "\u0301"} {
									greekAssertStage(t, stage.run, input+tail, input+tail, incoming, incoming)
								}
							}
						}
					}
				}
			}
		})
	}
}
