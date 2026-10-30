package functions

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype/unicode16"
)

// Language providers are immutable implementation identities, not user labels.
// A later language adds a real kernel and one entry in this table.
type snowballLanguage struct {
	name string
	stem func(string) string
}

var snowballLanguages = [...]snowballLanguage{
	{name: "english", stem: englishStem},
	{name: "arabic", stem: arabicStem},
	{name: "armenian", stem: armenianStem},
	{name: "basque", stem: basqueStem},
	{name: "danish", stem: danishStem},
}

// DefaultSnowballDictionary shares the immutable compiled bootstrap provider.
var DefaultSnowballDictionary = sync.OnceValues(func() (*SnowballDictionary, error) {
	return CompileSnowballDictionary(context.Background(), "english", "english")
})

// DefaultArabicSnowballDictionary has no stock stopword resource in REL_18_6.
var DefaultArabicSnowballDictionary = sync.OnceValues(func() (*SnowballDictionary, error) {
	return CompileSnowballDictionary(context.Background(), "arabic", "")
})

// DefaultArmenianSnowballDictionary has no stock stopword resource in REL_18_6.
var DefaultArmenianSnowballDictionary = sync.OnceValues(func() (*SnowballDictionary, error) {
	return CompileSnowballDictionary(context.Background(), "armenian", "")
})

// DefaultBasqueSnowballDictionary has no stock stopword resource in REL_18_6.
var DefaultBasqueSnowballDictionary = sync.OnceValues(func() (*SnowballDictionary, error) {
	return CompileSnowballDictionary(context.Background(), "basque", "")
})

// DefaultDanishSnowballDictionary includes the released Danish stoplist.
var DefaultDanishSnowballDictionary = sync.OnceValues(func() (*SnowballDictionary, error) {
	return CompileSnowballDictionary(context.Background(), "danish", "danish")
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

func (d *SnowballDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	token, err := unicode16.ConvertCase(ctx, input, unicode16.Lower, unicode16.ASCII)
	if err != nil {
		return fts.LexicalResult{}, err
	}
	// The release guard uses original input bytes before lookup and stemming.
	if len(input) > 1000 {
		return fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{{Text: token}}}, ctx.Err()
	}
	_, stopped := slices.BinarySearch(d.words, token)
	if token == "" || stopped {
		return fts.LexicalResult{Recognized: true}, ctx.Err()
	}
	stem := d.provider.stem(token)
	if stem == "" {
		stem = token
	}
	return fts.LexicalResult{Recognized: true, Lexemes: []fts.Lexeme{{Text: stem}}}, ctx.Err()
}
