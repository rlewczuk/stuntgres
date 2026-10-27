package sqltype

import (
 "strconv"
 "strings"
 "stuntgres/internal/sqlstate"
)

// ReadFSMPageContents borrows immutable raw bytes and returns owned node text.
// PostgreSQL checks only size and pd_upper; the remaining header is ignored.
func ReadFSMPageContents(raw string) (contents string, isNew bool, err error) {
 if len(raw)!=8192 {
  return "",false,&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:"invalid page size",Detail:"Expected 8192 bytes, got "+strconv.Itoa(len(raw))+"."}
 }
 if native16(raw[14:])==0{return "",true,nil}
 var out strings.Builder
 out.Grow(128)
 var decimal [20]byte
 for i:=range 8164 {
  if value:=raw[28+i];value!=0 {
   out.Write(strconv.AppendInt(decimal[:0],int64(i),10));out.WriteString(": ")
   out.Write(strconv.AppendInt(decimal[:0],int64(value),10));out.WriteByte('\n')
  }
 }
 out.WriteString("fp_next_slot: ")
 out.Write(strconv.AppendInt(decimal[:0],int64(int32(native32(raw[24:]))),10));out.WriteByte('\n')
 return out.String(),false,nil
}
