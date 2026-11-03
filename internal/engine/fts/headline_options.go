package fts

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

type HeadlineOptions struct {
	MaxWords, MinWords, ShortWord, MaxFragments int
	HighlightAll                                bool
	StartSel, StopSel, FragmentDelimiter        string
}
type headlineParameter struct{ key, value string }

// ParseHeadlineOptions shares SQL's int4 input through integer, without making
// this catalog-free leaf depend on descriptors or copying the integer codec.
func ParseHeadlineOptions(ctx context.Context, text string, integer func(string) (int32, error)) (HeadlineOptions, error) {
	options := HeadlineOptions{MaxWords: 35, MinWords: 15, ShortWord: 3, StartSel: "<b>", StopSel: "</b>", FragmentDelimiter: " ... "}
	parameters, err := headlineParameters(ctx, text)
	if err != nil {
		return HeadlineOptions{}, err
	}
	for _, parameter := range parameters {
		if err := ctx.Err(); err != nil {
			return HeadlineOptions{}, err
		}
		key, value := parameter.key, parameter.value
		switch {
		case headlineOptionEqual(key, "maxwords"):
			n, err := integer(value)
			if err != nil {
				return HeadlineOptions{}, err
			}
			options.MaxWords = int(n)
		case headlineOptionEqual(key, "minwords"):
			n, err := integer(value)
			if err != nil {
				return HeadlineOptions{}, err
			}
			options.MinWords = int(n)
		case headlineOptionEqual(key, "shortword"):
			n, err := integer(value)
			if err != nil {
				return HeadlineOptions{}, err
			}
			options.ShortWord = int(n)
		case headlineOptionEqual(key, "maxfragments"):
			n, err := integer(value)
			if err != nil {
				return HeadlineOptions{}, err
			}
			options.MaxFragments = int(n)
		case headlineOptionEqual(key, "startsel"):
			options.StartSel = value
		case headlineOptionEqual(key, "stopsel"):
			options.StopSel = value
		case headlineOptionEqual(key, "fragmentdelimiter"):
			options.FragmentDelimiter = value
		case headlineOptionEqual(key, "highlightall"):
			options.HighlightAll = headlineOptionEqual(value, "1") ||
				headlineOptionEqual(value, "on") ||
				headlineOptionEqual(value, "true") ||
				headlineOptionEqual(value, "t") ||
				headlineOptionEqual(value, "y") ||
				headlineOptionEqual(value, "yes")
		default:
			return HeadlineOptions{}, failure("22023", fmt.Sprintf("unrecognized headline parameter: \"%s\"", key))
		}
	}
	if !options.HighlightAll {
		message := ""
		switch {
		case options.MinWords >= options.MaxWords:
			message = "MinWords must be less than MaxWords"
		case options.MinWords <= 0:
			message = "MinWords must be positive"
		case options.ShortWord < 0:
			message = "ShortWord must be >= 0"
		case options.MaxFragments < 0:
			message = "MaxFragments must be >= 0"
		}
		if message != "" {
			return HeadlineOptions{}, failure("22023", message)
		}
	}
	return options, ctx.Err()
}

// headlineOptionEqual compares against lowercase ASCII literals using the
// released UTF8/C byte folding, without copying or Unicode normalization.
func headlineOptionEqual(input, lower string) bool {
	if len(input) != len(lower) {
		return false
	}
	for i := range len(input) {
		c := input[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != lower[i] {
			return false
		}
	}
	return true
}

func headlineParameters(ctx context.Context, text string) ([]headlineParameter, error) {
	bad := func() ([]headlineParameter, error) {
		return nil, failure("42601", fmt.Sprintf("invalid parameter list format: \"%s\"", text))
	}
	var parameters []headlineParameter
	at := 0
	quoted := func(quote byte) (string, bool, error) {
		at++
		start, segment := at, at
		var decoded strings.Builder
		for at < len(text) {
			if err := ctx.Err(); err != nil {
				return "", false, err
			}
			c := text[at]
			if c == quote {
				if at+1 < len(text) && text[at+1] == quote {
					decoded.WriteString(text[segment : at+1])
					at += 2
					segment = at
					continue
				}
				value := text[start:at]
				if segment != start {
					decoded.WriteString(text[segment:at])
					value = decoded.String()
				}
				at++
				return value, true, nil
			}
			if quote == '\'' && c == '\\' && at+1 < len(text) && text[at+1] == '\\' {
				decoded.WriteString(text[segment : at+1])
				at += 2
				segment = at
				continue
			}
			at++
		}
		return "", false, nil
	}
	for at < len(text) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if space(text[at]) || text[at] == ',' {
			at++
			continue
		}
		var key string
		if text[at] == '"' {
			var ok bool
			var err error
			key, ok, err = quoted('"')
			if err != nil {
				return nil, err
			}
			if !ok {
				return bad()
			}
		} else {
			start := at
			at++
			for at < len(text) && !space(text[at]) && text[at] != '=' {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				at++
			}
			key = text[start:at]
		}
		for at < len(text) && space(text[at]) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			at++
		}
		if at == len(text) || text[at] != '=' {
			return bad()
		}
		at++
		for at < len(text) && space(text[at]) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			at++
		}
		if at == len(text) {
			return bad()
		}
		if text[at] == 'E' && at+1 < len(text) && text[at+1] == '\'' {
			at++
		}
		var value string
		if text[at] == '\'' || text[at] == '"' {
			var ok bool
			var err error
			value, ok, err = quoted(text[at])
			if err != nil {
				return nil, err
			}
			if !ok {
				return bad()
			}
		} else {
			start := at
			at++
			for at < len(text) && !space(text[at]) && text[at] != ',' {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				at++
			}
			value = text[start:at]
			// buildDefItem canonicalizes only successful signed int32 decimal
			// nodes; float/string spellings retain their original text.
			if n, err := strconv.ParseInt(value, 10, 32); err == nil {
				value = strconv.FormatInt(n, 10)
			}
		}
		parameters = append(parameters, headlineParameter{key, value})
	}
	return parameters, ctx.Err()
}
