// Ordered states and actions ported from PostgreSQL REL_18_6's
// src/backend/tsearch/wparser_def.c (PostgreSQL License).
package fts

type documentState uint8

const (
	TPS_Base documentState = iota
	TPS_InNumWord
	TPS_InAsciiWord
	TPS_InWord
	TPS_InUnsignedInt
	TPS_InSignedIntFirst
	TPS_InSignedInt
	TPS_InSpace
	TPS_InUDecimalFirst
	TPS_InUDecimal
	TPS_InDecimalFirst
	TPS_InDecimal
	TPS_InVerVersion
	TPS_InSVerVersion
	TPS_InVersionFirst
	TPS_InVersion
	TPS_InMantissaFirst
	TPS_InMantissaSign
	TPS_InMantissa
	TPS_InXMLEntityFirst
	TPS_InXMLEntity
	TPS_InXMLEntityNumFirst
	TPS_InXMLEntityNum
	TPS_InXMLEntityHexNumFirst
	TPS_InXMLEntityHexNum
	TPS_InXMLEntityEnd
	TPS_InTagFirst
	TPS_InXMLBegin
	TPS_InTagCloseFirst
	TPS_InTagName
	TPS_InTagBeginEnd
	TPS_InTag
	TPS_InTagEscapeK
	TPS_InTagEscapeKK
	TPS_InTagBackSleshed
	TPS_InTagEnd
	TPS_InCommentFirst
	TPS_InCommentLast
	TPS_InComment
	TPS_InCloseCommentFirst
	TPS_InCloseCommentLast
	TPS_InCommentEnd
	TPS_InHostFirstDomain
	TPS_InHostDomainSecond
	TPS_InHostDomain
	TPS_InPortFirst
	TPS_InPort
	TPS_InHostFirstAN
	TPS_InHost
	TPS_InEmail
	TPS_InFileFirst
	TPS_InFileTwiddle
	TPS_InPathFirst
	TPS_InPathFirstFirst
	TPS_InPathSecond
	TPS_InFile
	TPS_InFileNext
	TPS_InURLPathFirst
	TPS_InURLPathStart
	TPS_InURLPath
	TPS_InFURL
	TPS_InProtocolFirst
	TPS_InProtocolSecond
	TPS_InProtocolEnd
	TPS_InHyphenAsciiWordFirst
	TPS_InHyphenAsciiWord
	TPS_InHyphenWordFirst
	TPS_InHyphenWord
	TPS_InHyphenNumWordFirst
	TPS_InHyphenNumWord
	TPS_InHyphenDigitLookahead
	TPS_InParseHyphen
	TPS_InParseHyphenHyphen
	TPS_InHyphenWordPart
	TPS_InHyphenAsciiWordPart
	TPS_InHyphenNumWordPart
	TPS_InHyphenUnsignedInt
	TPS_Null
)

type documentPredicate uint8

const (
	documentAlways documentPredicate = iota
	p_isEOF
	p_isURLPath
	p_isalnum
	p_isalpha
	p_isasclet
	p_isdigit
	p_iseqC
	p_ishost
	p_isignore
	p_isnotalnum
	p_isspace
	p_isspecial
	p_isstophost
	p_isurlchar
	p_isxdigit
)

const (
	A_NEXT uint8 = 0
	A_BINGO uint8 = 1
	A_POP uint8 = 2
	A_PUSH uint8 = 4
	A_RERUN uint8 = 8
	A_CLEAR uint8 = 16
	A_MERGE uint8 = 32
	A_CLRALL uint8 = 64
)

const (
	documentNoSpecial uint8 = iota
	SpecialTags
	SpecialFURL
	SpecialHyphen
	SpecialVerVersion
)

type documentAction struct {
	predicate documentPredicate
	c byte
	flags uint8
	to documentState
	token int32
	special uint8
}

var documentActions = [...][]documentAction{
	TPS_Base: {
		{p_isEOF, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '<', A_PUSH, TPS_InTagFirst, 0, documentNoSpecial},
		{p_isignore, 0, A_NEXT, TPS_InSpace, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InAsciiWord, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InWord, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InUnsignedInt, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InSignedIntFirst, 0, documentNoSpecial},
		{p_iseqC, '+', A_PUSH, TPS_InSignedIntFirst, 0, documentNoSpecial},
		{p_iseqC, '&', A_PUSH, TPS_InXMLEntityFirst, 0, documentNoSpecial},
		{p_iseqC, '~', A_PUSH, TPS_InFileTwiddle, 0, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFileFirst, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InPathFirstFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_InSpace, 0, documentNoSpecial},
	},
	TPS_InNumWord: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 3, documentNoSpecial},
		{p_isalnum, 0, A_NEXT, TPS_InNumWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InNumWord, 0, documentNoSpecial},
		{p_iseqC, '@', A_PUSH, TPS_InEmail, 0, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFileFirst, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InFileNext, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHyphenNumWordFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 3, documentNoSpecial},
	},
	TPS_InAsciiWord: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 1, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InHostFirstDomain, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InFileNext, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHyphenAsciiWordFirst, 0, documentNoSpecial},
		{p_iseqC, '_', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '@', A_PUSH, TPS_InEmail, 0, documentNoSpecial},
		{p_iseqC, ':', A_PUSH, TPS_InProtocolFirst, 0, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFileFirst, 0, documentNoSpecial},
		{p_isdigit, 0, A_PUSH, TPS_InHost, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InNumWord, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InWord, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 1, documentNoSpecial},
	},
	TPS_InWord: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 2, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InNumWord, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHyphenWordFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 2, documentNoSpecial},
	},
	TPS_InUnsignedInt: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 22, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InHostFirstDomain, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InUDecimalFirst, 0, documentNoSpecial},
		{p_iseqC, 'e', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{p_iseqC, 'E', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '_', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '@', A_PUSH, TPS_InEmail, 0, documentNoSpecial},
		{p_isasclet, 0, A_PUSH, TPS_InHost, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InNumWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InNumWord, 0, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFileFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 22, documentNoSpecial},
	},
	TPS_InSignedIntFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT | A_CLEAR, TPS_InSignedInt, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InSignedInt: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 21, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InDecimalFirst, 0, documentNoSpecial},
		{p_iseqC, 'e', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{p_iseqC, 'E', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 21, documentNoSpecial},
	},
	TPS_InSpace: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 12, documentNoSpecial},
		{p_iseqC, '<', A_BINGO, TPS_Base, 12, documentNoSpecial},
		{p_isignore, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_BINGO, TPS_Base, 12, documentNoSpecial},
		{p_iseqC, '+', A_BINGO, TPS_Base, 12, documentNoSpecial},
		{p_iseqC, '&', A_BINGO, TPS_Base, 12, documentNoSpecial},
		{p_iseqC, '/', A_BINGO, TPS_Base, 12, documentNoSpecial},
		{p_isnotalnum, 0, A_NEXT, TPS_InSpace, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 12, documentNoSpecial},
	},
	TPS_InUDecimalFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_CLEAR, TPS_InUDecimal, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InUDecimal: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 20, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InUDecimal, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InVersionFirst, 0, documentNoSpecial},
		{p_iseqC, 'e', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{p_iseqC, 'E', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 20, documentNoSpecial},
	},
	TPS_InDecimalFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_CLEAR, TPS_InDecimal, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InDecimal: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 20, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InDecimal, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InVerVersion, 0, documentNoSpecial},
		{p_iseqC, 'e', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{p_iseqC, 'E', A_PUSH, TPS_InMantissaFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 20, documentNoSpecial},
	},
	TPS_InVerVersion: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_RERUN, TPS_InSVerVersion, 0, SpecialVerVersion},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InSVerVersion: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_BINGO | A_CLRALL, TPS_InUnsignedInt, 12, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InVersionFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_CLEAR, TPS_InVersion, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InVersion: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 8, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InVersion, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InVersionFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 8, documentNoSpecial},
	},
	TPS_InMantissaFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_CLEAR, TPS_InMantissa, 0, documentNoSpecial},
		{p_iseqC, '+', A_NEXT, TPS_InMantissaSign, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InMantissaSign, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InMantissaSign: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_CLEAR, TPS_InMantissa, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InMantissa: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 7, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InMantissa, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 7, documentNoSpecial},
	},
	TPS_InXMLEntityFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '#', A_NEXT, TPS_InXMLEntityNumFirst, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, ':', A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLEntity: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isalnum, 0, A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, ':', A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, '.', A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InXMLEntity, 0, documentNoSpecial},
		{p_iseqC, ';', A_NEXT, TPS_InXMLEntityEnd, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLEntityNumFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, 'x', A_NEXT, TPS_InXMLEntityHexNumFirst, 0, documentNoSpecial},
		{p_iseqC, 'X', A_NEXT, TPS_InXMLEntityHexNumFirst, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InXMLEntityNum, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLEntityHexNumFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isxdigit, 0, A_NEXT, TPS_InXMLEntityHexNum, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLEntityNum: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InXMLEntityNum, 0, documentNoSpecial},
		{p_iseqC, ';', A_NEXT, TPS_InXMLEntityEnd, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLEntityHexNum: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isxdigit, 0, A_NEXT, TPS_InXMLEntityHexNum, 0, documentNoSpecial},
		{p_iseqC, ';', A_NEXT, TPS_InXMLEntityEnd, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLEntityEnd: {
		{documentAlways, 0, A_BINGO | A_CLEAR, TPS_Base, 23, documentNoSpecial},
	},
	TPS_InTagFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InTagCloseFirst, 0, documentNoSpecial},
		{p_iseqC, '!', A_PUSH, TPS_InCommentFirst, 0, documentNoSpecial},
		{p_iseqC, '?', A_PUSH, TPS_InXMLBegin, 0, documentNoSpecial},
		{p_isasclet, 0, A_PUSH, TPS_InTagName, 0, documentNoSpecial},
		{p_iseqC, ':', A_PUSH, TPS_InTagName, 0, documentNoSpecial},
		{p_iseqC, '_', A_PUSH, TPS_InTagName, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InXMLBegin: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, 'x', A_NEXT, TPS_InTag, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InTagCloseFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InTagName, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InTagName: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_InTagBeginEnd, 0, documentNoSpecial},
		{p_iseqC, '>', A_NEXT, TPS_InTagEnd, 0, SpecialTags},
		{p_isspace, 0, A_NEXT, TPS_InTag, 0, SpecialTags},
		{p_isalnum, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, ':', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '.', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InTagBeginEnd: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '>', A_NEXT, TPS_InTagEnd, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InTag: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '>', A_NEXT, TPS_InTagEnd, 0, SpecialTags},
		{p_iseqC, '\'', A_NEXT, TPS_InTagEscapeK, 0, documentNoSpecial},
		{p_iseqC, '"', A_NEXT, TPS_InTagEscapeKK, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '=', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '#', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, ':', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '.', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '&', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '?', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '%', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '~', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_isspace, 0, A_NEXT, TPS_Null, 0, SpecialTags},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InTagEscapeK: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '\\', A_PUSH, TPS_InTagBackSleshed, 0, documentNoSpecial},
		{p_iseqC, '\'', A_NEXT, TPS_InTag, 0, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_InTagEscapeK, 0, documentNoSpecial},
	},
	TPS_InTagEscapeKK: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '\\', A_PUSH, TPS_InTagBackSleshed, 0, documentNoSpecial},
		{p_iseqC, '"', A_NEXT, TPS_InTag, 0, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_InTagEscapeKK, 0, documentNoSpecial},
	},
	TPS_InTagBackSleshed: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{documentAlways, 0, A_MERGE, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InTagEnd: {
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_Base, 13, documentNoSpecial},
	},
	TPS_InCommentFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InCommentLast, 0, documentNoSpecial},
		{p_iseqC, 'D', A_NEXT, TPS_InTag, 0, documentNoSpecial},
		{p_iseqC, 'd', A_NEXT, TPS_InTag, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InCommentLast: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InComment, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InComment: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InCloseCommentFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InCloseCommentFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InCloseCommentLast, 0, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_InComment, 0, documentNoSpecial},
	},
	TPS_InCloseCommentLast: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '>', A_NEXT, TPS_InCommentEnd, 0, documentNoSpecial},
		{documentAlways, 0, A_NEXT, TPS_InComment, 0, documentNoSpecial},
	},
	TPS_InCommentEnd: {
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_Base, 13, documentNoSpecial},
	},
	TPS_InHostFirstDomain: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHostDomainSecond, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHost, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHostDomainSecond: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHostDomain, 0, documentNoSpecial},
		{p_isdigit, 0, A_PUSH, TPS_InHost, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '_', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InHostFirstDomain, 0, documentNoSpecial},
		{p_iseqC, '@', A_PUSH, TPS_InEmail, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHostDomain: {
		{p_isEOF, 0, A_BINGO | A_CLRALL, TPS_Base, 6, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHostDomain, 0, documentNoSpecial},
		{p_isdigit, 0, A_PUSH, TPS_InHost, 0, documentNoSpecial},
		{p_iseqC, ':', A_PUSH, TPS_InPortFirst, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '_', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InHostFirstDomain, 0, documentNoSpecial},
		{p_iseqC, '@', A_PUSH, TPS_InEmail, 0, documentNoSpecial},
		{p_isdigit, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isstophost, 0, A_BINGO | A_CLRALL, TPS_InURLPathStart, 6, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFURL, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_Base, 6, documentNoSpecial},
	},
	TPS_InPortFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InPort, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InPort: {
		{p_isEOF, 0, A_BINGO | A_CLRALL, TPS_Base, 6, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InPort, 0, documentNoSpecial},
		{p_isstophost, 0, A_BINGO | A_CLRALL, TPS_InURLPathStart, 6, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFURL, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_Base, 6, documentNoSpecial},
	},
	TPS_InHostFirstAN: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHost, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHost, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHost: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHost, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHost, 0, documentNoSpecial},
		{p_iseqC, '@', A_PUSH, TPS_InEmail, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InHostFirstDomain, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{p_iseqC, '_', A_PUSH, TPS_InHostFirstAN, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InEmail: {
		{p_isstophost, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_ishost, 0, A_BINGO | A_CLRALL, TPS_Base, 4, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InFileFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '.', A_NEXT, TPS_InPathFirst, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '~', A_PUSH, TPS_InFileTwiddle, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InFileTwiddle: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_InFileFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InPathFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '.', A_NEXT, TPS_InPathSecond, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_InFileFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InPathFirstFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '.', A_NEXT, TPS_InPathSecond, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_InFileFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InPathSecond: {
		{p_isEOF, 0, A_BINGO | A_CLEAR, TPS_Base, 19, documentNoSpecial},
		{p_iseqC, '/', A_NEXT | A_PUSH, TPS_InFileFirst, 0, documentNoSpecial},
		{p_iseqC, '/', A_BINGO | A_CLEAR, TPS_Base, 19, documentNoSpecial},
		{p_isspace, 0, A_BINGO | A_CLEAR, TPS_Base, 19, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InFile: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 19, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '.', A_PUSH, TPS_InFileNext, 0, documentNoSpecial},
		{p_iseqC, '_', A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '-', A_NEXT, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '/', A_PUSH, TPS_InFileFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 19, documentNoSpecial},
	},
	TPS_InFileNext: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_CLEAR, TPS_InFile, 0, documentNoSpecial},
		{p_isdigit, 0, A_CLEAR, TPS_InFile, 0, documentNoSpecial},
		{p_iseqC, '_', A_CLEAR, TPS_InFile, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InURLPathFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isurlchar, 0, A_NEXT, TPS_InURLPath, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InURLPathStart: {
		{documentAlways, 0, A_NEXT, TPS_InURLPath, 0, documentNoSpecial},
	},
	TPS_InURLPath: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 18, documentNoSpecial},
		{p_isurlchar, 0, A_NEXT, TPS_InURLPath, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_Base, 18, documentNoSpecial},
	},
	TPS_InFURL: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isURLPath, 0, A_BINGO | A_CLRALL, TPS_Base, 5, SpecialFURL},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InProtocolFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_InProtocolSecond, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InProtocolSecond: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_iseqC, '/', A_NEXT, TPS_InProtocolEnd, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InProtocolEnd: {
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_Base, 14, documentNoSpecial},
	},
	TPS_InHyphenAsciiWordFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHyphenAsciiWord, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWord, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenDigitLookahead, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHyphenAsciiWord: {
		{p_isEOF, 0, A_BINGO | A_CLRALL, TPS_InParseHyphen, 16, SpecialHyphen},
		{p_isasclet, 0, A_NEXT, TPS_InHyphenAsciiWord, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenWord, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHyphenAsciiWordFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_InParseHyphen, 16, SpecialHyphen},
	},
	TPS_InHyphenWordFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWord, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenDigitLookahead, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHyphenWord: {
		{p_isEOF, 0, A_BINGO | A_CLRALL, TPS_InParseHyphen, 17, SpecialHyphen},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenWord, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHyphenWordFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_InParseHyphen, 17, SpecialHyphen},
	},
	TPS_InHyphenNumWordFirst: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenDigitLookahead, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHyphenNumWord: {
		{p_isEOF, 0, A_BINGO | A_CLRALL, TPS_InParseHyphen, 15, SpecialHyphen},
		{p_isalnum, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InHyphenNumWordFirst, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO | A_CLRALL, TPS_InParseHyphen, 15, SpecialHyphen},
	},
	TPS_InHyphenDigitLookahead: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenDigitLookahead, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenNumWord, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InParseHyphen: {
		{p_isEOF, 0, A_RERUN, TPS_Base, 0, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHyphenAsciiWordPart, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWordPart, 0, documentNoSpecial},
		{p_isdigit, 0, A_PUSH, TPS_InHyphenUnsignedInt, 0, documentNoSpecial},
		{p_iseqC, '-', A_PUSH, TPS_InParseHyphenHyphen, 0, documentNoSpecial},
		{documentAlways, 0, A_RERUN, TPS_Base, 0, documentNoSpecial},
	},
	TPS_InParseHyphenHyphen: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isalnum, 0, A_BINGO | A_CLEAR, TPS_InParseHyphen, 12, documentNoSpecial},
		{p_isspecial, 0, A_BINGO | A_CLEAR, TPS_InParseHyphen, 12, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
	TPS_InHyphenWordPart: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 10, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWordPart, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenWordPart, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenNumWordPart, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_InParseHyphen, 10, documentNoSpecial},
	},
	TPS_InHyphenAsciiWordPart: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 11, documentNoSpecial},
		{p_isasclet, 0, A_NEXT, TPS_InHyphenAsciiWordPart, 0, documentNoSpecial},
		{p_isalpha, 0, A_NEXT, TPS_InHyphenWordPart, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenWordPart, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_InHyphenNumWordPart, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_InParseHyphen, 11, documentNoSpecial},
	},
	TPS_InHyphenNumWordPart: {
		{p_isEOF, 0, A_BINGO, TPS_Base, 9, documentNoSpecial},
		{p_isalnum, 0, A_NEXT, TPS_InHyphenNumWordPart, 0, documentNoSpecial},
		{p_isspecial, 0, A_NEXT, TPS_InHyphenNumWordPart, 0, documentNoSpecial},
		{documentAlways, 0, A_BINGO, TPS_InParseHyphen, 9, documentNoSpecial},
	},
	TPS_InHyphenUnsignedInt: {
		{p_isEOF, 0, A_POP, TPS_Null, 0, documentNoSpecial},
		{p_isdigit, 0, A_NEXT, TPS_Null, 0, documentNoSpecial},
		{p_isalpha, 0, A_CLEAR, TPS_InHyphenNumWordPart, 0, documentNoSpecial},
		{p_isspecial, 0, A_CLEAR, TPS_InHyphenNumWordPart, 0, documentNoSpecial},
		{documentAlways, 0, A_POP, TPS_Null, 0, documentNoSpecial},
	},
}
