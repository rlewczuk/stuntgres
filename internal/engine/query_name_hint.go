package engine

import (
	"fmt"
	"unicode/utf8"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
)

// boundedNameDistance compares characters, not UTF8 bytes. The caller decodes
// the requested name once and reuses two rows across every candidate. Rows
// must have room for the requested character count plus one.
// Only the limit-wide diagonal band can contribute an accepted distance.
func boundedNameDistance(name string, key, left, right []int, limit int) int {
	width := utf8.RuneCountInString(name)
	if width > len(key)+limit || len(key) > width+limit {
		return limit + 1
	}
	for j := 0; j <= len(key); j++ {
		left[j] = min(j, limit+1)
	}
	i := 0
	for _, candidate := range name {
		i++
		right[0] = min(i, limit+1)
		minimum := right[0]
		start, end := max(1, i-limit), min(len(key), i+limit)
		if start > 1 {
			right[start-1] = limit + 1
		}
		for j := start; j <= end; j++ {
			cost := 0
			if key[j-1] != int(candidate) {
				cost = 1
			}
			right[j] = min(left[j]+1, right[j-1]+1, left[j-1]+cost)
			minimum = min(minimum, right[j])
		}
		if end < len(key) {
			right[end+1] = limit + 1
		}
		if minimum > limit {
			return limit + 1
		}
		left, right = right, left
	}
	return min(left[len(key)], limit+1)
}

// queryColumnDiagnostic follows PostgreSQL18's missing-column RTE diagnostics.
// Within-entry exact ambiguity is checked regardless of qualifier penalty;
// otherwise inaccessible exact entries precede fuzzy spelling suggestions.
// Whole-join and USING alias entries are explicitly excluded, independently of identity.
func queryColumnDiagnostic(entries []queryQualifier, qualifier, name string, pos parser.Position, nonLateral bool) (detail, hint string, err error) {
	longest := 0
	var exactRelation string
	exactLateralForbidden := false
	exactCount := 0
	for _, q := range entries {
		if q.excludeColumnDiagnostics {
			continue
		}
		longest = max(longest, len(q.name))
		exactAttributes := 0
		for _, c := range q.columns {
			longest = max(longest, len(c.descriptor.Name))
			if c.descriptor.Name == name {
				exactAttributes++
				if exactAttributes > 1 {
					return "", "", expr.ErrorAt("42702", fmt.Sprintf(`column reference "%s" is ambiguous`, name), pos)
				}
			}
		}
		if exactAttributes == 1 && (qualifier == "" || qualifier == q.name) {
			if exactCount == 0 {
				exactRelation = q.name
				exactLateralForbidden = q.lateralForbidden
			}
			exactCount = min(2, exactCount+1)
		}
	}
	// Plural state cannot end the scan: a later entry may still be ambiguous.
	if exactCount == 2 {
		detail = fmt.Sprintf(`There are columns named "%s", but they are in tables that cannot be referenced from this part of the query.`, name)
		if qualifier == "" {
			hint = "Try using a table-qualified name."
		}
		return detail, hint, nil
	}
	if exactCount == 1 {
		if nonLateral && !exactLateralForbidden {
			hint = "To reference that column, you must mark this subquery with LATERAL."
		}
		return fmt.Sprintf(`There is a column named "%s" in table "%s", but it cannot be referenced from this part of the query.`, name, exactRelation), hint, nil
	}
	if longest == 0 {
		return "", "", nil
	}
	characters, qualifierCharacters := utf8.RuneCountInString(name), utf8.RuneCountInString(qualifier)
	if characters > longest+3 || qualifierCharacters > longest+3 {
		return "", "", nil
	}
	width := max(characters, qualifierCharacters) + 1
	scratch := make([]int, characters+qualifierCharacters+2*width)
	key, qualifierKey := scratch[:characters], scratch[characters:characters+qualifierCharacters]
	i := 0
	for _, r := range name {
		key[i] = int(r)
		i++
	}
	i = 0
	for _, r := range qualifier {
		qualifierKey[i] = int(r)
		i++
	}
	left := scratch[characters+qualifierCharacters : characters+qualifierCharacters+width]
	right := scratch[characters+qualifierCharacters+width:]
	type candidate struct{ relation, column string }
	var candidates [2]candidate
	best, ties := 4, 0
	for _, q := range entries {
		if q.excludeColumnDiagnostics {
			continue
		}
		penalty := 0
		if qualifier != "" {
			penalty = boundedNameDistance(q.name, qualifierKey, left, right, min(3, best))
		}
		if penalty > min(3, best) {
			continue
		}
		for _, c := range q.columns {
			// PostgreSQL uses strlen(request)/2 for this cutoff, although the
			// edit distance itself operates on characters.
			limit := min(3, best) - penalty
			limit = min(limit, len(name)/2)
			distance := boundedNameDistance(c.descriptor.Name, key, left, right, limit)
			if distance > limit {
				continue
			}
			distance += penalty
			if distance < best {
				best, ties = distance, 0
			}
			if ties < len(candidates) {
				candidates[ties] = candidate{q.name, c.descriptor.Name}
			}
			ties++
		}
	}
	switch ties {
	case 1:
		return "", fmt.Sprintf(`Perhaps you meant to reference the column "%s.%s".`, candidates[0].relation, candidates[0].column), nil
	case 2:
		return "", fmt.Sprintf(`Perhaps you meant to reference the column "%s.%s" or the column "%s.%s".`, candidates[0].relation, candidates[0].column, candidates[1].relation, candidates[1].column), nil
	default:
		return "", "", nil
	}
}
