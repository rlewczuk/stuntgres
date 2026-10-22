package session

// ForeignGrantKind reports whether a GRANT object kind is a wrapper or server.
func ForeignGrantKind(kind string) bool {
	return kind == "foreign server" || kind == "foreign-data wrapper"
}
