package xml

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"
)

// active: 0 ASCII, 1 GB, 2 CNS1. Announcement is independent of active;
// zero announcement selects GB on SO. SS2 and the H designation change neither.
type cnState struct { active, announced byte }
type cnStepResult struct {
	r rune
	n int
	state cnState
	emits, incomplete, invalid bool
}

func rawCNSRune(plane int,lead,trail byte) rune {
	if plane==1 {
		if r:=rawCNS1Supplement(uint16(lead)<<8|uint16(trail));r!=0{return r}
		return rawMappedRune(4,uint32(lead|0x80)<<8|uint32(trail|0x80),2)
	}
	return rawMappedRune(4,0x8ea20000|uint32(lead|0x80)<<8|uint32(trail|0x80),4)
}

func iso2022CNStep(input string,state cnState) cnStepResult {
	s:=cnStepResult{state:state}
	c:=input[0]
	if c==0x1b {
		if len(input)<2 {s.incomplete=true;return s}
		if input[1]=='$' {
			if len(input)<3 || (input[2]==')'||input[2]=='*') && len(input)<4 {s.incomplete=true;return s}
			if strings.HasPrefix(input,"\x1b$)A"){s.state.announced=1;s.n=4;return s}
			if strings.HasPrefix(input,"\x1b$)G"){s.state.announced=2;s.n=4;return s}
			if strings.HasPrefix(input,"\x1b$*H"){s.n=4;return s}
		}
		if input[1]=='N' {
			if len(input)<4 {s.incomplete=true;return s}
			lead,trail:=input[2],input[3]
			if lead<0x21 || lead>0x7d || trail<0x21 || trail>0x7e || uint16(lead)<<8|uint16(trail)>0x7244 {s.invalid=true;return s}
			s.r=rawCNSRune(2,lead,trail)
			if s.r==0{s.invalid=true;return s}
			s.n=4;s.emits=true;return s
		}
	}
	if c==0x0f {s.state.active=0;s.n=1;return s}
	if c==0x0e {s.state.active=state.announced;if s.state.active==0{s.state.active=1};s.n=1;return s}
	if c>=0x7f {s.invalid=true;return s}
	if state.active==0 {s.r=rune(c);s.n=1;s.emits=true;return s}
	if c<0x21 || state.active==1 && c>0x77 || state.active==2 && c>0x7d {s.invalid=true;return s}
	if len(input)<2 {s.incomplete=true;return s}
	t:=input[1]
	if t<0x21 || t>0x7e || state.active==2 && uint16(c)<<8|uint16(t)>0x7d4b {s.invalid=true;return s}
	if state.active==1{s.r=rawMappedRune(2,uint32(c|0x80)<<8|uint32(t|0x80),2)}else{s.r=rawCNSRune(1,c,t)}
	if s.r==0{s.invalid=true;return s}
	s.n=2;s.emits=true;return s
}

func iso2022CNPass(ctx context.Context,input string,limit int,out *strings.Builder)(rawDecodeStatus,error) {
	s:=rawDecodeStatus{fault:-1};state:=cnState{}
	nextRaw,nextOutput:=0,0
	for i:=0;i<limit; {
		if i>=nextRaw || s.size>=nextOutput {if err:=ctx.Err();err!=nil{return s,err};nextRaw=i+4096;nextOutput=s.size+4096}
		step:=iso2022CNStep(input[i:],state);s.cn=state
		if step.incomplete{s.incomplete=len(input)-i;return s,ctx.Err()}
		if step.invalid{s.fault=i;s.cause=fmt.Errorf("invalid byte sequence for ISO-2022-CN at byte %d",i);return s,ctx.Err()}
		if step.emits{width:=utf8.RuneLen(step.r);if !appendSize(s.size,width){return s,conversionSizeError(len(input))};s.size+=width;if out!=nil{out.WriteRune(step.r)}}
		i+=step.n;s.consumed=i;state=step.state
	}
	s.cn=state;return s,ctx.Err()
}
