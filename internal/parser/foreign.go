package parser

// ForeignOption preserves SQL option ordering and each ALTER operation.
// An empty Action means ADD; DROP has no value.
type ForeignOption struct {
	Name   Identifier
	Value  string
	Action string
}

// ForeignSupportOption retains source order and the start of each support
// clause. A nil Function denotes NO HANDLER or NO VALIDATOR.
type ForeignSupportOption struct {
	Kind     string
	Function *ObjectName
	Pos      Position
}

// ForeignDDL retains SQL/MED utility inputs without conflating executable
// procedure identities, relation declarations, or local storage statements.
type ForeignDDL struct {
	Action, Kind             string
	Name                     ObjectName
	Names                    []ObjectName
	IfExists, IfNotExists    bool
	Mode                     DropMode
	Pos                      Position
	Support                  []ForeignSupportOption
	Options                  []ForeignOption
	Server                   Identifier
	Role                     GrantGrantee
	ServerType, Version      *string
	VersionSet               bool
	NewName                  Identifier
	Owner                    *RoleSpec
	Table                    *CreateTable
	ColumnOptions            map[string][]ForeignOption
	TableActions             []ForeignTableAction
	Inner                    Statement
	RemoteSchema             Identifier
	LocalSchema              Identifier
	ImportFilter             []Identifier
	ImportExcept             bool
}

func (*ForeignDDL) statement() {}

type ForeignTableAction struct {
	Ordinary           *AlterTableAction
	Column             Identifier
	Options            []ForeignOption
	ColumnOptions      []ForeignOption
	columnOptionsToken lexeme // Retained only to reject OPTIONS in ordinary ADD.
	Default            *AlterTableDefault
}

type foreignElements struct {
	elements []TableElement
	options  map[string][]ForeignOption
}

func foreignColumn(c *Column, options []ForeignOption) foreignElements {
	return foreignElements{elements: []TableElement{{Column: c, Pos: c.Pos}}, options: map[string][]ForeignOption{c.Name: options}}
}

func appendForeignElements(a, b foreignElements) foreignElements {
	a.elements = append(a.elements, b.elements...)
	if a.options == nil {
		a.options = make(map[string][]ForeignOption)
	}
	for name, options := range b.options {
		a.options[name] = options
	}
	return a
}
