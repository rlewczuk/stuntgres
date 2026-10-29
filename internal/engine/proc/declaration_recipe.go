package proc

import (
 "strconv"
 "strings"
)

// DeclarationPartKind is the closed set of release-analyzed metadata slots.
// None is a bound expression, dependency, provider or execution capability.
type DeclarationPartKind uint8
const (
 DeclarationSyntax DeclarationPartKind = iota
 DeclarationFormal
 DeclarationRoutine
 DeclarationType
 DeclarationRelation
 DeclarationColumn
 DeclarationOperator
 DeclarationLiteral
 DeclarationAlias
 DeclarationExtensionMember
 DeclarationIdentifier
)

type DeclarationPart struct {
 Kind DeclarationPartKind
 Text string // Canonical spelling (syntax) or literal value.
 Name string
 Signature string
 OID uint32
 Namespace uint32
 Attribute int32
 Slot int
 Extension string
 Class uint32
 Arguments [3]uint32 // Zero is this installation's member-zero type identity.
 ArgumentCount int
}

type DeclarationRecipe struct {
 Return bool
 Parts []DeclarationPart
}

func DeclarationIdentifierName(name string) DeclarationPart {return DeclarationPart{Kind:DeclarationIdentifier,Name:name}}
func DeclarationText(text string) DeclarationPart { return DeclarationPart{Text:text} }
func DeclarationArgument(slot int) DeclarationPart { return DeclarationPart{Kind:DeclarationFormal, Slot:slot} }
func DeclarationFunction(name, signature string) DeclarationPart {
 return DeclarationPart{Kind:DeclarationRoutine, Name:name, Signature:signature, Namespace:11}
}
func DeclarationTypeName(id uint32, spelling string) DeclarationPart {
 return DeclarationPart{Kind:DeclarationType, OID:id, Text:spelling}
}
func DeclarationTable(id uint32, name string) DeclarationPart {
 return DeclarationPart{Kind:DeclarationRelation, OID:id, Name:name, Namespace:11}
}
func DeclarationAttribute(id uint32, relation string, attr int32, name string) DeclarationPart {
 return DeclarationPart{Kind:DeclarationColumn, OID:id, Text:relation, Name:name, Attribute:attr, Namespace:11}
}
func DeclarationOp(name string, left, right uint32) DeclarationPart {
 return DeclarationPart{Kind:DeclarationOperator, Name:name, Signature:strconv.FormatUint(uint64(left),10)+","+strconv.FormatUint(uint64(right),10), Namespace:11,Arguments:[3]uint32{left,right},ArgumentCount:2}
}
func DeclarationString(value string) DeclarationPart { return DeclarationPart{Kind:DeclarationLiteral, Text:value} }
func DeclarationCatalogAlias(class, id uint32, name string) DeclarationPart {
 return DeclarationPart{Kind:DeclarationAlias, Class:class, OID:id, Name:name, Namespace:11}
}
// DeclarationMember names a stable extension-owned member slot. The extension
// name selects the installation, not a schema spelling captured before a move.
func DeclarationMember(extension string, slot int, class uint32, spelling string, arguments ...uint32) DeclarationPart {
 if len(arguments)>3 {panic("stuntgres: invalid declaration member arguments")}
 part:=DeclarationPart{Kind:DeclarationExtensionMember, Extension:extension, Slot:slot, Class:class, Text:spelling,ArgumentCount:len(arguments)}
 copy(part.Arguments[:],arguments)
 return part
}

func (p DeclarationPart) source() string {
 switch p.Kind {
 case DeclarationFormal: return "$"+strconv.Itoa(p.Slot+1)
 case DeclarationRoutine, DeclarationRelation, DeclarationIdentifier: return p.Name
 case DeclarationColumn: return p.Text+"."+p.Name
 case DeclarationOperator: return p.Name
 case DeclarationLiteral: return declarationString(p.Text)
 case DeclarationAlias: return declarationString(p.Name)
 default: return p.Text
 }
}
func declarationString(value string) string {
 if strings.Contains(value, "\\") {
  return "E'"+strings.ReplaceAll(strings.ReplaceAll(value,"\\","\\\\"),"'","''")+"'"
 }
 return "'"+strings.ReplaceAll(value,"'","''")+"'"
}
func (r DeclarationRecipe) source() string {
 var out strings.Builder
 if r.Return { out.WriteString("SELECT ") }
 for _, part := range r.Parts { out.WriteString(part.source()) }
 return out.String()
}
