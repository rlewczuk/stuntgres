package control

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/typedef"
)

// An exact constraint names the whole predecessor. A prefix constraint also
// requires a following rune whose width is in [min,max]. This boundary guard is
// essential: identifier truncation can leave up to three spare UTF-8 bytes.
type arrayNameConstraint struct {
	text     string
	min, max uint8
}

func (c arrayNameConstraint) matches(name string) bool {
	if c.min == 0 {
		return name == c.text
	}
	if !strings.HasPrefix(name, c.text) || len(name) == len(c.text) {
		return false
	}
	_, width := utf8.DecodeRuneInString(name[len(c.text):])
	return width >= int(c.min) && width <= int(c.max)
}

type arrayNameWitness struct {
	name     string
	suffixes []uint64
	ok       bool
}

// arrayNameTrace solves the finite reverse graph, not the unbounded set of
// naming histories. Every edge removes observed text; suffixes are either
// observed exact decimals or the least completion of a finite decimal prefix.
// Candidate construction itself belongs exclusively to the SQL type owner.
func arrayNameTrace(parent, target string) ([]uint64, error) {
	memo := map[arrayNameConstraint]arrayNameWitness{}
	capacity := map[uint64]int{}
	limit := func(suffix uint64) int {
		if n, ok := capacity[suffix]; ok {
			return n
		}
		n := strings.Count(typedef.AssociatedArrayNameCandidate("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", suffix), "x")
		capacity[suffix] = n
		return n
	}
	var solve func(arrayNameConstraint) arrayNameWitness
	solve = func(goal arrayNameConstraint) arrayNameWitness {
		if goal.matches(parent) {
			return arrayNameWitness{name: parent, ok: true}
		}
		if previous, ok := memo[goal]; ok {
			return previous
		}
		memo[goal] = arrayNameWitness{}
		accept := func(predecessor arrayNameConstraint, suffix uint64) arrayNameWitness {
			before := solve(predecessor)
			if !before.ok {
				return arrayNameWitness{}
			}
			name := typedef.AssociatedArrayNameCandidate(before.name, suffix)
			if !goal.matches(name) {
				return arrayNameWitness{}
			}
			path := make([]uint64, len(before.suffixes)+1)
			copy(path, before.suffixes)
			path[len(before.suffixes)] = suffix
			return arrayNameWitness{name: name, suffixes: path, ok: true}
		}
		truncated := func(prefix string, suffix uint64) arrayNameWitness {
			remaining := limit(suffix) - len(prefix)
			if remaining < 0 {
				return arrayNameWitness{}
			}
			if result := accept(arrayNameConstraint{text: prefix}, suffix); result.ok {
				return result
			}
			if remaining < utf8.UTFMax {
				return accept(arrayNameConstraint{text: prefix, min: uint8(remaining + 1), max: utf8.UTFMax}, suffix)
			}
			return arrayNameWitness{}
		}
		finish := func(result arrayNameWitness) arrayNameWitness { memo[goal] = result; return result }
		if goal.min == 0 {
			if !strings.HasPrefix(goal.text, "_") {
				return finish(arrayNameWitness{})
			}
			body := goal.text[1:]
			if result := truncated(body, 0); result.ok {
				return finish(result)
			}
			if split := strings.LastIndexByte(body, '_'); split >= 0 {
				digits := body[split+1:]
				suffix, err := strconv.ParseUint(digits, 10, 64)
				if err == nil && suffix != 0 && strconv.FormatUint(suffix, 10) == digits {
					if result := truncated(body[:split], suffix); result.ok {
						return finish(result)
					}
				}
			}
			return finish(arrayNameWitness{})
		}
		if goal.text == "" {
			if goal.min <= 1 && goal.max >= 1 {
				name := typedef.AssociatedArrayNameCandidate(parent, 0)
				return finish(arrayNameWitness{name: name, suffixes: []uint64{0}, ok: true})
			}
			return finish(arrayNameWitness{})
		}
		if !strings.HasPrefix(goal.text, "_") {
			return finish(arrayNameWitness{})
		}
		body := goal.text[1:]
		// The next observed rune still lies inside the retained predecessor. The
		// unsuffixed rule has the greatest capacity; one-digit suffixes cover the
		// suffixed rule without enumerating arbitrary suffix values.
		for _, suffix := range []uint64{0, 1} {
			maximum := min(int(goal.max), limit(suffix)-len(body))
			if maximum >= int(goal.min) {
				if result := accept(arrayNameConstraint{text: body, min: goal.min, max: uint8(maximum)}, suffix); result.ok {
					return finish(result)
				}
			}
		}
		// Otherwise the observed prefix crosses into a numeric suffix. Enumerate
		// only rune-boundary splits and decimal widths, retaining the least legal
		// completion. Its next byte is ASCII, so wider boundary guards cannot match.
		if goal.min <= 1 && goal.max >= 1 {
			for split := 0; split <= len(body); {
				prefix, tail := body[:split], body[split:]
				digits := ""
				valid := tail == ""
				if strings.HasPrefix(tail, "_") {
					digits = tail[1:]
					valid = true
					for _, digit := range digits {
						if digit < '0' || digit > '9' {
							valid = false
							break
						}
					}
					if len(digits) > 0 && digits[0] == '0' {
						valid = false
					}
				}
				if valid {
					for width := 1; width <= 20; width++ {
						if tail != "" && width <= len(digits) {
							continue
						}
						completion := digits
						if completion == "" {
							completion = "1"
						}
						if len(completion) > width {
							continue
						}
						completion += strings.Repeat("0", width-len(completion))
						suffix, err := strconv.ParseUint(completion, 10, 64)
						if err != nil || suffix == 0 {
							continue
						}
						if result := truncated(prefix, suffix); result.ok {
							return finish(result)
						}
					}
				}
				if split == len(body) {
					break
				}
				_, width := utf8.DecodeRuneInString(body[split:])
				split += width
			}
		}
		return finish(arrayNameWitness{})
	}
	result := solve(arrayNameConstraint{text: target})
	if !result.ok {
		return nil, fmt.Errorf("stuntgres: associated array identity %q is not generated from %q", target, parent)
	}
	return result.suffixes, nil
}
