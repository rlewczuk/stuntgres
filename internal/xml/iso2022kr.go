package xml

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

type krStepResult struct {
	r rune
	n int
	selected bool
	emits, incomplete, invalid bool
}

// Designation does not select KSC. Availability precedes KSC lead validation,
// including at physical EOF; selected controls are not ASCII exceptions.
func iso2022KRStep(input string, selected bool) krStepResult {
	s := krStepResult{selected:selected}
	c := input[0]
	if c == 0x1b {
		if len(input)<2 || input[1]=='$' && (len(input)<3 || input[2]==')' && len(input)<4) { s.incomplete=true;return s }
		if strings.HasPrefix(input,"\x1b$)C") { s.n=4;return s }
	}
	if c==0x0e || c==0x0f { s.selected=c==0x0e;s.n=1;return s }
	if c>=0x80 { s.invalid=true;return s }
	if !selected { s.r=rune(c);s.n=1;s.emits=true;return s }
	if len(input)<2 { s.incomplete=true;return s }
	t:=input[1]
	if c<0x21 || c>0x7d || c==0x49 || t<0x21 || t>0x7e { s.invalid=true;return s }
	s.r=rawMappedRune(3,uint32(c|0x80)<<8|uint32(t|0x80),2)
	if s.r==0 { s.invalid=true;return s }
	s.n=2;s.emits=true;return s
}

func iso2022KRPass(ctx context.Context,input string,limit int,out *strings.Builder)(rawDecodeStatus,error) {
	s:=rawDecodeStatus{fault:-1}
	selected:=false
	nextRaw,nextOutput:=0,0
	for i:=0;i<limit; {
		if i>=nextRaw || s.size>=nextOutput { if err:=ctx.Err();err!=nil{return s,err};nextRaw=i+4096;nextOutput=s.size+4096 }
		step:=iso2022KRStep(input[i:],selected)
		s.kr=selected
		if step.incomplete { s.incomplete=len(input)-i;return s,ctx.Err() }
		if step.invalid { s.fault=i;s.cause=fmt.Errorf("invalid byte sequence for ISO-2022-KR at byte %d",i);return s,ctx.Err() }
		if step.emits {
			width:=utf8.RuneLen(step.r)
			if !appendSize(s.size,width){return s,conversionSizeError(len(input))}
			s.size+=width;if out!=nil{out.WriteRune(step.r)}
		}
		i+=step.n;s.consumed=i;selected=step.selected
	}
	s.kr=selected;return s,ctx.Err()
}
