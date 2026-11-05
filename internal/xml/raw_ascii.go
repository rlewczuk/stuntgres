package xml

import (
	"context"
	"fmt"
)

// Builtin ASCII validates every physical byte, including after NUL or a closed
// root. It has no iconv conversion callbacks; XML grammar owns sevenbit controls.
// A validated identity prefix can share its immutable spelling without copying.
func rawASCIIPass(ctx context.Context, input string) (rawDecodeStatus,error) {
	s:=rawDecodeStatus{fault:-1}
	next:=0
	for i:=range len(input) {
		if i>=next {
			if err:=ctx.Err();err!=nil { return s,err }
			next=i+4096
		}
		if input[i]>=0x80 {
			s.size,s.consumed,s.fault=i,i,i
			s.cause=fmt.Errorf("invalid byte sequence for ASCII at byte %d",i)
			return s,ctx.Err()
		}
	}
	s.size,s.consumed=len(input),len(input)
	return s,ctx.Err()
}
