package functions

import "testing"

// Literal stage outcomes are traced from REL_18_6's generated routines, not
// produced by the Go tables. The ASCII sentinel isolates each suffix boundary.
func TestSnowballTamilRules(t *testing.T) {
	stages := []struct {
		name string
		run func(*tamilWord)
		pairs [][2]string
	}{
		{"ending", func(w *tamilWord) { w.fixEnding() }, [][2]string{
			{"abcdந்", "abcd"}, {"abcdந்த", "abcd"}, {"abcdந்த்", "abcd"},
			{"abcdியை்", "abcdியை"}, {"abcdியைய்", "abcdியை"}, {"abcdிய்", "abcdி"}, {"abcdீய்", "abcdீ"},
			{"abcdய்", "abcdய்"}, {"abcdட்ப்", "abcdள்"}, {"abcdட்க்", "abcdள்"}, {"abcdன்ற்", "abcdல்"}, {"abcdற்க்", "abcdல்"},
			{"abcdட்ட்", "abcdடு"}, {"abcdத்த்", "abcd"}, {"abcdுக்", "abcd்"}, {"abcdுக்க்", "abcd்"},
			{"abcdவ", "abcd"}, {"abcdய", "abcd"}, {"abcdவ்", "abcd"}, {"abcdனு", "abcd"}, {"abcdானு", "abcdானு"},
			{"abcdங்", "abcdம்"}, {"abcdைங்", "abcdை"}, {"abcd்ங்", "abcd்"},
			{"abcdக்", "abcd"}, {"abcdச்", "abcd"}, {"abcdட்", "abcd"}, {"abcdத்", "abcd"}, {"abcdப்", "abcd"}, {"abcdற்", "abcd"},
			{"abcdக்ப்", "abcd"}, {"abcd்ய்", "abcd்"}, {"abcd்ர்", "abcd்"}, {"abcd்ல்", "abcd்"}, {"abcd்வ்", "abcd்"},
			{"abcd்ழ்", "abcd்"}, {"abcd்ள்", "abcd்"}, {"abcd்ஞ்", "abcd்"}, {"abcd்ண்", "abcd்"}, {"abcd்ந்", "abcd்"}, {"abcd்ம்", "abcd்"}, {"abcd்ன்", "abcd்"},
			{"abcdா்", "abcdா"}, {"abcdி்", "abcdி"}, {"abcdீ்", "abcdீ"}, {"abcdெ்", "abcdெ"}, {"abcdே்", "abcdே"}, {"abcdு்", "abcdு"}, {"abcdூ்", "abcdூ"}, {"abcdை்", "abcdை"}, {"abcd்்", "abcd்"},
			{"aக்", "aக்"}, {"abக்", "ab"}, {"abcd!", "abcd!"},
		}},
		{"prefix_question", func(w *tamilWord) { w.prefix(true) }, [][2]string{
			{"எக்வோabcd", "ஓabcd"}, {"எச்வொabcd", "ஒabcd"}, {"எத்வுabcd", "உabcd"}, {"எவ்வூabcd", "ஊabcd"},
			{"எந்abcd", "abcd"}, {"எப்abcd", "abcd"}, {"எம்abcd", "abcd"}, {"எய்abcd", "abcd"}, {"எங்abcd", "abcd"}, {"எஞ்abcd", "abcd"},
			{"எக்abcd", "abcd"}, {"எகabcd", "எகabcd"}, {"அக்abcd", "அக்abcd"},
		}},
		{"prefix_pronoun", func(w *tamilWord) { w.prefix(false) }, [][2]string{
			{"அக்வோabcd", "ஓabcd"}, {"இச்வொabcd", "ஒabcd"}, {"உத்வுabcd", "உabcd"}, {"அவ்வூabcd", "ஊabcd"}, {"எக்abcd", "எக்abcd"},
			{"அந்abcd", "abcd"}, {"இப்abcd", "abcd"}, {"உம்abcd", "abcd"}, {"அய்abcd", "abcd"}, {"இங்abcd", "abcd"}, {"உஞ்abcd", "abcd"},
			{"அகabcd", "அகabcd"}, {"ஆக்abcd", "ஆக்abcd"},
		}},
		{"question", (*tamilWord).question, [][2]string{{"abcdோ", "abcd்"}, {"abcdே", "abcd்"}, {"abcdா", "abcd்"}, {"abா", "abா"}, {"abcdவவ", "abcd"}}},
		{"um", (*tamilWord).um, [][2]string{{"abcdும்", "abcd்"}, {"aும்", "aும்"}, {"abcd!", "abcd!"}}},
		{"plural", (*tamilWord).plural, [][2]string{{"abcdுங்கள்", "abcd்"}, {"abcdகுங்கள்", "abcdகுங்"}, {"abcdசுங்கள்", "abcdசுங்"}, {"abcdடுங்கள்", "abcdடுங்"}, {"abcdதுங்கள்", "abcdதுங்"}, {"abcdபுங்கள்", "abcdபுங்"}, {"abcdறுங்கள்", "abcdறுங்"}, {"abcdற்கள்", "abcdல்"}, {"abcdட்கள்", "abcdள்"}, {"abcdகள்", "abcd"}, {"abcd!", "abcd!"}}},
		{"command", (*tamilWord).command, [][2]string{{"abcdபி", "abcd"}, {"abcdவி", "abcd"}, {"aபி", "aபி"}}},
		{"common", (*tamilWord).common, [][2]string{
			{"abcdுடன்", "abcd்"}, {"abcdில்லை", "abcd்"}, {"abcdிடம்", "abcd்"}, {"abcdின்றி", "abcd்"}, {"abcdாகி", "abcd்"}, {"abcdாகிய", "abcd்"}, {"abcdென்று", "abcd்"}, {"abcdுள்ள", "abcd்"}, {"abcdுடைய", "abcd்"}, {"abcdுடை", "abcd்"}, {"abcdெனும்", "abcd்"}, {"abcdென", "abcd்"}, {"abcdல்ல", "abcd்"}, {"abcdால்ல", "abcdால்ல"},
			{"abcdபடு", "abcd"}, {"abcdபட்ட", "abcd"}, {"abcdபட்டு", "abcd"}, {"abcdபட்டது", "abcd"}, {"abcdபட்டண", "abcd"}, {"abcdகுரிய", "abcd"}, {"abcdபற்றி", "abcd"}, {"abcdவிடு", "abcd"}, {"abcdவிட்டு", "abcd"}, {"abcdபடிதான", "abcd"}, {"abcdபடி", "abcd"}, {"abcdதான", "abcd"}, {"abcdெல்லாம்", "abcd"}, {"aென", "aென"},
		}},
		{"case", (*tamilWord).cases, [][2]string{
			{"abcdனை", "abcd"}, {"abcdொடு", "abcd்"}, {"abcdோடு", "abcd்"}, {"abcdில்", "abcd்"}, {"abcdிற்", "abcd்"}, {"abcdின்று", "abcd்"}, {"abcdிருந்து", "abcd்"}, {"abcdவிட", "abcd்"}, {"abcdால்", "abcd்"}, {"abcdுடை", "abcd்"}, {"abcdாமல்", "abcd்"}, {"abcdுள்", "abcd்"}, {"abcdின்", "abcd்"}, {"abcdமின்", "abcdமின்"}, {"abcdிடம்", "abcd்"}, {"abிடம்", "abிடம்"},
			{"abcdல்", "abcd்"}, {"abcdால்", "abcd்"}, {"abcdகண்", "abcd"}, {"abcdமுன்", "abcd"}, {"abcdமேல்", "abcd"}, {"abcdமேற்", "abcd"}, {"abcdகீழ்", "abcd"}, {"abcdது", "abcd"}, {"abcdாது", "abcdாது"}, {"abcdீ", "abcdி"}, {"abcdை", "abcd்"}, {"abcdகை", "abcdகை"}, {"abcd்கை", "abcd்"},
		}},
		{"tense", func(w *tamilWord) { w.tense() }, [][2]string{
			{"abcdகொண்டிர்", "abcd"}, {"abcdபடு", "abcd"}, {"abcdமார்", "abcd"}, {"abcdமின்", "abcd"}, {"abcdனன்", "abcd"}, {"abcdனான்", "abcd"}, {"abcdனாள்", "abcd"}, {"abcdனார்", "abcd"}, {"abcdனள்", "abcd"}, {"abcdவள்", "abcd"}, {"abcdனர்", "abcd"}, {"abcdவர்", "abcd"}, {"abcdன", "abcd"}, {"abcdப", "abcd"}, {"abcdக", "abcd"}, {"abcdத", "abcd"}, {"abcdய", "abcd"}, {"abcdபன்", "abcd"}, {"abcdபள்", "abcd"}, {"abcdபர்", "abcd"}, {"abcdிற்று", "abcd"}, {"abcdபம்", "abcd"}, {"abcdனம்", "abcd"}, {"abcdதும்", "abcd"}, {"abcdறும்", "abcd"}, {"abcdகும்", "abcd"}, {"abcdனென்", "abcd"}, {"abcdனை", "abcd"}, {"abcdவை", "abcd"},
			{"abcdவன்", "abcd"}, {"abcdஅவன்", "abcdஅவன்"}, {"abcdது", "abcd"}, {"abcdாது", "abcdாது"}, {"abcdான்", "abcd்"}, {"abcdசான்", "abcdசான்"}, {"abcdாள்", "abcd்"}, {"abcdார்", "abcd்"}, {"abcdேன்", "abcd்"}, {"abcdா", "abcd்"}, {"abcdாம்", "abcd்"}, {"abcdெம்", "abcd்"}, {"abcdேம்", "abcd்"}, {"abcdோம்", "abcd்"}, {"abcdடும்", "abcd்"}, {"abcdாய்", "abcd்"}, {"abcdனிர்", "abcd்"}, {"abcdீர்", "abcd்"}, {"abcdீயர்", "abcd்"}, {"abcd்கு", "abcd்"}, {"abcdகு", "abcdகு"},
			{"abcdகின்றப", "abcd"}, {"abcdாநின்றப", "abcd"},
			{"abcdாநின்ற", "abcd"}, {"abcdாநின்ற்", "abcd"}, {"abcdகின்ற", "abcd"}, {"abcdகின்ற்", "abcd"}, {"abcdகிற", "abcd"}, {"abcdகிற்", "abcd"}, {"abா", "abா"},
		}},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			for _, pair := range stage.pairs {
				w := tamilWord{word: pair[0]}
				stage.run(&w)
				if w.word != pair[1] { t.Errorf("%q = %q, want %q", pair[0], w.word, pair[1]) }
			}
		})
	}
	for _, tc := range []struct { input, want string; flag bool }{{"abcdத்த்", "abcdம்", true}, {"abcdைத்த்", "abcdை", true}, {"abcdத்த்", "abcd", false}} {
		w := tamilWord{word: tc.input, caseRemoved: tc.flag}
		w.fixEnding()
		if w.word != tc.want { t.Errorf("case repair %q flag %v = %q, want %q", tc.input, tc.flag, w.word, tc.want) }
	}
	for _, tc := range []struct { input, want string; flag bool }{
		{"abcdத்தில்", "abcdம்", true},
		{"abcdமின்", "abcdமின்", false},
		{"abcd!", "abcd!", false},
	} {
		w := tamilWord{word: tc.input, caseRemoved: true}
		w.cases()
		if w.word != tc.want || w.caseRemoved != tc.flag {
			t.Errorf("case flag %q = %q/%v, want %q/%v", tc.input, w.word, w.caseRemoved, tc.want, tc.flag)
		}
	}
	for _, pair := range [][2]string{
		{"கள்", "கள்"}, {"அகள்", "அகள்"}, {"அஅகள்", "அஅ"}, {"abக்", "ab"},
		{"தமிழில்", "தமிழ்"}, {"மரத்தில்", "மரம்"}, {"புத்தகங்கள்", "புத்தகம்"}, {"வீடுகள்", "வீடு"},
		{"நடந்தான்", "நட"}, {"படித்தான்", "படி"}, {"மரங்களைவிட", "மரங்களை"}, {"வீடுகளுக்கு", "வீடுகள்"}, {"வீடுகளுக்கும்", "வீடு"},
		{"abcdவவ", "abcd"}, {"abcdபப", "abcd"}, {"RUNNING", "RUNNING"},
	} {
		if got := tamilStem(pair[0]); got != pair[1] { t.Errorf("stem %q = %q, want %q", pair[0], got, pair[1]) }
	}
}
