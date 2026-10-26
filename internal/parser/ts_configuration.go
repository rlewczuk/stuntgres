package parser

type ConfigurationOption struct {
	Name              Identifier
	Value             ObjectName
	Supplied, Numeric bool
}

func configurationOption(o DictionaryOption) ConfigurationOption {
	value := ObjectName{Parts: []Identifier{{Text: o.Value}}}
	if o.Source != nil {
		value = *o.Source
	}
	return ConfigurationOption{Name: o.Name, Value: value, Supplied: o.Supplied, Numeric: o.Numeric}
}

type CreateConfiguration struct {
	Name    ObjectName
	Options []ConfigurationOption
	Pos     Position
}
type ConfigurationMappingAction uint8

const (
	ConfigurationAddMapping ConfigurationMappingAction = iota + 1
	ConfigurationAlterMapping
	ConfigurationReplaceMapping
	ConfigurationDropMapping
)

type AlterConfiguration struct {
	Name          ObjectName
	Action        string
	MappingAction ConfigurationMappingAction
	Tokens        []Identifier
	Dictionaries  []ObjectName
	OldDictionary ObjectName
	IfExists      bool
	Target        Identifier
	OwnerIdentity bool
	Pos           Position
}
type DropConfiguration struct {
	Names    []ObjectName
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*CreateConfiguration) statement() {}
func (*AlterConfiguration) statement()  {}
func (*DropConfiguration) statement()   {}
