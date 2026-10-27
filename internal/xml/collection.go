package xml

import (
 "errors"
 "math"
 "unsafe"
)

// Collection storage uses the same checked append-size boundary as character
// and diagnostic buffers. The multiplication is checked before it is evaluated.
// A rejected append leaves the old collection owned by the failed operation.
func (p *parser) collectionSize(length,width int)bool {
 if p.err!=nil{return false}
 p.err=p.ctx.Err();if p.err!=nil{return false}
 if length<0||width<=0||length>math.MaxInt/width{p.err=errors.New("XML parse collection size overflow");return false}
 current:=length*width
 if !appendSize(current,width){p.err=appendSizeError(current,width);return false}
 return true
}
func checkedAppend[T any](p *parser,items []T,value T)[]T {
 width:=int(unsafe.Sizeof(value))
 if !p.collectionSize(len(items),width){return items}
 if len(items)<cap(items){return append(items,value)}
 capacity:=len(items)+1
 if len(items)<=math.MaxInt/2{capacity=max(capacity,len(items)*2)}
 if capacity>math.MaxInt/width||!appendSize(0,capacity*width){capacity=len(items)+1}
 p.err=p.ctx.Err();if p.err!=nil{return items}
 grown:=make([]T,len(items),capacity)
 p.err=p.ctx.Err();if p.err!=nil{return items}
 chunk:=max(1,4096/width)
 for at:=0;at<len(items); {
  p.err=p.ctx.Err();if p.err!=nil{return items}
  end:=at+min(chunk,len(items)-at);copy(grown[at:end],items[at:end]);at=end
 }
 grown=append(grown,value)
 p.err=p.ctx.Err();if p.err!=nil{return items}
 return grown
}
