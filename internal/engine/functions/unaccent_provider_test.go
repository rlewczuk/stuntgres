package functions

import (
	"context"
	"errors"
	"testing"
)

func TestExtensionRules(t *testing.T) {
	stockUnaccent := StockUnaccent
	ctx := context.Background()
	for _, tc := range []struct{ input, want string }{{"Hôtel", "Hotel"}, {"Æsir Straße Œuf ﬃ", "AEsir Strasse OEuf ffi"}, {"À", "A"}, {"1½", "1 1/2"}, {"℃℉", "°C°F"}, {"℗", "(P)"}, {"ёлка", "елка"}, {"ЁЖИК", "ЕЖИК"}, {"ℌ", "H"}, {"〝", "\""}, {"東京🙂", "東京🙂"}, {"foobar", "foobar"}, {"", ""}} {
		got, err := stockUnaccent.Text(ctx, tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("%q => %q want %q: %v", tc.input, got, tc.want, err)
		}
	}
	if allocs := testing.AllocsPerRun(100, func() {
		_, err := stockUnaccent.Text(ctx, "unchanged 東京🙂")
		if err != nil {
			panic(err)
		}
	}); allocs != 0 {
		t.Fatal("unchanged output allocated", allocs)
	}
	matcher := CompileUnaccent("a\tx\nab\ty\n")
	got, changed, err := matcher.lexize(ctx, matcher.init(), "abc")
	if err != nil || !changed || got != "yc" {
		t.Fatal("longest match", got, changed, err)
	}
	_, changed, err = stockUnaccent.lexize(ctx, stockUnaccent.init(), "foobar")
	if err != nil || changed {
		t.Fatal("unchanged lexize", changed, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := stockUnaccent.Text(canceled, "Hôtel"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
