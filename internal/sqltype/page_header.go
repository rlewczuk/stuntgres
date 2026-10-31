package sqltype

import (
 "fmt"
 "stuntgres/internal/sqlstate"
)

// PageHeader decodes the PostgreSQL 18 amd64 PageHeaderData layout. It owns
// only numbers: neither the result nor checksum evaluation retains raw bytes.
// Raw pageinspect routines check size, not page validity or offset ordering.
type PageHeader struct {
 LSN uint64
 Checksum, Flags int16
 Lower, Upper, Special uint16
 PageSize uint16
 Version uint8
 PruneXID uint32
}

func rawPageSize(raw string) error {
 if len(raw)!=8192 {return &sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:"invalid page size",Detail:fmt.Sprintf("Expected 8192 bytes, got %d.",len(raw))}}
 return nil
}

func ReadPageHeader(raw string) (PageHeader,error) {
 if err:=rawPageSize(raw);err!=nil {return PageHeader{},err}
 packed:=native16(raw[18:])
 return PageHeader{
  // PageXLogRecPtr is two native uint32 words, high word first, not native64.
  LSN:uint64(native32(raw))<<32|uint64(native32(raw[4:])),
  Checksum:int16(native16(raw[8:])),Flags:int16(native16(raw[10:])),
  Lower:native16(raw[12:]),Upper:native16(raw[14:]),Special:native16(raw[16:]),
  PageSize:packed&0xff00,Version:uint8(packed),PruneXID:native32(raw[20:]),
 },nil
}

// ReadPageChecksum returns the signed SQL smallint and whether PageIsNew
// suppressed calculation. Block validation precedes size and PageIsNew checks.
func ReadPageChecksum(raw string,block int64) (int16,bool,error) {
 if block<0 || block>4294967294 {return 0,false,&sqlstate.Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22023",Message:"invalid block number"}}
 if err:=rawPageSize(raw);err!=nil {return 0,false,err}
 if native16(raw[14:])==0 {return 0,true,nil}
 // PostgreSQL REL_18_6 src/include/storage/checksum_impl.h,
 // checksumBaseOffsets and CHECKSUM_COMP (PostgreSQL Global Development Group).
 // Fixed lanes are call-local; the immutable input is never zeroed or copied.
 sums:=[32]uint32{
  0x5B1F36E9,0xB8525960,0x02AB50AA,0x1DE66D2A,
  0x79FF467A,0x9BB9F8A3,0x217E7CD2,0x83E13D2C,
  0xF8D4474F,0xE39EB970,0x42C6AE16,0x993216FA,
  0x7B093B5D,0x98DAFF3C,0xF718902A,0x0B1C9CDB,
  0xE58F764B,0x187636BC,0x5D7B3BB1,0xE73DE7DE,
  0x92BEC979,0xCCA6C0B2,0x304A0979,0x85AA43D4,
  0x783125BB,0x6CA8EAA2,0xE407EAC6,0x4B5CFC3E,
  0x9FBF8C76,0x15CA20BE,0xF2CA9FD3,0x959BD756,
 }
 for group:=0;group<8192;group+=128 {
  for lane:=range sums {
   at:=group+lane*4;word:=native32(raw[at:])
   if at==8 {word&=0xffff0000} // Ignore stored checksum, but retain flags.
   t:=sums[lane]^word;sums[lane]=t*16777619^(t>>17)
  }
 }
 for range 2 {for lane,t:=range sums {sums[lane]=t*16777619^(t>>17)}}
 folded:=uint32(block);for _,sum:=range sums {folded^=sum}
 return int16(uint16(folded%65535+1)),false,nil
}
