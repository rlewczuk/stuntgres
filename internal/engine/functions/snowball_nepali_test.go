package functions

import (
	"strings"
	"testing"
)

func TestSnowballNepaliRules(t *testing.T) {
	// Independent release transcription, not iteration over implementation tables.
	// The prefix घर survives every literal suffix in this corpus; category-one
	// exclusions allow the overlapping category-three entries to be selected.
	for _, suffix := range strings.Fields("की लाइ ले लाई कै सँगै मै को सँग संग मार्फत रत का मा द्वारा कि पछि") {
		if got := nepaliStem("घर" + suffix); got != "घर" {
			t.Errorf("category one %q: %q", suffix, got)
		}
	}
	category3 := strings.Fields(`
ेकी एकी इएकी िएकी देखी थी दी छु ेछु नेछु एछु
नु हरु हरू छे थे ने ेकै नेकै एकै दै इदै िदै
ेको नेको एको इएको िएको दो इदो िदो यो इयो थ्यो
भयो ियो थियो दियो छौ इछौ ेछौ नेछौ एछौ िछौ यौ
छ्यौ थ्यौ थियौ छन् इछन् ेछन् नेछन् एछन् िछन् लान्
छिन् थिन् पर् इस् थिइस् छेस् होस् छस् इछस् ेछस्
नेछस् एछस् िछस् िस् थिस् थिए छ इछ ेछ नेछ हुनेछ
हुन्छ इन्छ िन्छ एछ िछ ेका नेका एका इएका िएका दा
इदा िदा देखि माथि
`)
	if len(category3) != 91 {
		t.Fatal("independent released category-three inventory", len(category3))
	}
	for _, suffix := range category3 {
		if got := nepaliStem("घर" + suffix); got != "घर" {
			t.Errorf("category three %q: %q", suffix, got)
		}
	}
	for _, tc := range [][2]string{
		{"घरमामा", "घरमा"}, {"घरहरूमामा", "घरहरूमा"},
		{"घरएकि", "घरएकि"}, {"घरेकि", "घरेकि"},
		{"घरएकी", "घर"}, {"घरेकी", "घर"}, {"घरएको", "घर"}, {"घरेको", "घर"},
		{"घरत्रै", "घरत्र"}, {"त्रै", "त्र"}, {"घरत्रैँ", "घरत्रैँ"},
		{"घरकाँ", "घरकाँ"}, {"घरकं", "घरकं"}, {"घरकँ", "घरकँ"},
		{"घरहरूहरूनु", "घर"}, {"घरको!", "घरको!"}, {"घरको ", "घरको "},
		{"गर्दै", "गर्"}, {"गरेको", "गर"}, {"किताबहरूलाई", "किताब"},
		{"घरनौँ", "घरनौ"}, {"घरनौं", "घरनौ"},
		{"नु", ""}, {"हरू", ""}, {"ले", ""}, {"", ""},
		{"RATS", "RATS"}, {"CAFÉS", "CAFÉS"}, {"café", "café"}, {"cafe\u0301", "cafe\u0301"},
	} {
		if got := nepaliStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	for _, context := range []string{"यौ", "छौ", "नौ", "थे"} {
		want := "घर"
		if context == "नौ" {
			want = "घरनौ"
		}
		for _, mark := range []string{"ँ", "ं"} {
			if got := nepaliStem("घर" + context + mark); got != want {
				t.Errorf("mark context %q: got %q, want %q", context+mark, got, want)
			}
		}
	}
}
