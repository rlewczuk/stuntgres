package parser

import "strings"

func FTSParserFunctionName(name string) bool {
	return strings.EqualFold(name,"ts_parse") || strings.EqualFold(name,"ts_token_type")
}
