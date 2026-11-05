package analyze

import "stuntgres/internal/parser"

// Column options are the one raw utility map. Each distinct name's first
// declaration fixes its order; the parser retains the final option list for
// that name. Duplicate column declarations remain in the ordinary table AST
// for CREATE's semantic diagnostics, not additional entries in this map.
func (j *StatementJumble) utilityColumnOptions(table *parser.CreateTable, options map[string][]parser.ForeignOption, anchor *int) {
	j.number(uint64(len(options)))
	if len(options) == 0 {
		return
	}
	if table == nil {
		panic("stuntgres: foreign column options lack declaration order")
	}
	seen := make(map[string]struct{}, len(options))
	for _, element := range table.Elements {
		if element.Column == nil {
			continue
		}
		name := element.Column.Name
		if selected, ok := options[name]; ok {
			if _, duplicate := seen[name]; duplicate {
				continue
			}
			seen[name] = struct{}{}
			j.text(name)
			utilitySlice(j, selected, anchor)
		}
	}
	if len(seen) != len(options) {
		panic("stuntgres: foreign column options lack matching declarations")
	}
}
