// Package replication owns pure replication argument validation, not slot state.
package replication

import (
 "fmt"
 "strings"
 "stuntgres/internal/sqlstate"
)
func failure(code,message string) *sqlstate.Error { return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:code,Message:message} }
func ValidateSlotName(name string) error {
 if len(name)==0 { return failure("42602",`replication slot name "" is too short`) }
 if len(name)>=64 { return failure("42622",fmt.Sprintf(`replication slot name "%s" is too long`,name)) }
 for i:=range len(name) { c:=name[i]; if c>='a' && c<='z' || c>='0' && c<='9' || c=='_' { continue }; e:=failure("42602",fmt.Sprintf(`replication slot name "%s" contains invalid character`,name)); e.Hint="Replication slot names may only contain lower case letters, numbers, and the underscore character."; return e }
 return nil
}
// PluginAllowed scans the entire SplitGUCList spelling. An early matching token
// cannot authorize a malformed suffix; names are never folded or clipped.
// Only an element's initial quote starts quoted grammar.
func PluginAllowed(input,target string) bool {
 const space=" \t\r\n\f\v"
 s:=strings.Trim(input,space); matched:=false
 if s=="" { return false }
 for {
  consumed:=0; equal:=true; at:=0
  compare:=func(c byte) { if at>=len(target) || target[at]!=c { equal=false }; at++ }
  if s[0]=='"' {
   consumed=1; closed:=false
   for consumed<len(s) { c:=s[consumed]; consumed++; if c=='"' { if consumed<len(s) && s[consumed]=='"' { consumed++; compare('"'); continue }; closed=true; break }; compare(c) }
   if !closed { return false }
  } else {
   for consumed<len(s) && s[consumed]!=',' && !strings.ContainsRune(space,rune(s[consumed])) { compare(s[consumed]); consumed++ }
   if consumed==0 { return false }
  }
  matched=matched || equal && at==len(target)
  s=strings.TrimLeft(s[consumed:],space)
  if s=="" { return matched }
  if s[0]!=',' { return false }
  s=strings.TrimLeft(s[1:],space); if s=="" { return false }
 }
}
