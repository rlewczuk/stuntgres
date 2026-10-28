package functions

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/sqltype/unicode16"
)

// Language providers are immutable implementation identities, not user labels.
// A later language adds a real kernel and one entry in this table.
type snowballLanguage struct {
	name string
	stem func(string) string
}

var snowballLanguages = [...]snowballLanguage{{name: "english", stem: englishStem}}

// DefaultSnowballDictionary shares the immutable compiled bootstrap provider.
var DefaultSnowballDictionary = sync.OnceValues(func() (*SnowballDictionary, error) {
	return CompileSnowballDictionary(context.Background(), "english", "english")
})

func lookupSnowballLanguage(name string) *snowballLanguage {
	for i := range snowballLanguages {
		p := &snowballLanguages[i]
		if len(name) != len(p.name) {
			continue
		}
		same := true
		for j := range len(name) {
			c := name[j]
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			if c != p.name[j] {
				same = false
				break
			}
		}
		if same {
			return p
		}
	}
	return nil
}

// SnowballDictionary owns immutable configured spelling and registered authority.
type SnowballDictionary struct {
	provider            *snowballLanguage
	language, stopwords string
	words               []string
}

func CheckSnowballLanguage(language string) error {
	if lookupSnowballLanguage(language) == nil {
		return dictionaryError("42704", fmt.Sprintf(`no Snowball stemmer available for language "%s" and encoding "UTF8"`, language))
	}
	return nil
}

func CompileSnowballDictionary(ctx context.Context, language, stopwords string) (*SnowballDictionary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := CheckSnowballLanguage(language); err != nil {
		return nil, err
	}
	words, err := loadDictionaryStopwords(ctx, stopwords)
	if err != nil {
		return nil, err
	}
	return &SnowballDictionary{provider: lookupSnowballLanguage(language), language: language, stopwords: stopwords, words: words}, nil
}

func (d *SnowballDictionary) Configured(language, stopwords string) bool {
	return d != nil && d.provider != nil && d.provider == lookupSnowballLanguage(language) && d.language == language && d.stopwords == stopwords
}

func (*SnowballDictionary) Filtering() bool { return false }

func (d *SnowballDictionary) Lexize(ctx context.Context, input string, result *sqltype.Descriptor) (sqltype.Value, error) {
	token, err := unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return sqltype.Value{}, err
	}
	// PostgreSQL's implementation guard is on original input bytes, before
	// stopword lookup and stemming, not on UTF8 character count.
	if len(input) > 1000 {
		return lexemeArray(ctx, result, token, false)
	}
	_, stopped := slices.BinarySearch(d.words, token)
	if token == "" || stopped {
		return lexemeArray(ctx, result, "", true)
	}
	return lexemeArray(ctx, result, d.provider.stem(token), false)
}
