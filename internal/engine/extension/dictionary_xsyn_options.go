package extension

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/functions"
)

type xsynSettings struct {
	rulesPresent                                     bool
	rules                                            string
	matchorig, matchsynonyms, keeporig, keepsynonyms bool
}

func xsynDictionaryOptions(options []catalog.DictionaryOption) (xsynSettings, error) {
	s := xsynSettings{matchorig: true, keeporig: true, keepsynonyms: true}
	for _, option := range options {
		switch option.Name {
		case "rules":
			if !option.Supplied {
				return s, dictionaryError("42601", "rules requires a parameter")
			}
			s.rulesPresent, s.rules = true, option.Value
		case "matchorig", "matchsynonyms", "keeporig", "keepsynonyms":
			// Bare CREATE initializes as true before shared serialization
			// rejects it. Bare ALTER has already removed the setting.
			value := true
			if option.Supplied {
				if option.Numeric {
					n, err := strconv.ParseInt(option.Value, 10, 32)
					if err != nil || n != 0 && n != 1 {
						return s, dictionaryError("42601", option.Name+" requires a Boolean value")
					}
					value = n == 1
				} else {
					switch {
					case strings.EqualFold(option.Value, "true"), strings.EqualFold(option.Value, "on"):
						value = true
					case strings.EqualFold(option.Value, "false"), strings.EqualFold(option.Value, "off"):
						value = false
					default:
						return s, dictionaryError("42601", option.Name+" requires a Boolean value")
					}
				}
			}
			switch option.Name {
			case "matchorig":
				s.matchorig = value
			case "matchsynonyms":
				s.matchsynonyms = value
			case "keeporig":
				s.keeporig = value
			case "keepsynonyms":
				s.keepsynonyms = value
			}
		default:
			return s, dictionaryError("22023", fmt.Sprintf(`unrecognized xsyn parameter: "%s"`, option.Name))
		}
	}
	return s, nil
}

func (s xsynSettings) compile(ctx context.Context) (*functions.XsynDictionary, error) {
	return functions.CompileXsynDictionary(ctx, s.rulesPresent, s.rules, s.matchorig, s.matchsynonyms, s.keeporig, s.keepsynonyms)
}

func (s xsynSettings) configured(d *functions.XsynDictionary) bool {
	return d.Configured(s.rulesPresent, s.rules, s.matchorig, s.matchsynonyms, s.keeporig, s.keepsynonyms)
}
