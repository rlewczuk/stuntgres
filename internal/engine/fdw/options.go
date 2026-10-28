package fdw

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
)

func TransformOptions(old []catalog.ForeignOption, changes []parser.ForeignOption) ([]catalog.ForeignOption, error) {
	options := append([]catalog.ForeignOption{}, old...)
	for _, change := range changes {
		name := change.Name.Text
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
			options = append(options, catalog.ForeignOption{Name: name, Value: change.Value})
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
	for _, option := range options {
		if strings.Contains(option.Name, "=") {
			return nil, sqlError("22023", fmt.Sprintf(`invalid option name "%s": must not contain "="`, option.Name))
		}
	}
	return options, nil
}
