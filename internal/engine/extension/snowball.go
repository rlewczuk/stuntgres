package extension

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
)

// CREATE processes options in source order, including provider/resource errors.
// ALTER has already applied PostgreSQL's remove-then-append merge.
func compileSnowballOptions(ctx context.Context, options []catalog.DictionaryOption) (catalog.DictionaryRuntime, error) {
	language, stopwords := "", ""
	seenLanguage, seenStopwords := false, false
	for _, option := range options {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch option.Name {
		case "language":
			if seenLanguage {
				return nil, dictionaryError("22023", "multiple Language parameters")
			}
			seenLanguage = true
			if !option.Supplied {
				return nil, dictionaryError("42601", "language requires a parameter")
			}
			language = option.Value
			if err := functions.CheckSnowballLanguage(language); err != nil {
				return nil, err
			}
		case "stopwords":
			if seenStopwords {
				return nil, dictionaryError("22023", "multiple StopWords parameters")
			}
			seenStopwords = true
			if !option.Supplied {
				return nil, dictionaryError("42601", "stopwords requires a parameter")
			}
			stopwords = option.Value
			// Loading here preserves the first error before the final Language check.
			if err := functions.CheckDictionaryStopwords(ctx, stopwords); err != nil {
				return nil, err
			}
		default:
			return nil, dictionaryError("22023", fmt.Sprintf(`unrecognized Snowball parameter: "%s"`, option.Name))
		}
	}
	if !seenLanguage {
		return nil, dictionaryError("22023", "missing Language parameter")
	}
	return functions.CompileSnowballDictionary(ctx, language, stopwords)
}
