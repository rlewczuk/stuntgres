package sqltype

// nativeVarlena follows REL_18_6 varatt.h. External pointers cannot be formed
// by brin_form_tuple, which detoasts external values before storing them.
func nativeVarlena(raw string, at int) (string,int,error) {
 if at<0 || at>=len(raw) { return "",at,nativeLayoutError() }
 first:=raw[at]
 if first&1!=0 {
  n:=int(first>>1)
  if n<1 || n>len(raw)-at { return "",at,nativeLayoutError() }
  return raw[at+1:at+n],at+n,nil
 }
 if len(raw)-at<4 { return "",at,nativeLayoutError() }
 h:=native32(raw[at:]); n:=int(h>>2)
 if n<4 || n>len(raw)-at { return "",at,nativeLayoutError() }
 end:=at+n
 if h&3==0 { return raw[at+4:end],end,nil }
 if n<8 { return "",at,nativeLayoutError() }
 info:=native32(raw[at+4:]); size:=int(info&0x3fffffff); method:=info>>30
 input:=raw[at+8:end]
 // The format bounds the expansion: PGLZ references emit at most 273 bytes
 // per three input bytes; LZ4 extensions emit at most 255 per input byte.
 // Check before allocation, without imposing a datatype or page-size cap.
 if size>len(input)*273 { return "",at,nativeLayoutError() }
 var out []byte; var err error
 switch method { case 0:out,err=nativePGLZ(input,size); case 1:out,err=nativeLZ4(input,size); default:return "",at,nativeLayoutError() }
 if err!=nil { return "",at,err }; return string(out),end,nil
}

func nativePGLZ(input string,size int) ([]byte,error) {
 out:=make([]byte,0,size); at:=0
 for at<len(input) && len(out)<size {
  control:=input[at]; at++
  for bit:=uint(0);bit<8 && at<len(input) && len(out)<size;bit++ {
   if control&(1<<bit)==0 { out=append(out,input[at]);at++;continue }
   if len(input)-at<2 { return nil,nativeLayoutError() }
   n:=int(input[at]&15)+3; offset:=int(input[at]&0xf0)<<4|int(input[at+1]);at+=2
   if n==18 { if at==len(input) { return nil,nativeLayoutError() };n+=int(input[at]);at++ }
   if offset==0 || offset>len(out) || n>size-len(out) { return nil,nativeLayoutError() }
   for range n { out=append(out,out[len(out)-offset]) }
  }
 }
 if len(out)!=size || at!=len(input) { return nil,nativeLayoutError() };return out,nil
}

// LZ4's raw block format is the format used by toast_compress_datum; there is
// no frame header, checksum or dictionary. Match copies deliberately overlap.
func nativeLZ4(input string,size int) ([]byte,error) {
 out:=make([]byte,0,size);at:=0
 length:=func(n int)(int,error){
  if n==15 { for { if at>=len(input) { return 0,nativeLayoutError() };c:=int(input[at]);at++;n+=c;if c!=255 { break } } }
  return n,nil
 }
 for at<len(input) {
  token:=input[at];at++
  literals,err:=length(int(token>>4));if err!=nil { return nil,err }
  if literals>len(input)-at || literals>size-len(out) { return nil,nativeLayoutError() }
  out=append(out,input[at:at+literals]...);at+=literals
  if at==len(input) { break }
  if len(input)-at<2 { return nil,nativeLayoutError() }
  offset:=int(native16(input[at:]));at+=2
  n,err:=length(int(token&15));if err!=nil { return nil,err };n+=4
  if offset==0 || offset>len(out) || n>size-len(out) { return nil,nativeLayoutError() }
  for range n { out=append(out,out[len(out)-offset]) }
 }
 if len(out)!=size { return nil,nativeLayoutError() };return out,nil
}
