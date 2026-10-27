package xml

import (
 "strings"
)

// CONTENT's xml:space diagnostic construction spells each scalar ampersand as
// "&#38;". That injective construction is not the transient attribute value.
// Invert it only for this captured value, after the ordinary warning is formed.
func (p *parser) collectedAttribute(v *namespaceValue)string {
 value:=p.s[v.start:v.end];if v.transformed{value=v.uri.String()}
 if !v.construction||!v.deferAmp{return value}
 first:=-1
 for at:=range len(value){if at&4095==0{p.err=p.ctx.Err();if p.err!=nil{return ""}};if strings.HasPrefix(value[at:],"&#38;"){first=at;break}}
 if first<0{p.err=p.ctx.Err();return value}
 var b strings.Builder;p.namespaceCopy(&b,value[:first]);nextPoll:=first
 for at:=first;at<len(value)&&p.err==nil; {
  if at>=nextPoll{p.err=p.ctx.Err();if p.err!=nil{return ""};nextPoll=at+min(4096,len(value)-at)}
  if strings.HasPrefix(value[at:],"&#38;"){if !p.builderByte(&b,'&'){return ""};at+=5}else{if !p.builderByte(&b,value[at]){return ""};at++}
 }
 if p.err==nil{p.err=p.ctx.Err()};return b.String()
}
