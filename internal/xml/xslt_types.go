package xml

// Expanded identity is URI plus Local; Prefix records the requested spelling.
type xsltName struct{ Prefix, Local, URI string }
type xsltNamespace struct{ Prefix, URI string }
type xsltResultAttribute struct {
	Name  xsltName
	Value string
}
type xsltResultKind uint8

const (
	xsltResultElement xsltResultKind = iota
	xsltResultText
	xsltResultComment
	xsltResultPI
)

type xsltResultNode struct {
	Kind            xsltResultKind
	Name            xsltName
	Value           string
	Attributes      []xsltResultAttribute
	Namespaces      []xsltNamespace
	Children        []*xsltResultNode
	DisableEscaping bool
}
type xsltResultTree struct{ Children []*xsltResultNode }
type xsltOutputProperties struct {
	Method                                                                                   xsltName
	MethodSet                                                                                bool
	Version, Encoding, Standalone, DoctypePublic, DoctypeSystem, MediaType                   string
	VersionSet, EncodingSet, StandaloneSet, DoctypePublicSet, DoctypeSystemSet, MediaTypeSet bool
	OmitDeclaration, Indent                                                                  bool
	OmitDeclarationSet, IndentSet                                                            bool
	CDATA                                                                                    map[string]bool
}

func (n xsltName) key() string { return n.URI + "\x00" + n.Local }

const xsltNamespaceURI = "http://www.w3.org/1999/XSL/Transform"
