package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRussianSnowballStopwords(t *testing.T) {
	// Independent, complete REL_18_6 snowball/stopwords/russian.stop literal.
	words := strings.Fields(`и в во не что он на я с со как а то все она так его но да ты к у же вы за бы по только ее мне было вот от меня еще нет о из ему теперь когда даже ну вдруг ли если уже или ни быть был него до вас нибудь опять уж вам ведь там потом себя ничего ей может они тут где есть надо ней для мы тебя их чем была сам чтоб без будто чего раз тоже себе под будет ж тогда кто этот того потому этого какой совсем ним здесь этом один почти мой тем чтобы нее сейчас были куда зачем всех никогда можно при наконец два об другой хоть после над больше тот через эти нас про всего них какая много разве три эту моя впрочем хорошо свою этой перед иногда лучше чуть том нельзя такой им более всегда конечно всю между`)
	if len(words) != 151 {
		t.Fatal("released Russian stopword inventory", len(words))
	}
	for start := 0; start < len(words); start += 25 {
		end := min(start+25, len(words))
		group := words[start:end]
		t.Run(group[0], func(t *testing.T) {
			portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY russian_listless (TEMPLATE=snowball,Language=russian)", 0)
				regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY russian_simple (TEMPLATE=simple,StopWords=russian)", 0)
				q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				for _, word := range group {
					for _, e := range []fixture.Executor{c, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}} {
						for _, dict := range []string{"russian_stem", "russian_simple"} {
							dictionaryResult(t, p, b, e, q, "{}", dict, word)
						}
						// Without a list Snowball recognizes every nonempty token;
						// a stopword must not disappear through hidden stock policy.
						rows, err := e.QueryContext(p.Ctx, q, "russian_listless", word)
						if err != nil {
							t.Fatal(err)
						}
						if !rows.Next() {
							rows.Close()
							t.Fatal("missing listless output", word)
						}
						var value string
						err = rows.Scan(&value)
						more := rows.Next()
						terminal := rows.Err()
						rows.Close()
						if err != nil || terminal != nil || more || value == "{}" || len(value) < 3 {
							t.Fatal("listless token dropped", word, value, err, terminal)
						}
					}
				}
				dictionaryResult(t, p, b, c, q, "{машины}", "russian_simple", "машины")
				dictionaryResult(t, p, b, c, q, "{всё}", "russian_simple", "всё")
			})
		})
	}
	portugueseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range [][2]string{{"И", "{И}"}, {"В", "{В}"}, {"НЕ", "{НЕ}"}, {"всё", "{все}"}, {"её", "{е}"}, {"ещё", "{ещ}"}} {
			portugueseSmokeBoth(t, p, b, c, fixture.Select("SELECT ts_lexize('russian_stem',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc[1]}}, tc[0]))
		}
	})
}
