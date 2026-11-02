package xml

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

const ( jisASCII byte = iota; jisRoman; jisDouble )

func jis0208Rune(lead, trail byte) rune {
	if lead < 0x21 || lead > 0x7e || trail < 0x21 || trail > 0x7e { return 0 }
	at := ((int(lead)-0x21)*94+int(trail)-0x21)*2
	return rune(jis0208Data[at])<<8 | rune(jis0208Data[at+1])
}

// iso2022JPStep models the glibc 2.36 JP input designations, not MIME framing.
// Unknown complete escapes emit literal ESC for the XML grammar to diagnose.
// Incomplete physical EOF bytes remain buffered and emit no character.
func iso2022JPStep(input string, state byte) (r rune, n int, next byte, incomplete, invalid bool) {
	next = state
	if input[0] == 0x1b {
		if len(input)==1 || len(input)==2 && (input[1]=='(' || input[1]=='$') { return 0,0,state,true,false }
		if len(input)>=3 {
		switch input[:3] {
		case "\x1b(B": return 0,3,jisASCII,false,false
		case "\x1b(J": return 0,3,jisRoman,false,false
		case "\x1b$@", "\x1b$B": return 0,3,jisDouble,false,false
		}
		}
	}
	c := input[0]
	if c >= 0x80 { return 0,0,state,false,true }
	if state == jisDouble && c >= 0x21 && c != 0x7f {
		if len(input) < 2 { return 0,0,state,true,false }
		r = jis0208Rune(c,input[1])
		if r == 0 { return 0,0,state,false,true }
		return r,2,state,false,false
	}
	r = rune(c)
	if state == jisRoman {
		if c == 0x5c { r=0x00a5 } else if c == 0x7e { r=0x203e }
	}
	return r,1,state,false,false
}

// limit bounds consumed input, not physical input availability. Emission replays
// a verified prefix with the original lookahead, including bytes at a later
// conversion fault; clipping input there would turn a literal ESC into fake EOF.
func iso2022JPPass(ctx context.Context, input string, limit int, out *strings.Builder) (rawDecodeStatus,error) {
	s := rawDecodeStatus{fault:-1}
	state := jisASCII
	nextRaw,nextOutput := 0,0
	for i:=0;i<limit; {
		if i>=nextRaw || s.size>=nextOutput {
			if err:=ctx.Err();err!=nil{return s,err}
			nextRaw=i+4096;nextOutput=s.size+4096
		}
		r,n,next,incomplete,invalid:=iso2022JPStep(input[i:],state)
		if incomplete { s.incomplete=len(input)-i;return s,ctx.Err() }
		if invalid {
			s.fault,s.state=i,state
			s.cause=fmt.Errorf("invalid byte sequence for ISO-2022-JP at byte %d",i)
			return s,ctx.Err()
		}
		// A designation consumes bytes without a scalar, including at EOF.
		if n!=3 || input[i]!=0x1b {
			width:=utf8.RuneLen(r)
			if !appendSize(s.size,width){return s,conversionSizeError(len(input))}
			s.size+=width
			if out!=nil{out.WriteRune(r)}
		}
		i+=n;s.consumed=i;state=next
	}
	return s,ctx.Err()
}
