package parser

// securityLabelName keeps the first token's keyword/quoted category until the
// grammar knows whether this is a bare identity or an explicit signature.
// Function names allow type/function keywords unqualified, while qualified
// names start with ColId. Bare routine identities also allow ColId.
type securityLabelName struct {
	name  ObjectName
	first lexeme
}

// securityLabelSignatureName admits the shared routine/aggregate opener before
// either argument grammar runs, preserving its error ahead of malformed suffixes.
func (l *lexer) securityLabelSignatureName(raw securityLabelName, opener lexeme) (ObjectName, bool) {
	if len(raw.name.Parts) == 1 && !raw.first.keyword.typeFunctionName() {
		// Both routine and aggregate signatures report an unqualified
		// ColumnName keyword's invalid callable syntax at its opener.
		l.syntax(opener)
		return ObjectName{}, false
	}
	return raw.name, true
}
