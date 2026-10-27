package xml

import (
 "fmt"
 "unicode/utf8"
)

// Return canonical UTF8 width and decoded scalar separately. Attribute callers
// derive mode-specific construction width; namespace equality uses the scalar.
// Invalid references contribute neither.
func (p *parser) reference() (int,rune) {
 p.advance(1)
 if p.byte()=='#' {
  p.advance(1);base:=uint32(10);hex:=false
  if p.byte()=='x' {base=16;hex=true;p.advance(1)}
  if !p.fragmentGrow(){return 0,0}
  var value uint32
  count:=0
  for p.byte()!=';'&&p.poll() {
   // Preserve the release CharRef recovery counter, including its bounded
   // hexadecimal-letter path, without ever overflowing accumulated arithmetic.
   if count>20 {count=0;if !p.fragmentGrow(){return 0,0}}else{count++}
   b:=p.byte();v:=uint32(99)
   if b>='0'&&b<='9' {v=uint32(b-'0')}else if hex&&count<20&&b>='a'&&b<='f' {v=uint32(b-'a'+10)}else if hex&&count<20&&b>='A'&&b<='F' {v=uint32(b-'A'+10)}
   if v>=base {
    if hex {p.event("XML_ERR_INVALID_HEX_CHARREF","CharRef: invalid hexadecimal value")}else{p.event("XML_ERR_INVALID_DEC_CHARREF","CharRef: invalid decimal value")}
    value=0;break
   }
   if value>(0x110000-v)/base {value=0x110000}else{value=value*base+v}
   p.advance(1);count++
  }
  if p.byte()==';' {p.advance(1)}
  if value>=0x110000 {p.event("XML_ERR_INVALID_CHAR","xmlParseCharRef: character reference out of bounds");return 0,0}
  if !xmlChar(rune(value)) {p.event("XML_ERR_INVALID_CHAR",fmt.Sprintf("xmlParseCharRef: invalid xmlChar value %d",value));return 0,0}
  return utf8.RuneLen(rune(value)),rune(value)
 }
 name:=p.name()
 if name=="" {p.event("XML_ERR_NAME_REQUIRED","xmlParseEntityRef: no name");return 0,0}
 if p.byte()!=';' {p.event("XML_ERR_ENTITYREF_SEMICOL_MISSING","EntityRef: expecting ';'");return 0,0}
 p.advance(1)
 switch name {case "amp":return 1,'&';case "lt":return 1,'<';case "gt":return 1,'>';case "apos":return 1,'\'';case "quot":return 1,'"'}
 p.event("XML_ERR_UNDECLARED_ENTITY",p.joinMessage("Entity '",name,"' not defined"));return 0,0
}
