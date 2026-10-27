package sqltype

import (
 "fmt"
 "math"
 "strings"
)

// NativeDatum reads the released PostgreSQL 18 amd64 physical datum layout.
// Offsets are tuple-relative: varlena's nonzero first byte bypasses alignment.
// It does not apply input typmods or constraints to already stored values.
func NativeDatum(raw string, at int, usage Usage) (Value, int, error) {
 if usage.Type == nil || at < 0 || at >= len(raw) { return Value{}, at, nativeLayoutError() }
 d := usage.Type
 facts := d.AttributeFacts()
 alignment := 4
 switch facts.Alignment { case "c": alignment=1; case "s": alignment=2; case "d": alignment=8 }
 if facts.Length != -1 || raw[at] == 0 { at = (at+alignment-1)& -alignment }
 if at > len(raw) { return Value{}, at, nativeLayoutError() }
 end := at+int(facts.Length)
 var payload string
 if facts.Length == -1 {
  var err error
  payload, end, err = nativeVarlena(raw, at)
  if err != nil { return Value{}, at, err }
 } else {
  if end < at || end > len(raw) { return Value{}, at, nativeLayoutError() }
  payload=raw[at:end]
 }
 v, err := nativeDatumPayload(d.BaseType(), payload)
 if err != nil { return Value{}, at, err }
 v.typ=d
 return v, end, nil
}

func nativeLayoutError() error { return fmt.Errorf("stuntgres: unsafe PostgreSQL native datum layout") }
func native16(s string) uint16 { return uint16(s[0]) | uint16(s[1])<<8 }
func native32(s string) uint32 { return uint32(native16(s)) | uint32(native16(s[2:]))<<16 }
func native64(s string) uint64 { return uint64(native32(s)) | uint64(native32(s[4:]))<<32 }

func nativeDatumPayload(d *Descriptor, p string) (Value, error) {
 v := Value{typ:d}
 switch d.id {
 case Char: if p[0]!=0 {v.text=p}
 case Name: v.text=p; if n:=strings.IndexByte(p,0); n>=0 { v.text=p[:n] }
 case Text, VarChar, BPChar, Bytea: v.text=p
 case Int2: v.integer=int64(int16(native16(p)))
 case Int4: v.integer=int64(int32(native32(p)))
 case Oid: v.oid=native32(p)
 case Int8, Time, Timestamp, Timestamptz: v.integer=int64(native64(p))
 case Date:
  n:=int32(native32(p)); v.integer=int64(n)
  if n==math.MaxInt32 { v.integer=DatePositiveInfinity }; if n==math.MinInt32 { v.integer=DateNegativeInfinity }
 case Timetz: v.integer=int64(native64(p)); v.west=int32(native32(p[8:]))
 case Interval: v.integer=int64(native64(p)); v.days=int32(native32(p[8:])); v.months=int32(native32(p[12:]))
 case Float4: v.float4Raw=native32(p); v.float4RawValid=true; v.floating=float64(math.Float32frombits(v.float4Raw))
 case Float8: v.floating=math.Float64frombits(native64(p))
 case Tid: v.integer=int64(uint64(uint32(native16(p))<<16|uint32(native16(p[2:])))<<16|uint64(native16(p[4:])))
 case UUID: copy(v.uuid[:],p)
 case Macaddr, Macaddr8:
  for i := range len(p) { v.integer = int64(uint64(v.integer)<<8 | uint64(p[i])) }
 case Bit, VarBit:
  if len(p)<4 { return Value{},nativeLayoutError() }; n:=int(int32(native32(p)))
  if n<0 || (n+7)/8>len(p)-4 { return Value{},nativeLayoutError() }
  var b strings.Builder; b.Grow(n)
  for i := range n { b.WriteByte('0'+(p[4+i/8]>>uint(7-i%8)&1)) }; v.text=b.String()
 case Inet, Cidr:
  if len(p)<2 { return Value{},nativeLayoutError() }
  size,family:=4,uint8(4); if p[0]==3 { size,family=16,6 } else if p[0]!=2 { return Value{},nativeLayoutError() }
  if len(p)<2+size { return Value{},nativeLayoutError() }; v.network.family=family; v.network.prefix=p[1]; copy(v.network.address[:],p[2:2+size])
 case Numeric: return nativeNumeric(d,p)
 default: return Value{},fmt.Errorf("stuntgres: missing native datum reader for %s",d.Name())
 }
 return v,nil
}

// NumericShort and NumericLong follow numeric.c, not numeric_recv's wire header.
func nativeNumeric(d *Descriptor,p string) (Value,error) {
 if len(p)<2 { return Value{},nativeLayoutError() }
 h:=native16(p); v:=Value{typ:d}; offset,weight:=2,int64(0)
 switch h&0xc000 {
 case 0xc000:
  switch h&0xf000 { case 0xc000:v.special=numericNaN; case 0xd000:v.special=numericInfinity; case 0xf000:v.special=numericNegativeInfinity; default:return Value{},nativeLayoutError() }; return v,nil
 case 0x8000:
  v.negative=h&0x2000!=0; v.scale=int64((h&0x1f80)>>7); weight=int64(h&0x3f); if h&0x40!=0 { weight-=64 }
 default:
  if len(p)<4 { return Value{},nativeLayoutError() }; offset=4; weight=int64(int16(native16(p[2:]))); v.scale=int64(h&0x3fff); v.negative=h&0x4000!=0
 }
 if (len(p)-offset)%2!=0 { return Value{},nativeLayoutError() }
 count:=(len(p)-offset)/2; var b strings.Builder; b.Grow(count*4)
 for i := range count { n:=native16(p[offset+i*2:]); if n>=10000 { return Value{},nativeLayoutError() }; var g [4]byte; for j:=3;j>=0;j-- { g[j]='0'+byte(n%10); n/=10 }; b.Write(g[:]) }
 v.text=b.String(); v.exponent=(weight-int64(count)+1)*4
 return normalizeNumeric(v),nil
}

