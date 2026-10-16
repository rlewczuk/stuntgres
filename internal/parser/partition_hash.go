package parser

import "strconv"

func (l *lexer) hashBoundOption(name, value lexeme) (PartitionBound, bool) {
	n, err := strconv.ParseInt(value.text, 10, 32)
	if err != nil {
		l.syntax(value)
		return PartitionBound{}, false
	}
	switch name.text {
	case "modulus":
		return PartitionBound{Modulus: int32(n), ModulusPos: name.pos}, true
	case "remainder":
		return PartitionBound{Remainder: int32(n), RemainderPos: name.pos}, true
	default:
		l.failAt("42601", `unrecognized hash partition bound specification "`+name.text+`"`, name.pos.Offset)
		return PartitionBound{}, false
	}
}
