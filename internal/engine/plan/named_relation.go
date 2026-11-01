package plan

// NamedRelation is an invocation-local relation shape. Neither this shape nor
// a retained scan carries data, catalog identity, or permission to read it.
type NamedRelation struct {
	Name   string
	Schema QuerySchema
}

// NamedRelationScanNode resolves its immutable shape against the current
// invocation at execution, with a private cursor over shared immutable rows.
type NamedRelationScanNode struct {
	QuerySchema
	Name          string
	RangeName     string
	ColumnAliases []string
	PresenceSlot  int
}
