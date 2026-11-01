package functions

import "testing"

func frenchFinalStageRules(t *testing.T) {
	t.Helper()
	for _, tc := range [][2]string{
		{"bababtion", "bababt"}, {"bababxion", "bababxion"}, {"baption", "baption"},
		{"babier", "babi"}, {"babière", "babi"}, {"babIer", "babi"}, {"babIère", "babi"},
		{"babe", "bab"}, {"be", "be"},
		{"bababs", "babab"}, {"babas", "babas"}, {"babis", "babis"}, {"babos", "babos"},
		{"babus", "babus"}, {"babès", "babès"}, {"babss", "babss"}, {"babaHis", "babaHi"}, {"s", "s"}, {"", ""},
	} {
		if got := string(frenchResidual([]rune(tc[0]), 2, 5)); got != tc[1] {
			t.Errorf("residual %q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	for _, tc := range [][2]string{
		{"bababenne", "bababen"}, {"bababonne", "bababon"}, {"bababette", "bababet"},
		{"bababelle", "bababel"}, {"bababeille", "bababeil"},
		{"bababçif", "bababc"}, {"bababç", "bababç"}, {"payer", "pai"}, {"cèd", "ced"}, {"cé", "cé"},
	} {
		if got := frenchStem(tc[0]); got != tc[1] {
			t.Errorf("final stage %q: got %q, want %q", tc[0], got, tc[1])
		}
	}
}
