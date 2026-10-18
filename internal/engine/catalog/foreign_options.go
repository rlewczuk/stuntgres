package catalog

// ForeignOption is an ordered, uninterpreted SQL/MED name/value pair.
// Table, column, server, wrapper and mapping lists are independent.
type ForeignOption struct{ Name, Value string }
