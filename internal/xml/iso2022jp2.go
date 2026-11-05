package xml

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/sqltype/pgencoding18"
)

type jp2Set byte
const (
	jp2ASCII jp2Set = iota
	jp2Roman
	jp2Kana
	jp2JIS0208
	jp2JIS0212
	jp2GB2312
	jp2KSC5601
)
type jp2G2 byte
const (
	jp2Unassigned jp2G2 = iota
	jp2Latin
	jp2Greek
)

// Both designations are invocation values. A single shift changes neither.
// Zero initializes ASCII G0 with no G2, including every sizing/emission pass.
type jp2State struct { g0 jp2Set; g2 jp2G2 }
type jp2StepResult struct {
	r rune
	n int
	state jp2State
	emits, incomplete, invalid bool
}

func jis0212Rune(lead, trail byte) rune {
	if lead<0x21 || lead>0x7e || trail<0x21 || trail>0x7e { return 0 }
	at:=((int(lead)-0x21)*94+int(trail)-0x21)*2
	return rune(jis0212Data[at])<<8 | rune(jis0212Data[at+1])
}

// Resolve only the independently qualified immutable mappings, not the foreign
// framing rules of PostgreSQL Verify/Length/Step. These three maps are scalar.
func jp2MappedRune(id int, packed uint32, width int) rune {
	word,_:=pgencoding18.LookupUTF8(id,packed,width)
	switch {
	case word<=0x7f: return rune(word)
	case word<=0xffff: return rune(word>>8&0x1f)<<6 | rune(word&0x3f)
	case word<=0xffffff: return rune(word>>16&0x0f)<<12 | rune(word>>8&0x3f)<<6 | rune(word&0x3f)
	default: return rune(word>>24&7)<<18 | rune(word>>16&0x3f)<<12 | rune(word>>8&0x3f)<<6 | rune(word&0x3f)
	}
}

// iso2022JP2Step follows the pinned glibc input handler, not MIME restrictions.
// Its input is the full physical suffix, never clipped to an emission limit.
// Explicit emits distinguishes a designation from SS2 (both consume ESC bytes)
// and from the genuine U+0000 scalar owned by subsequent XML grammar.
func iso2022JP2Step(input string, state jp2State) jp2StepResult {
	s:=jp2StepResult{state:state}
	c:=input[0]
	if c==0x1b {
		if len(input)<3 || input[1]=='$' && input[2]=='(' && len(input)<4 { s.incomplete=true;return s }
		switch input[:3] {
		case "\x1b(B": s.state.g0=jp2ASCII;s.n=3;return s
		case "\x1b(J": s.state.g0=jp2Roman;s.n=3;return s
		case "\x1b(I": s.state.g0=jp2Kana;s.n=3;return s
		case "\x1b$@", "\x1b$B": s.state.g0=jp2JIS0208;s.n=3;return s
		case "\x1b$A": s.state.g0=jp2GB2312;s.n=3;return s
		case "\x1b$(":
			switch input[3] {
			case 'C': s.state.g0=jp2KSC5601;s.n=4;return s
			case 'D': s.state.g0=jp2JIS0212;s.n=4;return s
			}
		case "\x1b.A": s.state.g2=jp2Latin;s.n=3;return s
		case "\x1b.F": s.state.g2=jp2Greek;s.n=3;return s
		}
		if input[1]=='N' {
			payload:=input[2]
			switch state.g2 {
			case jp2Latin: s.r=rune(payload|0x80)
			case jp2Greek:
				if payload<0x20 || payload>=0x80 { s.invalid=true;return s }
				s.r=jp2MappedRune(27,uint32(payload|0x80),1)
				if s.r==0 { s.invalid=true;return s }
			default: s.invalid=true;return s
			}
			s.n=3;s.emits=true;return s
		}
	}
	if c>=0x80 { s.invalid=true;return s }
	if state.g0==jp2ASCII || c<0x21 || c==0x7f {
		s.r=rune(c);s.n=1;s.emits=true;return s
	}
	switch state.g0 {
	case jp2Roman:
		s.r=rune(c)
		if c==0x5c { s.r=0x00a5 } else if c==0x7e { s.r=0x203e }
		s.n=1;s.emits=true;return s
	case jp2Kana:
		if c>0x5f { s.invalid=true;return s }
		s.r=0xff61+rune(c-0x21);s.n=1;s.emits=true;return s
	case jp2JIS0212:
		// Unlike KSC, this helper rejects a bad first lead even at EOF.
		if c<0x22 || c>0x6d { s.invalid=true;return s }
	case jp2GB2312:
		if c>0x77 { s.invalid=true;return s }
	}
	if len(input)<2 { s.incomplete=true;return s }
	trail:=input[1]
	if trail<0x21 || trail>0x7e || state.g0==jp2KSC5601 && (c==0x49 || c>=0x7e) { s.invalid=true;return s }
	switch state.g0 {
	case jp2JIS0208: s.r=jis0208Rune(c,trail)
	case jp2JIS0212: s.r=jis0212Rune(c,trail)
	case jp2GB2312: s.r=jp2MappedRune(2,uint32(c|0x80)<<8|uint32(trail|0x80),2)
	case jp2KSC5601: s.r=jp2MappedRune(3,uint32(c|0x80)<<8|uint32(trail|0x80),2)
	}
	if s.r==0 { s.invalid=true;return s }
	s.n=2;s.emits=true;return s
}

func iso2022JP2Pass(ctx context.Context, input string, limit int, out *strings.Builder) (rawDecodeStatus,error) {
	s:=rawDecodeStatus{fault:-1}
	state:=jp2State{}
	nextRaw,nextOutput:=0,0
	for i:=0;i<limit; {
		if i>=nextRaw || s.size>=nextOutput {
			if err:=ctx.Err();err!=nil { return s,err }
			nextRaw=i+4096;nextOutput=s.size+4096
		}
		step:=iso2022JP2Step(input[i:],state)
		if step.incomplete { s.incomplete=len(input)-i;s.jp2=state;return s,ctx.Err() }
		if step.invalid {
			s.fault=i;s.jp2=state
			s.cause=fmt.Errorf("invalid byte sequence for ISO-2022-JP-2 at byte %d",i)
			return s,ctx.Err()
		}
		if step.emits {
			width:=utf8.RuneLen(step.r)
			if !appendSize(s.size,width) { return s,conversionSizeError(len(input)) }
			s.size+=width
			if out!=nil { out.WriteRune(step.r) }
		}
		i+=step.n;s.consumed=i;state=step.state
	}
	s.jp2=state
	return s,ctx.Err()
}
