package xml

import (
 "math"
 "strings"
)

// strings.Builder's automatic growth copies the entire prior buffer without
// polling. Grow an empty builder instead, then copy the immutable prior prefix
// in bounded chunks. Existing strings remain immutable across Reset and Grow.
func (p *parser) ensureBuilder(b *strings.Builder,additional int)bool {
 if p.err!=nil{return false}
 if !appendSize(b.Len(),additional){p.err=appendSizeError(b.Len(),additional);return false}
 if additional<=b.Cap()-b.Len(){return true}
 needed:=b.Len()+additional;capacity:=needed
 if b.Cap()<=math.MaxInt/2{capacity=max(capacity,b.Cap()*2)}
 if !appendSize(0,capacity){capacity=needed}
 p.err=p.ctx.Err();if p.err!=nil{return false}
 prior:=b.String();b.Reset();b.Grow(capacity)
 p.err=p.ctx.Err();if p.err!=nil{return false}
 for at:=0;at<len(prior); {
  p.err=p.ctx.Err();if p.err!=nil{return false}
  end:=at+min(4096,len(prior)-at);b.WriteString(prior[at:end]);at=end
 }
 p.err=p.ctx.Err();return p.err==nil
}
func (p *parser) builderByte(b *strings.Builder,value byte)bool {
 if !p.ensureBuilder(b,1){return false};b.WriteByte(value);return true
}
