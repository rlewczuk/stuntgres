package xml

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// bits contains only the low pending bits, never an unbounded history. high is
// a withheld UTF16 high surrogate. Physical EOF does not flush this value state.
type utf7State struct { bits uint32; count uint8; high uint16; shifted bool }

func utf7Sextet(c byte) int {
	switch {
	case c>='A'&&c<='Z':return int(c-'A')
	case c>='a'&&c<='z':return int(c-'a')+26
	case c>='0'&&c<='9':return int(c-'0')+52
	case c=='+':return 62
	case c=='/':return 63
	}
	return -1
}

func utf7Pass(ctx context.Context,input string,limit int,out *strings.Builder)(rawDecodeStatus,error) {
	return utf7PassFrom(ctx,input,limit,out,utf7State{})
}

func utf7PassFrom(ctx context.Context,input string,limit int,out *strings.Builder,state utf7State)(rawDecodeStatus,error) {
	s:=rawDecodeStatus{fault:-1}
	// The chained UTF8 encoder's checkpoint advances only on accepted scalars,
	// not on a terminator or the next shift. Thus it includes residual bits and
	// may precede zero-output input belonging to an earlier shifted run.
	checkpoint,checkpointState:=0,state
	nextRaw,nextOutput:=0,0
	for i:=0;i<limit; {
		if i>=nextRaw || s.size>=nextOutput {if err:=ctx.Err();err!=nil{return s,err};nextRaw=i+4096;nextOutput=s.size+4096}
		c:=input[i]
		var r rune
		emits,invalid,low:=false,false,false
		n:=1
		next:=state
		if !state.shifted {
			switch {
			case c=='+':
				if len(input)-i<2 {s.incomplete=1;s.utf7=state;return s,ctx.Err()}
				if input[i+1]=='-' {r='+';emits=true;n=2}else{next=utf7State{shifted:true}}
			case c==9 || c==10 || c==13 || c>=0x20&&c<=0x7d&&c!='\\':r=rune(c);emits=true
			default:invalid=true
			}
		} else if value:=utf7Sextet(c);value>=0 {
			next.bits=state.bits<<6|uint32(value);next.count=state.count+6
			// A pending high surrogate must be followed by the 110111 prefix.
			// Check the current sextet before consuming it, as the FROM loop does.
			if state.high!=0 && next.count>=6 && next.bits>>(next.count-6)!=0x37 {invalid=true}
			if !invalid && next.count>=16 {
				unit:=uint16(next.bits>>(next.count-16));next.count-=16
				next.bits &= (uint32(1)<<next.count)-1
				switch {
				case state.high!=0:r=0x10000+(rune(state.high)-0xd800)*0x400+rune(unit)-0xdc00;next.high=0;emits=true
				case unit>=0xd800&&unit<=0xdbff:next.high=unit
				case unit>=0xdc00&&unit<=0xdfff:low=true
				default:r=rune(unit);emits=true
				}
			}
		} else {
			if state.high!=0 || state.count>=6 || state.bits!=0 {invalid=true}else{
				next=utf7State{}
				if c!='-' {state=next;continue} // reprocess the real implicit terminator
			}
		}
		if low {i=checkpoint;state=checkpointState;invalid=true}
		if invalid {
			s.fault=i;s.consumed=i;s.utf7=state
			s.cause=fmt.Errorf("invalid byte sequence for UTF-7 at byte %d",i)
			return s,ctx.Err()
		}
		if emits {
			width:=utf8.RuneLen(r)
			if !appendSize(s.size,width){return s,conversionSizeError(len(input))}
			s.size+=width;if out!=nil{out.WriteRune(r)}
			checkpoint,checkpointState=i+n,next
		}
		i+=n;s.consumed=i;state=next
	}
	s.utf7=state;return s,ctx.Err()
}
