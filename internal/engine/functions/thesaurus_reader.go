package functions

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/sqlstate"
)

// ThesaurusRules is immutable parsed resource input, without a normalizer or
// executable authority. Parsing completes before later option/source errors.
type ThesaurusRules struct {
	resource string
	rules    []thesaurusRule
}

type thesaurusRule struct {
	sample     []string
	substitute []thesaurusSubstitute
}

type thesaurusSubstitute struct {
	text    string
	literal bool
}

// ReadThesaurusRules performs the complete resource read at the ordered DictFile
// acquisition point. It runs detached and returns no partially parsed result.
func ReadThesaurusRules(ctx context.Context, resource string) (*ThesaurusRules, error) {
	if err := CheckDictionaryResourceName(ctx, resource); err != nil {
		return nil, err
	}
	path := resource + ".ths"
	data, err := dictionaryResources.ReadFile(path)
	if err != nil {
		return nil, dictionaryError("F0000", fmt.Sprintf(`could not open thesaurus file "%s": No such file or directory`, path))
	}
	return parseThesaurusRules(ctx, resource, string(data))
}

func parseThesaurusRules(ctx context.Context, resource, data string) (*ThesaurusRules, error) {
	parsed := &ThesaurusRules{resource: resource}
	physical := 0
	for line := range strings.SplitAfterSeq(data, "\n") {
		physical++
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		text := strings.TrimSpace(line)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		failure := func(message string) error {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "F0000", Message: message, Context: fmt.Sprintf("line %d of configuration file \"%s.ths\": \"%s\"", physical, resource, line)}
		}
		left, right, found := strings.Cut(text, ":")
		lhs, rhs := strings.Fields(left), strings.Fields(right)
		if !found || len(lhs) == 0 || len(rhs) == 0 {
			return nil, failure("unexpected end of line")
		}
		rule := thesaurusRule{sample: lhs, substitute: make([]thesaurusSubstitute, len(rhs))}
		for i, word := range rhs {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if word == "*" {
				return nil, failure("unexpected end of line or lexeme")
			}
			rule.substitute[i] = thesaurusSubstitute{text: word}
			if strings.HasPrefix(word, "*") {
				rule.substitute[i] = thesaurusSubstitute{text: word[1:], literal: true}
			}
		}
		parsed.rules = append(parsed.rules, rule)
	}
	return parsed, ctx.Err()
}
