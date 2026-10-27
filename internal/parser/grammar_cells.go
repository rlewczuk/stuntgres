package parser

// cell boxes a grammar value for a pointer-typed %union member. The large
// members are pointers so that every shift and reduce copies a small value
// cell; actions never share one boxed value between two AST positions.
func cell[T any](v T) *T { return &v }
