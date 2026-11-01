package functions

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestSnowballRussianRules(t *testing.T) {
	// Independent REL_18_6 suffix transcription and historical snowball-data
	// 4f982ec072a091771b321ab7861841471942e14a pairs, never production tables.
	cases := [][2]string{
		{"абиссинию", "абиссин"}, {"абонемента", "абонемент"},
		{"абонировался", "абонирова"}, {"абонируйся", "абонир"},
		{"абонируюсь", "абонир"}, {"абрикосы", "абрикос"},
		{"авиация", "авиац"}, {"авось", "ав"}, {"австрийский", "австрийск"},
		{"автомобили", "автомоб"}, {"автомобиль", "автомобил"},
		{"агроном", "агрон"}, {"агрономии", "агроном"},
		{"администратор", "администратор"}, {"адресовано", "адресова"},
		{"ав", "ав"}, {"абв", "абв"}, {"абав", "аба"}, {"абив", "аб"},
		{"абывшись", "аб"}, {"ала", "ал"}, {"абала", "аба"}, {"абла", "абл"},
		{"абил", "аб"}, {"абилсь", "аб"}, {"абился", "аб"}, {"абавшая", "аба"}, {"абившая", "аб"},
		{"величие", "велич"}, {"кость", "кост"}, {"абабость", "абаб"},
		{"абанн", "абан"}, {"абаннейший", "абан"}, {"абаннейше", "абан"},
		{"абейш", "аб"}, {"абейше", "аб"}, {"анн", "ан"},
		{"", ""}, {"ё", "е"}, {"ёлки", "елк"}, {"Ё", "Ё"},
		{"МАШИНЫ", "МАШИНЫ"}, {"Машины", "Машин"}, {"машины", "машин"},
		{"машины!", "машины!"}, {"машины ", "машины "}, {"машины\u0301", "машины\u0301"},
		{"Xабала", "Xаба"}, {"éабала", "éаба"}, {"😀абала", "😀аба"},
	}
	for _, group := range []struct{ prefix, endings, tail, want string }{
		{"аба", "в вши вшись", "", "аба"},
		{"аб", "ив ивши ившись ыв ывши ывшись", "", "аб"},
		{"аб", "ее ие ые ое ими ыми ей ий ый ой ем им ым ом его ого ему ому их ых ую юю ая яя ою ею", "", "аб"},
		{"аба", "ем нн вш ющ щ", "ая", "аба"},
		{"аб", "ивш ывш ующ", "ая", "аб"},
		{"аба", "ла на ете йте ли й л ем н ло но ет ют ны ть ешь нно", "", "аба"},
		{"аб", "ила ыла ена ейте уйте ите или ыли ей уй ил ыл им ым ен ило ыло ено ят ует уют ит ыт ены ить ыть ишь ую ю", "", "аб"},
		{"аб", "а ев ов ие ье е иями ями ами еи ии и ией ей ой ий й иям ям ием ем ам ом о у ах иях ях ы ь ию ью ю ия ья я", "", "аб"},
		{"абаб", "ост ость", "", "абаб"},
	} {
		for suffix := range strings.FieldsSeq(group.endings) {
			cases = append(cases, [2]string{group.prefix + suffix + group.tail, group.want})
		}
	}
	for _, tc := range cases {
		if got := russianStem(tc[0]); got != tc[1] {
			t.Errorf("%q: got %q, want %q", tc[0], got, tc[1])
		}
	}
	for _, tc := range []struct {
		word   string
		rv, r2 int
	}{
		{"", 0, 0}, {"б", 2, 2}, {"а", 2, 2}, {"абаб", 2, 8},
		{"абаба", 2, 8}, {"éабаб", 4, 10}, {"😀абаб", 6, 12},
		{"а\u0301аб", 2, 8}, {"АБАБ", 8, 8},
	} {
		if rv, r2 := russianRegions(tc.word); rv != tc.rv || r2 != tc.r2 {
			t.Error("fixed byte regions", tc, rv, r2)
		}
	}
	// Every conditional suffix: the predecessor must be а/я AND in RV.
	// Calling the actual stage matcher separates predicates from later noun work.
	for _, group := range []struct {
		endings string
		table   []russianSuffix
	}{
		{"в вши вшись", russianPerfective[:]},
		{"ем нн вш ющ щ", russianParticiple[:]},
		{"ла на ете йте ли й л ем н ло но ет ют ны ть ешь нно", russianVerb[:]},
	} {
		for suffix := range strings.FieldsSeq(group.endings) {
			for _, prefix := range []string{"аба", "абя", "абб"} {
				word := prefix + suffix
				end, ok := russianMatch(word, 2, group.table)
				want := prefix != "абб"
				if ok != want || ok && end != len(prefix) {
					t.Error("conditional action", word, end, ok)
				}
			}
			if _, ok := russianMatch("а"+suffix, 2, group.table); ok {
				t.Error("predecessor before RV", suffix)
			}
		}
	}
	for _, tc := range []struct {
		word     string
		boundary int
		table    []russianSuffix
		end      int
		ok       bool
	}{
		{"абив", 4, russianPerfective[:], 4, true},
		{"абив", 5, russianPerfective[:], 8, false},
		{"абивш", 4, russianParticiple[:], 4, true},
		{"абивш", 5, russianParticiple[:], 10, false},
		{"абейте", 4, russianVerb[:], 4, true},
		{"абейте", 5, russianVerb[:], 12, false},
		{"абие", 4, russianNoun[:], 4, true},
		{"абие", 5, russianNoun[:], 6, true},
	} {
		if end, ok := russianMatch(tc.word, tc.boundary, tc.table); end != tc.end || ok != tc.ok {
			t.Error("longest in bounded region, no predicate retry", tc.word, end, ok)
		}
	}
	for _, r := range "аеиоуыэюя" {
		if !russianVowel(r) {
			t.Errorf("released vowel %q", r)
		}
	}
	for _, r := range "ёАЕИОУЫЭЮЯaeiouйь\u0301" {
		if russianVowel(r) {
			t.Errorf("not a released vowel %q", r)
		}
	}
}

func TestSnowballRussianDictionaryResources(t *testing.T) {
	ctx := t.Context()
	d, err := DefaultRussianSnowballDictionary()
	if err != nil {
		t.Fatal(err)
	}
	// Complete independent released inventory, including stop-before-ё policy.
	stops := strings.Fields("и в во не что он на я с со как а то все она так его но да ты к у же вы за бы по только ее мне было вот от меня еще нет о из ему теперь когда даже ну вдруг ли если уже или ни быть был него до вас нибудь опять уж вам ведь там потом себя ничего ей может они тут где есть надо ней для мы тебя их чем была сам чтоб без будто чего раз тоже себе под будет ж тогда кто этот того потому этого какой совсем ним здесь этом один почти мой тем чтобы нее сейчас были куда зачем всех никогда можно при наконец два об другой хоть после над больше тот через эти нас про всего них какая много разве три эту моя впрочем хорошо свою этой перед иногда лучше чуть том нельзя такой им более всегда конечно всю между")
	slices.Sort(stops)
	if len(stops) != 151 || !slices.Equal(d.words, stops) {
		t.Fatal("released stoplist", d.words)
	}
	// A serial cold-cache load cancels inside the real stopword compiler.
	// Restore the original backing even on failure; singleton owners retain it.
	func() {
		dictionaryStopwords.Lock()
		saved := dictionaryStopwords.words["russian"]
		delete(dictionaryStopwords.words, "russian")
		dictionaryStopwords.Unlock()
		defer func() {
			dictionaryStopwords.Lock()
			dictionaryStopwords.words["russian"] = saved
			dictionaryStopwords.Unlock()
		}()
		canceled, cancel := context.WithCancel(ctx)
		defer cancel()
		scanning := &russianLoadContext{Context: canceled, cancel: cancel, remaining: 8}
		bad, err := CompileSnowballDictionary(scanning, "russian", "russian")
		if bad != nil || !errors.Is(err, context.Canceled) || canceled.Err() != context.Canceled {
			t.Fatal("real compiler cancellation", bad, err)
		}
		dictionaryStopwords.Lock()
		_, published := dictionaryStopwords.words["russian"]
		dictionaryStopwords.Unlock()
		if published {
			t.Fatal("canceled cold load published cache entry")
		}
	}()
	check := func(runtime *SnowballDictionary, input, want string) {
		t.Helper()
		got, err := runtime.Lexize(ctx, input)
		if err != nil || !got.Recognized || want == "" && len(got.Lexemes) != 0 || want != "" && (len(got.Lexemes) != 1 || got.Lexemes[0].Text != want) {
			t.Error(input, got, want, err)
		}
		for _, lexeme := range got.Lexemes {
			if lexeme.Prefix || lexeme.Variant != 0 {
				t.Error("lexeme flags", lexeme)
			}
		}
	}
	for _, word := range stops {
		check(d, word, "")
	}
	for _, tc := range [][2]string{
		{"", ""}, {"МАШИНЫ", "МАШИНЫ"}, {"Машины", "Машин"},
		{"И", "И"}, {"В", "В"}, {"НЕ", "НЕ"}, {"всё", "все"}, {"её", "е"}, {"ещё", "ещ"},
		{"ё", "е"}, {"Ё", "Ё"}, {"ёлки", "елк"}, {"RUNNING", "running"},
		{strings.Repeat("я", 498) + "ам", strings.Repeat("я", 498)},
		{strings.Repeat("я", 498) + "Aам", strings.Repeat("я", 498) + "aам"},
		{strings.Repeat("я", 498) + "ами", strings.Repeat("я", 498) + "ами"},
		{"ё" + strings.Repeat("я", 498) + "ами", "ё" + strings.Repeat("я", 498) + "ами"},
	} {
		check(d, tc[0], tc[1])
	}
	free, err := CompileSnowballDictionary(ctx, "russian", "")
	if err != nil || free.words != nil {
		t.Fatal(free, err)
	}
	check(free, "все", "все")
	mixed, err := CompileSnowballDictionary(ctx, "RuSsIaN", "russian")
	if err != nil || mixed.provider != d.provider || !mixed.Configured("RuSsIaN", "russian") || mixed.Configured("russian", "russian") {
		t.Fatal("provider authority and exact spelling", mixed, err)
	}
	simple, err := CompileSimpleDictionary(ctx, true, "russian")
	if err != nil {
		t.Fatal(err)
	}
	if &mixed.words[0] != &d.words[0] || &simple.words[0] != &d.words[0] {
		t.Fatal("shared immutable backing")
	}
	for _, word := range stops {
		if got, err := simple.Lexize(ctx, word); err != nil || !got.Recognized || len(got.Lexemes) != 0 {
			t.Fatal(word, got, err)
		}
	}
	if got, err := simple.Lexize(ctx, "машины"); err != nil || len(got.Lexemes) != 1 || got.Lexemes[0].Text != "машины" {
		t.Fatal("simple does not stem", got, err)
	}
	first, err := d.Lexize(ctx, "машины")
	if err != nil {
		t.Fatal(err)
	}
	first.Lexemes[0].Text = "changed"
	check(d, "машины", "машин")
	for _, resource := range []string{"russian_missing", "RUSSIAN", "../russian"} {
		if bad, err := CompileSnowballDictionary(ctx, "russian", resource); bad != nil || err == nil {
			t.Fatal(resource, bad, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if bad, err := CompileSnowballDictionary(canceled, "russian", "russian"); bad != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(bad, err)
	}
	if _, err := d.Lexize(canceled, "машины"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	dictionaryStopwords.Lock()
	_, missing := dictionaryStopwords.words["russian_missing"]
	backing := dictionaryStopwords.words["russian"]
	dictionaryStopwords.Unlock()
	if missing || &backing[0] != &d.words[0] || !slices.Equal(backing, stops) {
		t.Fatal("failed compile changed cache")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 32 {
				check(d, "машины", "машин")
				check(d, "все", "")
				check(free, "все", "все")
			}
		})
	}
	wg.Wait()
}

type russianLoadContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *russianLoadContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}
