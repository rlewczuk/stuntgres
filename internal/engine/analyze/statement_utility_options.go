package analyze

import "stuntgres/internal/parser"

// Column options are the one raw utility map. Their original declaration
// order belongs to the retained table elements, not the map's iteration order.
func (j *StatementJumble) utilityColumnOptions(table *parser.CreateTable, options map[string][]parser.ForeignOption, anchor *int) {
	j.number(uint64(len(options)))
	if len(options) == 0 {
		return
	}
	if table == nil {
		panic("stuntgres: foreign column options lack declaration order")
	}
	count := 0
	for _, element := range table.Elements {
		if element.Column == nil {
			continue
		}
		name := element.Column.Name
		if selected, ok := options[name]; ok {
			j.text(name)
			utilitySlice(j, selected, anchor)
			count++
		}
	}
	if count != len(options) {
		panic("stuntgres: foreign column options lack matching declarations")
	}
}
