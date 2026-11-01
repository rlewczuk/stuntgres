package extension

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
)

func intDictionaryOptions(options []catalog.DictionaryOption) (int, bool, bool, error) {
	maxlen, rejectlong, absval := 6, false, false
	for _, option := range options {
		switch option.Name {
		case "maxlen":
			if !option.Supplied {
				return 0, false, false, dictionaryError("42601", "maxlen requires a parameter")
			}
			maxlen = intDictionaryLength(option.Value)
			if maxlen < 1 {
				return 0, false, false, dictionaryError("22023", "maxlen value has to be >= 1")
			}
		case "rejectlong", "absval":
			// defGetBoolean treats a bare CREATE option as true; the shared
			// serializer subsequently rejects its missing parameter. ALTER
			// has already removed bare settings through MergeDictionaryOptions.
			value := true
			if option.Supplied {
				if option.Numeric {
					n, err := strconv.ParseInt(option.Value, 10, 32)
					if err != nil || n != 0 && n != 1 {
						return 0, false, false, dictionaryError("42601", option.Name+" requires a Boolean value")
					}
					value = n == 1
				} else {
					switch {
					case strings.EqualFold(option.Value, "true"), strings.EqualFold(option.Value, "on"):
						value = true
					case strings.EqualFold(option.Value, "false"), strings.EqualFold(option.Value, "off"):
						value = false
					default:
						return 0, false, false, dictionaryError("42601", option.Name+" requires a Boolean value")
					}
				}
			}
			if option.Name == "rejectlong" {
				rejectlong = value
			} else {
				absval = value
			}
		default:
			return 0, false, false, dictionaryError("22023", fmt.Sprintf(`unrecognized intdict parameter: "%s"`, option.Name))
		}
	}
	return maxlen, rejectlong, absval, nil
}

// Released dict_int uses atoi, so only the decimal prefix is consumed. This
// intentionally does not define a portable contract for overflowing C integers.
func intDictionaryLength(value string) int {
	start := 0
	for start < len(value) {
		switch value[start] {
		case ' ', '\t', '\n', '\r', '\v', '\f':
			start++
			continue
		}
		break
	}
	end := start
	if end < len(value) && (value[end] == '+' || value[end] == '-') {
		end++
	}
	digits := end
	for end < len(value) && value[end] >= '0' && value[end] <= '9' {
		end++
	}
	if end == digits {
		return 0
	}
	n, err := strconv.ParseInt(value[start:end], 10, 32)
	if err != nil {
		return 0
	}
	return int(n)
}
