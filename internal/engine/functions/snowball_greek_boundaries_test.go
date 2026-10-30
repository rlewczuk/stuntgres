package functions

import (
	"strings"
	"testing"
)

func testGreekLongest(input, members string) string {
	best := ""
	for _, member := range strings.Fields(members) {
		if len(member) > len(best) && strings.HasSuffix(input, member) {
			best = member
		}
	}
	return best
}

func greekTestIrregular(t *testing.T) {
	for _, group := range []struct{ endings, stem string }{
		{"φαγια φαγιου φαγιων", "φα"}, {"σκαγια σκαγιου σκαγιων", "σκα"},
		{"ολογια ολογιου ολογιων", "ολο"}, {"σογια σογιου σογιων", "σο"},
		{"τατογια τατογιου τατογιων", "τατο"}, {"κρεασ κρεατοσ κρεατα κρεατων", "κρε"},
		{"περασ περατοσ περατη περατα περατων", "περ"}, {"τερασ τερατοσ τερατα τερατων", "τερ"},
		{"φωσ φωτοσ φωτα φωτων", "φω"}, {"καθεστωσ καθεστωτοσ καθεστωτα καθεστωτων", "καθεστ"},
		{"γεγονοσ γεγονοτοσ γεγονοτα γεγονοτων", "γεγον"},
	} {
		for _, ending := range strings.Fields(group.endings) {
			for _, prefix := range []string{"", "σσσ"} {
				for _, incoming := range []bool{false, true} {
					greekAssertStage(t, (*greekState).step1, prefix+ending, prefix+group.stem, incoming, false)
					for _, tail := range []string{"!", "\u0301"} {
						input := prefix + ending + tail
						greekAssertStage(t, (*greekState).step1, input, input, incoming, incoming)
					}
					short := string([]rune(ending)[:len([]rune(ending))-1])
					greekAssertStage(t, (*greekState).step1, prefix+short, prefix+short, incoming, incoming)
				}
			}
			// These irregulars have no later suffix rule affecting the replacement.
			if got := greekStem(ending); got != group.stem {
				t.Errorf("pipeline irregular %q -> %q, want %q", ending, got, group.stem)
			}
		}
	}
	for _, incoming := range []bool{false, true} {
		greekAssertStage(t, (*greekState).step1, "σσσ", "σσσ", incoming, incoming)
	}
}

func greekTestSecondary(t *testing.T) {
	for _, pair := range [][2]string{
		{"αγνωστικ", "αγνωστ"}, {"ατομικ", "ατομ"}, {"γνωστικ", "γνωστ"}, {"εθνικ", "εθν"},
		{"εκλεκτικ", "εκλεκτ"}, {"σκεπτικ", "σκεπτ"}, {"τοπικ", "τοπ"},
		{"αλεξανδριν", "αλεξανδρ"}, {"βυζαντιν", "βυζαντ"}, {"θεατριν", "θεατρ"},
	} {
		for _, ending := range strings.Fields("ισμο ισμοι ισμοσ ισμου ισμουσ ισμων") {
			for _, incoming := range []bool{false, true} {
				for _, prefix := range []string{"", "σσσ"} {
					greekAssertStage(t, (*greekState).s6, prefix+pair[0]+ending, prefix+pair[1], incoming, false)
					greekAssertStage(t, (*greekState).s6, prefix+pair[0]+"ξ"+ending, prefix+pair[0]+"ξ", incoming, false)
					short := string([]rune(pair[0])[:len([]rune(pair[0]))-1])
					greekAssertStage(t, (*greekState).s6, prefix+short+ending, prefix+short, incoming, false)
				}
			}
		}
	}
	// Sequential do-block edits persist, and the following required arm may
	// still act on the edited prefix. These are not transactional replacements.
	for _, tc := range []struct {
		run     func(*greekState)
		in, out string
	}{
		{(*greekState).step5a, "σσσαμεησαμε", "σσσ"},
		{(*greekState).step5a, "αναπαμεησαμε", "αναπαμ"},
		{(*greekState).step5b, "σσσανεησανε", "σσσ"},
		{(*greekState).step5b, "αανεησανε", "ααν"},
		{(*greekState).step5c, "σσσετεησετε", "σσσ"},
		{(*greekState).step5c, "υετεησετε", "υετ"},
		{(*greekState).step5c, "ξυετε", "ξυ"},
		{(*greekState).step5f, "αλεστειεστε", "αλιεστ"},
		{(*greekState).step5g, "σσσηκαηθηκα", "σσσ"},
		{(*greekState).step5g, "θηκαηθηκα", "θηκ"},
		{(*greekState).step5f, "σσσεστειεστε", "σσσ"},
	} {
		for _, incoming := range []bool{false, true} {
			greekAssertStage(t, tc.run, tc.in, tc.out, incoming, false)
		}
	}
}

func greekTestGeneric(t *testing.T) {
	// Complete independent step6 generic among, including single vowels and
	// overlapping long members. Step6 never changes test1 itself.
	endings := "α αγατε αγαν αει αμαι αν ασ ασαι αται αω ε ει εισ ειτε εσαι εσ εται ι ιεμαι ιεμαστε ιεται ιεσαι ιεσαστε ιομασταν ιομουν ιομουνα ιονταν ιοντουσαν ιοσασταν ιοσαστε ιοσουν ιοσουνα ιοταν ιουμα ιουμαστε ιουνται ιουνταν η ηδεσ ηδων ηθει ηθεισ ηθειτε ηθηκατε ηθηκαν ηθουν ηθω ηκατε ηκαν ησ ησαν ησατε ησει ησεσ ησουν ησω ο οι ομαι ομασταν ομουν ομουνα ονται ονταν οντουσαν οσ οσασταν οσαστε οσουν οσουνα οταν ου ουμαι ουμαστε ουν ουνται ουνταν ουσ ουσαν ουσατε υ υσ ω ων"
	for _, ending := range strings.Fields(endings) {
		for _, incoming := range []bool{false, true} {
			for _, prefix := range []string{"", "σσσ"} {
				want := prefix + ending
				if incoming {
					want = prefix
				}
				greekAssertStage(t, (*greekState).step6, prefix+ending, want, incoming, incoming)
			}
			for _, tail := range []string{"!", "\u0301"} {
				input := "σσσ" + ending + tail
				greekAssertStage(t, (*greekState).step6, input, input, incoming, incoming)
			}
			short := string([]rune(ending)[:len([]rune(ending))-1])
			want := "σσσ" + short
			if incoming {
				want = strings.TrimSuffix(want, testGreekLongest(short, endings))
			}
			greekAssertStage(t, (*greekState).step6, "σσσ"+short, want, incoming, incoming)
		}
	}
	for _, ending := range []string{"ματα", "ματων", "ματοσ"} {
		for _, incoming := range []bool{false, true} {
			for _, prefix := range []string{"", "σσσ"} {
				want := prefix + "μα"
				if incoming {
					want = prefix + "μ"
				}
				greekAssertStage(t, (*greekState).step6, prefix+ending, want, incoming, incoming)
			}
			for _, tail := range []string{"!", "\u0301"} {
				input := "σσσ" + ending + tail
				greekAssertStage(t, (*greekState).step6, input, input, incoming, incoming)
			}
			short := string([]rune(ending)[:len([]rune(ending))-1])
			want := "σσσ" + short
			if incoming {
				want = strings.TrimSuffix(want, testGreekLongest(short, endings))
			}
			greekAssertStage(t, (*greekState).step6, "σσσ"+short, want, incoming, incoming)
		}
	}
	for _, incoming := range []bool{false, true} {
		greekAssertStage(t, (*greekState).step6, "σσσ", "σσσ", incoming, incoming)
	}
	// Proper suffix prefixes either match another independently listed member,
	// or preserve text/flag. A changed final rune blocks every stage.
	for _, stage := range greekStageManifest() {
		for _, ending := range strings.Fields(stage.endings) {
			short := string([]rune(ending)[:len([]rune(ending))-1])
			matched := testGreekLongest(short, stage.endings)
			for _, incoming := range []bool{false, true} {
				want, flag := "σσσ"+short, incoming
				if matched != "" {
					want, flag = strings.TrimSuffix(want, matched)+stage.base, incoming && stage.retain
				}
				greekAssertStage(t, stage.run, "σσσ"+short, want, incoming, flag)
			}
		}
	}
}
