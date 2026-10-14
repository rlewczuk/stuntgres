package functions

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// Independent published expected arrays: PostgreSQL18 §F.16.2 and REL_18_0
// contrib/fuzzystrmatch/expected/fuzzystrmatch{,_utf8}.out, never native output.
func TestDaitchMokotoffPublishedCodes(t *testing.T) {
	for _, tc := range []struct{ input, output string }{
		{"George", "595000"}, {"John", "160000,460000"},
		{"Bierschbach", "794575,794574,794750,794740,745750,745740,747500,747400"},
		{"Schwartzenegger", "479465"}, {"Müller", "689000"}, {"Schäfer", "479000"},
		{"Straßburg", "294795"}, {"Éregon", "095600"}, {"gąszczu", "564000,540000"},
		{"brzęczy", "794640,794400,746400,744000"}, {"ţamas", "364000,464000"}, {"țamas", "364000,464000"},
		{"Augsburg", "054795"}, {"Breuer", "791900"}, {"Freud", "793000"},
		{"Halberstadt", "587943,587433"}, {"Mannheim", "665600"},
		{"Chernowitz", "596740,496740"}, {"Cherkassy", "595400,495400"},
		{"Kleinman", "586660"}, {"Nowy Targ", "673950"}, {"Berlin", "798600"},
		{"Ceniow", "567000,467000"}, {"Tsenyuv", "467000"}, {"Holubica", "587500,587400"},
		{"Golubitsa", "587400"}, {"Przemysl", "794648,746480"}, {"Pshemeshil", "746480"},
		{"Rosochowaciec", "945755,945754,945745,945744,944755,944754,944745,944744"},
		{"Rosokhovatsets", "945744"}, {"'OBrien", "079600"}, {"O'Brien", "079600"},
		{"CJC", "550000,540000,545000,450000,400000,440000"}, {"BESST", "743000"},
		{"BOUEY", "710000"}, {"HANNMANN", "566600"},
		{"MCCOYJR", "651900,654900,654190,654490,645190,645490,641900,644900"},
		{"ACCURSO", "059400,054000,054940,054400,045940,045400,049400,044000"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := DaitchMokotoff(context.Background(), tc.input)
			if err != nil || !reflect.DeepEqual(got, strings.Split(tc.output, ",")) {
				t.Fatalf("codes=%v err=%v want=%s", got, err, tc.output)
			}
			seen := map[string]bool{}
			for _, code := range got {
				if len(code) != 6 || seen[code] {
					t.Fatal("non-six-digit or duplicate code", code)
				}
				seen[code] = true
				for i := range code {
					if code[i] < '0' || code[i] > '9' {
						t.Fatal("nondecimal code", code)
					}
				}
			}
		})
	}
}

func TestDaitchMokotoffBoundaries(t *testing.T) {
	for _, word := range []string{"", " \t\n", "-.,![]\\", "0123456789", "東京🙂", "Жуков"} {
		if got, err := DaitchMokotoff(context.Background(), word); got != nil || err != nil {
			t.Fatal("no encodable input", word, got, err)
		}
	}
	for _, tc := range []struct{ word, want string }{
		{"A", "000000"}, {strings.Repeat("B", 1024), "700000"},
		{"john", "160000,460000"}, {"JOHN", "160000,460000"},
		{"S-c h w a r t z e n e g g e r", "479465"}, {"Strasburg", "294795"},
		{"Mu\u0308ller", "689000"}, {"łJohn", "160000,460000"}, {"ß", "400000"},
	} {
		got, err := DaitchMokotoff(context.Background(), tc.word)
		if err != nil || !reflect.DeepEqual(got, strings.Split(tc.want, ",")) {
			t.Fatal(tc.word, got, tc.want, err)
		}
	}
	// Returned buffers never alias state from later evaluations.
	first, err := DaitchMokotoff(context.Background(), "John")
	if err != nil {
		t.Fatal(err)
	}
	first[0] = "changed"
	second, err := DaitchMokotoff(context.Background(), "John")
	if err != nil || second[0] != "160000" {
		t.Fatal("call-local result ownership", second, err)
	}
}

// Cancel a real context at a deterministic checkpoint, without timing sleeps
// or a worker that might escape on failure. Err checks still delegate to it.
type dmCancelAtCheckpoint struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *dmCancelAtCheckpoint) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestDaitchMokotoffCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := DaitchMokotoff(ctx, "John"); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("precancel", got, err)
	}
	for _, word := range []string{strings.Repeat("🙂", 1024), "S" + strings.Repeat("-", 1024) + "CH", strings.Repeat("CJC", 1024)} {
		ctx, cancel := context.WithCancel(context.Background())
		c := &dmCancelAtCheckpoint{Context: ctx, cancel: cancel, remaining: 20}
		got, err := DaitchMokotoff(c, word)
		cancel()
		if got != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("incremental cancellation", got, err)
		}
	}
}
