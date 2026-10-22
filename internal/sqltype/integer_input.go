package sqltype

// A bigint option consumes the same released integer spelling and diagnostics
// as type input, but does not expose a SQL value or descriptor identity.
var bigintInputDescriptor = Descriptor{id: Int8}

// ParseInt8 parses text using PostgreSQL bigint input, including base prefixes,
// digit separators, whitespace and exact syntax/overflow precedence.
func ParseInt8(text string) (int64, error) {
	value, err := bigintInputDescriptor.integerInput(text)
	if err != nil {
		return 0, err
	}
	n, _ := value.Integer()
	return n, nil
}
