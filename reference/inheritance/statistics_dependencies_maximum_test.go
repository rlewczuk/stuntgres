package reference_test

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsDependenciesMaximumWidth(t *testing.T) {
	// Independently form the complete set by determinant bit membership, not
	// the producer's recursive width/combination enumeration. Sorting expresses
	// the released order: total width, determinant tuple, then dependent.
	// The dropped second attribute makes every direction test sparse attnums.
	dimensions := []int{1, 3, 4, 5, 6, 7, 8, 9}
	var directions [][]int
	for dependent := range dimensions {
		for determinant := 1; determinant < 256; determinant++ {
			if determinant&(1<<dependent) != 0 {
				continue
			}
			var attributes []int
			for dimension, attribute := range dimensions {
				if determinant&(1<<dimension) != 0 {
					attributes = append(attributes, attribute)
				}
			}
			directions = append(directions, append(attributes, dimensions[dependent]))
		}
	}
	slices.SortFunc(directions, func(a, b []int) int {
		if len(a) != len(b) {
			return len(a) - len(b)
		}
		if order := slices.Compare(a[:len(a)-1], b[:len(b)-1]); order != 0 {
			return order
		}
		return a[len(a)-1] - b[len(b)-1]
	})
	if len(directions) != 1016 {
		t.Fatal("maximum directed dependency count", len(directions))
	}
	var widths [9]int
	var expected strings.Builder
	expected.WriteByte('{')
	for index, direction := range directions {
		widths[len(direction)]++
		if index != 0 {
			expected.WriteString(", ")
		}
		expected.WriteByte('"')
		for position, attribute := range direction {
			if position == len(direction)-1 {
				expected.WriteString(" => ")
			} else if position != 0 {
				expected.WriteString(", ")
			}
			expected.WriteString(strconv.Itoa(attribute))
		}
		expected.WriteString(`": 1.000000`)
	}
	expected.WriteByte('}')
	if widths != [9]int{0, 0, 56, 168, 280, 280, 168, 56, 8} {
		t.Fatal("independent directed width cardinalities", widths)
	}

	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE dep_max(a INT,hole INT,b INT,c INT,d INT,e INT,f INT,g INT,h INT)", 0))
		s.run(fixture.Exec("ALTER TABLE dep_max DROP COLUMN hole", 0))
		s.run(fixture.Exec("CREATE STATISTICS dep_max_s(dependencies) ON h,g,f,e,d,c,b,a FROM dep_max", 0))
		s.run(fixture.Exec("INSERT INTO dep_max VALUES(1,2,3,4,5,6,7,8),(11,12,13,14,15,16,17,18)", 2))
		s.run(fixture.Exec("ANALYZE dep_max", 0))
		// Every column is injective over these two rows: every possible
		// determinant determines every other dimension with degree exactly one.
		// Exact whole-datum comparison checks all 1016 identities and their order.
		s.run(dependenciesRows(s.pg, []any{"dep_max_s", false, expected.String()}))
		s.run(fixture.Select("SELECT a,b,c,d,e,f,g,h FROM dep_max ORDER BY a", []string{"a", "b", "c", "d", "e", "f", "g", "h"}, []string{"INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3), int64(4), int64(5), int64(6), int64(7), int64(8)}, {int64(11), int64(12), int64(13), int64(14), int64(15), int64(16), int64(17), int64(18)}}))
	})
}
