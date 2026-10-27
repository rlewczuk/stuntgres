package xml

import (
 "context"
 "errors"
 "strings"
 "testing"
)

func TestXMLCoreFailureRecoveryDiagnostics(t *testing.T) {
 // Independent PostgreSQL18.6 UTF8/C binary COPY captures: artifacts412,
 // 417,421,427, recorded before these expectations. Structured conditions,
 // character columns and offsets follow the matching Debian libxml2 sources.
 type event struct {
  condition Condition
  message string
  line,column,offset int
  source,caret string
 }
 cases:=[]struct{
  name,input,detail string
  events []event
 }{
  {
   name:"encoded_surrogate",input:"<a>\xed\xa0\x80</a>",
   detail:"line 1: Char 0xD800 out of allowed range\n<a>\xed\xa0\x80</a>\n   ^\nline 1: PCDATA invalid Char value 55296\n<a>\xed\xa0\x80</a>\n   ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xD800 out of allowed range",1,4,3,"<a>\xed\xa0\x80</a>","   ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 55296",1,4,3,"<a>\xed\xa0\x80</a>","   ^"},
   },
  },
  {
   name:"encoded_out_of_range",input:"<a>\xf4\x90\x80\x80</a>",
   detail:"line 1: Char 0x110000 out of allowed range\n<a>\xf4\x90\x80\x80</a>\n   ^\nline 1: PCDATA invalid Char value 1114112\n<a>\xf4\x90\x80\x80</a>\n   ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0x110000 out of allowed range",1,4,3,"<a>\xf4\x90\x80\x80</a>","   ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1114112",1,4,3,"<a>\xf4\x90\x80\x80</a>","   ^"},
   },
  },
  {
   name:"surrogate_comment",input:"<a><!--\xed\xa0\x80--></a>",
   detail:"line 1: Char 0xD800 out of allowed range\n<a><!--\xed\xa0\x80--></a>\n       ^\nline 1: xmlParseComment: invalid xmlChar value 55296\n<a><!--\xed\xa0\x80--></a>\n       ^\nline 1: Char 0xD800 out of allowed range\n<a><!--\xed\xa0\x80--></a>\n       ^\nline 1: PCDATA invalid Char value 55296\n<a><!--\xed\xa0\x80--></a>\n       ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xD800 out of allowed range",1,8,7,"<a><!--\xed\xa0\x80--></a>","       ^"},
    {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 55296",1,8,7,"<a><!--\xed\xa0\x80--></a>","       ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xD800 out of allowed range",1,8,7,"<a><!--\xed\xa0\x80--></a>","       ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 55296",1,8,7,"<a><!--\xed\xa0\x80--></a>","       ^"},
   },
  },
  {
   name:"out_of_range_comment",input:"<a><!--\xf4\x90\x80\x80--></a>",
   detail:"line 1: Char 0x110000 out of allowed range\n<a><!--\xf4\x90\x80\x80--></a>\n       ^\nline 1: xmlParseComment: invalid xmlChar value 1114112\n<a><!--\xf4\x90\x80\x80--></a>\n       ^\nline 1: Char 0x110000 out of allowed range\n<a><!--\xf4\x90\x80\x80--></a>\n       ^\nline 1: PCDATA invalid Char value 1114112\n<a><!--\xf4\x90\x80\x80--></a>\n       ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0x110000 out of allowed range",1,8,7,"<a><!--\xf4\x90\x80\x80--></a>","       ^"},
    {"XML_ERR_INVALID_CHAR","xmlParseComment: invalid xmlChar value 1114112",1,8,7,"<a><!--\xf4\x90\x80\x80--></a>","       ^"},
    {"XML_ERR_INVALID_CHAR","Char 0x110000 out of allowed range",1,8,7,"<a><!--\xf4\x90\x80\x80--></a>","       ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1114112",1,8,7,"<a><!--\xf4\x90\x80\x80--></a>","       ^"},
   },
  },
  {
   name:"surrogate_cdata",input:"<a><![CDATA[\xed\xa0\x80]]></a>",
   detail:"line 1: Char 0xD800 out of allowed range\n<a><![CDATA[\xed\xa0\x80]]></a>\n            ^\nline 1: Unregistered error message\n<a><![CDATA[\xed\xa0\x80]]></a>\n            ^\nline 1: Char 0xD800 out of allowed range\n<a><![CDATA[\xed\xa0\x80]]></a>\n            ^\nline 1: PCDATA invalid Char value 55296\n<a><![CDATA[\xed\xa0\x80]]></a>\n            ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[\xed\xa0\x80]]></a>\n               ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xD800 out of allowed range",1,13,12,"<a><![CDATA[\xed\xa0\x80]]></a>","            ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","Unregistered error message",1,13,12,"<a><![CDATA[\xed\xa0\x80]]></a>","            ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xD800 out of allowed range",1,13,12,"<a><![CDATA[\xed\xa0\x80]]></a>","            ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 55296",1,13,12,"<a><![CDATA[\xed\xa0\x80]]></a>","            ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,14,15,"<a><![CDATA[\xed\xa0\x80]]></a>","               ^"},
   },
  },
  {
   name:"out_of_range_cdata",input:"<a><![CDATA[\xf4\x90\x80\x80]]></a>",
   detail:"line 1: Char 0x110000 out of allowed range\n<a><![CDATA[\xf4\x90\x80\x80]]></a>\n            ^\nline 1: Unregistered error message\n<a><![CDATA[\xf4\x90\x80\x80]]></a>\n            ^\nline 1: Char 0x110000 out of allowed range\n<a><![CDATA[\xf4\x90\x80\x80]]></a>\n            ^\nline 1: PCDATA invalid Char value 1114112\n<a><![CDATA[\xf4\x90\x80\x80]]></a>\n            ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[\xf4\x90\x80\x80]]></a>\n                ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0x110000 out of allowed range",1,13,12,"<a><![CDATA[\xf4\x90\x80\x80]]></a>","            ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","Unregistered error message",1,13,12,"<a><![CDATA[\xf4\x90\x80\x80]]></a>","            ^"},
    {"XML_ERR_INVALID_CHAR","Char 0x110000 out of allowed range",1,13,12,"<a><![CDATA[\xf4\x90\x80\x80]]></a>","            ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 1114112",1,13,12,"<a><![CDATA[\xf4\x90\x80\x80]]></a>","            ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,14,16,"<a><![CDATA[\xf4\x90\x80\x80]]></a>","                ^"},
   },
  },
  {
   name:"text_mismatch",input:"<a>\xff</b>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3C 0x2F 0x62\n<a>\xff</b>\n   ^\nline 1: Opening and ending tag mismatch: a line 1 and b\n<a>\xff</b>\n        ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3C 0x2F 0x62",1,4,3,"<a>\xff</b>","   ^"},
    {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,9,8,"<a>\xff</b>","        ^"},
   },
  },
  {
   name:"attribute_mismatch",input:"<a x='\xff'></b>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x27 0x3E 0x3C\n<a x='\xff'></b>\n      ^\nline 1: Opening and ending tag mismatch: a line 1 and b\n<a x='\xff'></b>\n             ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x27 0x3E 0x3C",1,7,6,"<a x='\xff'></b>","      ^"},
    {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,14,13,"<a x='\xff'></b>","             ^"},
   },
  },
  {
   name:"pi_mismatch",input:"<a><?go \xff?></b>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3F 0x3E 0x3C\n<a><?go \xff?></b>\n        ^\nline 1: Opening and ending tag mismatch: a line 1 and b\n<a><?go \xff?></b>\n               ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x3F 0x3E 0x3C",1,9,8,"<a><?go \xff?></b>","        ^"},
    {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,16,15,"<a><?go \xff?></b>","               ^"},
   },
  },
  {
   name:"comment_mismatch",input:"<a><!--\xff--></b>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E\n<a><!--\xff--></b>\n       ^\nline 1: Opening and ending tag mismatch: a line 1 and b\n<a><!--\xff--></b>\n               ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x2D 0x2D 0x3E",1,8,7,"<a><!--\xff--></b>","       ^"},
    {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,16,15,"<a><!--\xff--></b>","               ^"},
   },
  },
  {
   name:"decoder_state_repeated_byte",input:"<a>\xff\xff</b>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0xFF 0x3C 0x2F\n<a>\xff\xff</b>\n   ^\nline 1: Opening and ending tag mismatch: a line 1 and b\n<a>\xff\xff</b>\n         ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0xFF 0x3C 0x2F",1,4,3,"<a>\xff\xff</b>","   ^"},
    {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,10,9,"<a>\xff\xff</b>","         ^"},
   },
  },
  {
   name:"decoder_state_multibyte_after_failure",input:"<a>\xff\uFFFE</b>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0xEF 0xBF 0xBE\n<a>\xff\uFFFE</b>\n   ^\nline 1: Opening and ending tag mismatch: a line 1 and b\n<a>\xff\uFFFE</b>\n           ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0xEF 0xBF 0xBE",1,4,3,"<a>\xff\uFFFE</b>","   ^"},
    {"XML_ERR_TAG_NAME_MISMATCH","Opening and ending tag mismatch: a line 1 and b",1,12,11,"<a>\xff\uFFFE</b>","           ^"},
   },
  },
  {
   name:"cdata_second_lookahead",input:"<a><![CDATA[é\uFFFE]]></a>",
   detail:"line 1: Char 0xFFFE out of allowed range\n<a><![CDATA[é\uFFFE]]></a>\n              ^\nline 1: Unregistered error message\n<a><![CDATA[é\uFFFE]]></a>\n              ^\nline 1: Char 0xFFFE out of allowed range\n<a><![CDATA[é\uFFFE]]></a>\n              ^\nline 1: PCDATA invalid Char value 65534\n<a><![CDATA[é\uFFFE]]></a>\n              ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[é\uFFFE]]></a>\n                 ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,14,14,"<a><![CDATA[é\uFFFE]]></a>","              ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","Unregistered error message",1,14,14,"<a><![CDATA[é\uFFFE]]></a>","              ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,14,14,"<a><![CDATA[é\uFFFE]]></a>","              ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",1,14,14,"<a><![CDATA[é\uFFFE]]></a>","              ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,15,17,"<a><![CDATA[é\uFFFE]]></a>","                 ^"},
   },
  },
  {
   name:"cdata_initial_current",input:"<a><![CDATA[éx\uFFFE]]></a>",
   detail:"line 1: Char 0xFFFE out of allowed range\n<a><![CDATA[éx\uFFFE]]></a>\n               ^\nline 1: CData section not finished\n<a><![CDATA[éx\uFFFE]]></a>\n               ^\nline 1: Char 0xFFFE out of allowed range\n<a><![CDATA[éx\uFFFE]]></a>\n               ^\nline 1: PCDATA invalid Char value 65534\n<a><![CDATA[éx\uFFFE]]></a>\n               ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[éx\uFFFE]]></a>\n                  ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,15,15,"<a><![CDATA[éx\uFFFE]]></a>","               ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished",1,15,15,"<a><![CDATA[éx\uFFFE]]></a>","               ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,15,15,"<a><![CDATA[éx\uFFFE]]></a>","               ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",1,15,15,"<a><![CDATA[éx\uFFFE]]></a>","               ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,16,18,"<a><![CDATA[éx\uFFFE]]></a>","                  ^"},
   },
  },
  {
   name:"cdata_committed_preview",input:"<a><![CDATA[abc\uFFFE]]></a>",
   detail:"line 1: Char 0xFFFE out of allowed range\n<a><![CDATA[abc\uFFFE]]></a>\n               ^\nline 1: CData section not finished\na\n<a><![CDATA[abc\uFFFE]]></a>\n               ^\nline 1: Char 0xFFFE out of allowed range\n<a><![CDATA[abc\uFFFE]]></a>\n               ^\nline 1: PCDATA invalid Char value 65534\n<a><![CDATA[abc\uFFFE]]></a>\n               ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[abc\uFFFE]]></a>\n                  ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,16,15,"<a><![CDATA[abc\uFFFE]]></a>","               ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished\na",1,16,15,"<a><![CDATA[abc\uFFFE]]></a>","               ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,16,15,"<a><![CDATA[abc\uFFFE]]></a>","               ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",1,16,15,"<a><![CDATA[abc\uFFFE]]></a>","               ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,17,18,"<a><![CDATA[abc\uFFFE]]></a>","                  ^"},
   },
  },
  {
   name:"cdata_rolling_preview",input:"<a><![CDATA[abcdef\uFFFE]]></a>",
   detail:"line 1: Char 0xFFFE out of allowed range\n<a><![CDATA[abcdef\uFFFE]]></a>\n                  ^\nline 1: CData section not finished\nabcd\n<a><![CDATA[abcdef\uFFFE]]></a>\n                  ^\nline 1: Char 0xFFFE out of allowed range\n<a><![CDATA[abcdef\uFFFE]]></a>\n                  ^\nline 1: PCDATA invalid Char value 65534\n<a><![CDATA[abcdef\uFFFE]]></a>\n                  ^\nline 1: Sequence ']]>' not allowed in content\n<a><![CDATA[abcdef\uFFFE]]></a>\n                     ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,19,18,"<a><![CDATA[abcdef\uFFFE]]></a>","                  ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished\nabcd",1,19,18,"<a><![CDATA[abcdef\uFFFE]]></a>","                  ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,19,18,"<a><![CDATA[abcdef\uFFFE]]></a>","                  ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",1,19,18,"<a><![CDATA[abcdef\uFFFE]]></a>","                  ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,20,21,"<a><![CDATA[abcdef\uFFFE]]></a>","                     ^"},
   },
  },
  {
   name:"cdata_normalized_preview",input:"<a><![CDATA[a\r\nbcd\uFFFE]]></a>",
   detail:"line 2: Char 0xFFFE out of allowed range\nbcd\uFFFE]]></a>\n   ^\nline 2: CData section not finished\na\nb\nbcd\uFFFE]]></a>\n   ^\nline 2: Char 0xFFFE out of allowed range\nbcd\uFFFE]]></a>\n   ^\nline 2: PCDATA invalid Char value 65534\nbcd\uFFFE]]></a>\n   ^\nline 2: Sequence ']]>' not allowed in content\nbcd\uFFFE]]></a>\n      ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",2,4,18,"bcd\uFFFE]]></a>","   ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished\na\nb",2,4,18,"bcd\uFFFE]]></a>","   ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",2,4,18,"bcd\uFFFE]]></a>","   ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",2,4,18,"bcd\uFFFE]]></a>","   ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",2,5,21,"bcd\uFFFE]]></a>","      ^"},
   },
  },
  {
   name:"cdata_normalized_trailing_lf",input:"<a><![CDATA[a\r\nbc\uFFFE]]></a>",
   detail:"line 2: Char 0xFFFE out of allowed range\nbc\uFFFE]]></a>\n  ^\nline 2: CData section not finished\na\nbc\uFFFE]]></a>\n  ^\nline 2: Char 0xFFFE out of allowed range\nbc\uFFFE]]></a>\n  ^\nline 2: PCDATA invalid Char value 65534\nbc\uFFFE]]></a>\n  ^\nline 2: Sequence ']]>' not allowed in content\nbc\uFFFE]]></a>\n     ^",
   events:[]event{
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",2,3,17,"bc\uFFFE]]></a>","  ^"},
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished\na",2,3,17,"bc\uFFFE]]></a>","  ^"},
    {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",2,3,17,"bc\uFFFE]]></a>","  ^"},
    {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",2,3,17,"bc\uFFFE]]></a>","  ^"},
    {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",2,4,20,"bc\uFFFE]]></a>","     ^"},
   },
  },
  {
   name:"cdata_nul_initial",input:"<a><![CDATA[\x00]]></a>",
   detail:"line 1: Unregistered error message\n<a><![CDATA[\n            ^\nline 1: Premature end of data in tag a line 1\n<a><![CDATA[\n            ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","Unregistered error message",1,13,12,"<a><![CDATA[","            ^"},
    {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,13,12,"<a><![CDATA[","            ^"},
   },
  },
  {
   name:"cdata_nul_second",input:"<a><![CDATA[a\x00]]></a>",
   detail:"line 1: Unregistered error message\n<a><![CDATA[a\n             ^\nline 1: Premature end of data in tag a line 1\n<a><![CDATA[a\n             ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","Unregistered error message",1,14,13,"<a><![CDATA[a","             ^"},
    {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,14,13,"<a><![CDATA[a","             ^"},
   },
  },
  {
   name:"cdata_nul_current",input:"<a><![CDATA[ab\x00]]></a>",
   detail:"line 1: CData section not finished\n<a><![CDATA[ab\n              ^\nline 1: Premature end of data in tag a line 1\n<a><![CDATA[ab\n              ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished",1,15,14,"<a><![CDATA[ab","              ^"},
    {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,15,14,"<a><![CDATA[ab","              ^"},
   },
  },
  {
   name:"cdata_nul_rolling",input:"<a><![CDATA[abc\x00]]></a>",
   detail:"line 1: CData section not finished\na\n<a><![CDATA[abc\n               ^\nline 1: Premature end of data in tag a line 1\n<a><![CDATA[abc\n               ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished\na",1,16,15,"<a><![CDATA[abc","               ^"},
    {"XML_ERR_TAG_NOT_FINISHED","Premature end of data in tag a line 1",1,16,15,"<a><![CDATA[abc","               ^"},
   },
  },
  {
   name:"cdata_ff_current",input:"<a><![CDATA[ab\xff]]></a>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x5D 0x5D 0x3E\n<a><![CDATA[ab\xff]]></a>\n              ^",
   events:[]event{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x5D 0x5D 0x3E",1,15,14,"<a><![CDATA[ab\xff]]></a>","              ^"}},
  },
  {
   name:"cdata_ff_rolling",input:"<a><![CDATA[abc\xff]]></a>",
   detail:"line 1: Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x5D 0x5D 0x3E\n<a><![CDATA[abc\xff]]></a>\n               ^",
   events:[]event{{"XML_ERR_INVALID_CHAR","Input is not proper UTF-8, indicate encoding !\nBytes: 0xFF 0x5D 0x5D 0x3E",1,16,15,"<a><![CDATA[abc\xff]]></a>","               ^"}},
  },
  {
   name:"short_text",input:"<a>\xff",
   detail:"line 1: internal error: detected an error in element content\n<a>\xff\n   ^",
   events:[]event{{"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,4,3,"<a>\xff","   ^"}},
  },
  {
   name:"short_comment",input:"<a><!--\xff",
   detail:"line 1: Comment not terminated\n<a><!--\xff\n       ^\nline 1: internal error: detected an error in element content\n<a><!--\xff\n       ^",
   events:[]event{
    {"XML_ERR_COMMENT_NOT_FINISHED","Comment not terminated",1,8,7,"<a><!--\xff","       ^"},
    {"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,8,7,"<a><!--\xff","       ^"},
   },
  },
  {
   name:"short_pi",input:"<a><?go \xff",
   detail:"line 1: ParsePI: PI go never end ...\n<a><?go \xff\n        ^\nline 1: internal error: detected an error in element content\n<a><?go \xff\n        ^",
   events:[]event{
    {"XML_ERR_PI_NOT_FINISHED","ParsePI: PI go never end ...",1,9,8,"<a><?go \xff","        ^"},
    {"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,9,8,"<a><?go \xff","        ^"},
   },
  },
  {
   name:"short_cdata_initial",input:"<a><![CDATA[\xff",
   detail:"line 1: Unregistered error message\n<a><![CDATA[\xff\n            ^\nline 1: internal error: detected an error in element content\n<a><![CDATA[\xff\n            ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","Unregistered error message",1,13,12,"<a><![CDATA[\xff","            ^"},
    {"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,13,12,"<a><![CDATA[\xff","            ^"},
   },
  },
  {
   name:"short_cdata_current",input:"<a><![CDATA[ab\xff",
   detail:"line 1: CData section not finished\n<a><![CDATA[ab\xff\n              ^\nline 1: internal error: detected an error in element content\n<a><![CDATA[ab\xff\n              ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished",1,15,14,"<a><![CDATA[ab\xff","              ^"},
    {"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,15,14,"<a><![CDATA[ab\xff","              ^"},
   },
  },
  {
   name:"short_cdata_rolling",input:"<a><![CDATA[abc\xff",
   detail:"line 1: CData section not finished\na\n<a><![CDATA[abc\xff\n               ^\nline 1: internal error: detected an error in element content\n<a><![CDATA[abc\xff\n               ^",
   events:[]event{
    {"XML_ERR_CDATA_NOT_FINISHED","CData section not finished\na",1,16,15,"<a><![CDATA[abc\xff","               ^"},
    {"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,16,15,"<a><![CDATA[abc\xff","               ^"},
   },
  },
  {
   name:"short_attribute",input:"<a x='\xff",
   detail:"line 1: AttValue: ' expected\n<a x='\xff\n      ^\nline 1: attributes construct error\n<a x='\xff\n      ^\nline 1: Couldn't find end of Start Tag a line 1\n<a x='\xff\n      ^",
   events:[]event{
    {"XML_ERR_ATTRIBUTE_NOT_FINISHED","AttValue: ' expected",1,7,6,"<a x='\xff","      ^"},
    {"XML_ERR_SPACE_REQUIRED","attributes construct error",1,7,6,"<a x='\xff","      ^"},
    {"XML_ERR_GT_REQUIRED","Couldn't find end of Start Tag a line 1",1,7,6,"<a x='\xff","      ^"},
   },
  },
 }
 // The independently captured 53-byte payload commits 51 bytes but previews
 // only 50. Repetition spells the exact literal bytes without a scanner oracle.
 longInput:="<a><![CDATA["+strings.Repeat("x",53)+"\uFFFE]]></a>"
 longPreview:="CData section not finished\n"+strings.Repeat("x",50)
 firstCaret,lastCaret:=strings.Repeat(" ",65)+"^",strings.Repeat(" ",68)+"^"
 cases=append(cases,struct{name,input,detail string;events []event}{
  "cdata_preview_50",longInput,
  "line 1: Char 0xFFFE out of allowed range\n"+longInput+"\n"+firstCaret+"\nline 1: "+longPreview+"\n"+longInput+"\n"+firstCaret+"\nline 1: Char 0xFFFE out of allowed range\n"+longInput+"\n"+firstCaret+"\nline 1: PCDATA invalid Char value 65534\n"+longInput+"\n"+firstCaret+"\nline 1: Sequence ']]>' not allowed in content\n"+longInput+"\n"+lastCaret,
  []event{
   {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,66,65,longInput,firstCaret},
   {"XML_ERR_CDATA_NOT_FINISHED",longPreview,1,66,65,longInput,firstCaret},
   {"XML_ERR_INVALID_CHAR","Char 0xFFFE out of allowed range",1,66,65,longInput,firstCaret},
   {"XML_ERR_INVALID_CHAR","PCDATA invalid Char value 65534",1,66,65,longInput,firstCaret},
   {"XML_ERR_MISPLACED_CDATA_END","Sequence ']]>' not allowed in content",1,67,68,longInput,lastCaret},
  },
 })
 for _,m:=range []Mode{Content,Document} {
  for _,tc:=range cases {
   if tc.name=="short_attribute" {
    if m==Document {
     tc.detail+="\nline 1: Extra content at the end of the document\n<a x='\xff\n      ^"
     tc.events=append(tc.events,event{"XML_ERR_DOCUMENT_END","Extra content at the end of the document",1,7,6,"<a x='\xff","      ^"})
    }else{
     tc.detail+="\nline 1: internal error: detected an error in element content\n<a x='\xff\n      ^"
     tc.events=append(tc.events,event{"XML_ERR_INTERNAL_ERROR","internal error: detected an error in element content",1,7,6,"<a x='\xff","      ^"})
    }
   }
   for _,kind:=range []InputKind{Text,Binary} {
    t.Run(tc.name+map[Mode]string{Content:"/content",Document:"/document"}[m]+map[InputKind]string{Text:"/text",Binary:"/binary"}[kind],func(t *testing.T){
     r,err:=Parse(context.Background(),tc.input,Options{Mode:m,InputKind:kind})
     var e *ParseError
     if !errors.As(err,&e)||r.valid||r.original!=""||r.requested!=0||r.effective!=0||r.declaration!=(Declaration{})||len(r.diagnostics)!=0 {t.Fatalf("result%+v error%v",r,err)}
     primary:="invalid XML content";if m==Document {primary="invalid XML document"}
     if e.Error()!=primary||e.RequestedMode!=m||e.EffectiveMode!=m||e.Category!=GrammarError||e.SourceBaseOffset!=0||e.Declaration!=(Declaration{})||e.Detail()!=tc.detail||len(e.Diagnostics)!=len(tc.events) {t.Fatalf("%+v detail%q want%q",e,e.Detail(),tc.detail)}
     for i,v:=range tc.events {
      want:=Diagnostic{Condition:v.condition,Domain:ParserDomain,Severity:Fatal,Line:v.line,Column:v.column,Offset:v.offset,Message:v.message,SourceContext:v.source,Caret:v.caret}
      if e.Diagnostics[i]!=want {t.Fatalf("event%d: %+v want %+v",i,e.Diagnostics[i],want)}
     }
     accept(t,"<healthy>é</healthy>",m)
    })
   }
  }
 }
}
