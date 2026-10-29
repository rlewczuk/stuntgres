package parser

import "testing"

func TestKeywordMetadata(t *testing.T) {
	if KeywordCount() != 494 { t.Fatal("released count", KeywordCount()) }
	for _, index := range []int{-1, 494, 495} {
		if value, ok := KeywordAt(index); ok || value != (KeywordMetadata{}) { t.Fatal("invalid index", index, value, ok) }
	}
	for i, private := range keywords {
		value, ok := KeywordAt(i)
		if !ok || value.Word != private.word || value.Category != byte(private.category) || value.BareLabel != private.bareLabel { t.Fatal("metadata mismatch", i, value) }
		value.Word, value.Category, value.BareLabel = "changed", 'X', !value.BareLabel
		again, _ := KeywordAt(i)
		if again.Word != private.word || again.Category != byte(private.category) || again.BareLabel != private.bareLabel { t.Fatal("metadata escaped by reference", i) }
	}
	for _, want := range []KeywordMetadata{
		{"abort",'U',true},{"between",'C',true},{"authorization",'T',true},{"select",'R',true},
		{"char",'C',false},{"day",'U',false},{"isnull",'T',false},{"from",'R',false},{"virtual",'U',true},
	} {
		found := false
		for i := range KeywordCount() { got,_ := KeywordAt(i); if got.Word == want.Word { found = true; if got != want { t.Fatal(got,want) } } }
		if !found { t.Fatal("missing literal witness",want) }
	}
}
