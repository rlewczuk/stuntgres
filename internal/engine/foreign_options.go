package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/parser"
)

func transformForeignOptions(old []ForeignOption, changes []parser.ForeignOption) ([]ForeignOption, error) {
	options := append([]ForeignOption{}, old...)
	for _, change := range changes {
		name := change.Name.Text
		if strings.Contains(name, "=") {
			return nil, sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, name))
		}
		index := -1
		for i := range options {
			if options[i].Name == name {
				index = i
				break
			}
		}
		switch change.Action {
		case "", "add":
			if index >= 0 {
				return nil, sqlError("42710", fmt.Sprintf(`option "%s" provided more than once`, name))
			}
			options = append(options, ForeignOption{Name: name, Value: change.Value})
		case "set":
			if index < 0 {
				return nil, sqlError("42704", fmt.Sprintf(`option "%s" not found`, name))
			}
			options[index].Value = change.Value
		case "drop":
			if index < 0 {
				return nil, sqlError("42704", fmt.Sprintf(`option "%s" not found`, name))
			}
			options = append(options[:index], options[index+1:]...)
		default:
			return nil, fmt.Errorf("stuntgres: invalid foreign option operation %q", change.Action)
		}
	}
	return options, nil
}
