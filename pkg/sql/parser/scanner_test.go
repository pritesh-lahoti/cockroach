// Copyright 2015 The Cockroach Authors.
//
// Use of this software is governed by the CockroachDB Software License
// included in the /LICENSE file.

package parser

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cockroachdb/cockroach/pkg/sql/lexbase"
	"github.com/cockroachdb/cockroach/pkg/testutils"
	"github.com/cockroachdb/cockroach/pkg/util/leaktest"
	"github.com/cockroachdb/errors"
)

func TestScanner(t *testing.T) {
	testData := []struct {
		sql      string
		expected []int
	}{
		{``, nil},
		{` `, nil},
		{` /* hello */`, nil},
		{`.`, []int{'.'}},
		{`..`, []int{DOT_DOT}},
		{`!`, []int{'!'}},
		{`!=`, []int{NOT_EQUALS}},
		{`<`, []int{'<'}},
		{`<>`, []int{NOT_EQUALS}},
		{`<=`, []int{LESS_EQUALS}},
		{`<<`, []int{LSHIFT}},
		{`<<=`, []int{INET_CONTAINED_BY_OR_EQUALS}},
		{`>`, []int{'>'}},
		{`>=`, []int{GREATER_EQUALS}},
		{`>>`, []int{RSHIFT}},
		{`>>=`, []int{INET_CONTAINS_OR_EQUALS}},
		{`=`, []int{'='}},
		{`:`, []int{':'}},
		{`::`, []int{TYPECAST}},
		{`:: :`, []int{TYPECAST, ':'}},
		{`(`, []int{'('}},
		{`)`, []int{')'}},
		{`[`, []int{'['}},
		{`]`, []int{']'}},
		{`,`, []int{','}},
		{`;`, []int{';'}},
		{`+`, []int{'+'}},
		{`-`, []int{'-'}},
		{`*`, []int{'*'}},
		{`/`, []int{'/'}},
		{`//`, []int{FLOORDIV}},
		{`%`, []int{'%'}},
		{`^`, []int{'^'}},
		{`$`, []int{'$'}},
		{`&`, []int{'&'}},
		{`&&`, []int{AND_AND}},
		{`|`, []int{'|'}},
		{`||`, []int{CONCAT}},
		{`|/`, []int{SQRT}},
		{`||/`, []int{CBRT}},
		{`#`, []int{'#'}},
		{`~`, []int{'~'}},
		{`!~`, []int{NOT_REGMATCH}},
		{`~*`, []int{REGIMATCH}},
		{`!~*`, []int{NOT_REGIMATCH}},
		{`$1`, []int{PLACEHOLDER}},
		{`$a`, []int{'$', IDENT}},
		{`a`, []int{IDENT}},
		{`foo + bar`, []int{IDENT, '+', IDENT}},
		{`select a from b`, []int{SELECT, IDENT, FROM, IDENT}},
		{`"a" "b"`, []int{IDENT, IDENT}},
		{`'a'`, []int{SCONST}},
		{`$$a$$`, []int{SCONST}},
		{`$a$b$a$`, []int{SCONST}},
		{`$a$b b$a$`, []int{SCONST}},
		{`$a$ $a$`, []int{SCONST}},
		{`$a$1$b$2$b$3$a$`, []int{SCONST}},
		{`$a$1$b$2$b3$a$`, []int{SCONST}},
		{`$a$1$$3$a$`, []int{SCONST}},
		{`$a$1$$3$a$`, []int{SCONST}},
		{`$a$1$3$a$`, []int{SCONST}},
		{`$ab$1$a$ab$`, []int{SCONST}},
		{`$ab1$ab$ab1$`, []int{SCONST}},
		{`$ab1$ab12$ab1$`, []int{SCONST}},
		{`$$~!@#$%^&*()_+:",./<>?;'$$`, []int{SCONST}},
		{`$$hello
world$$`, []int{SCONST}},
		{`b'a'`, []int{BCONST}},
		{`b'\xff'`, []int{BCONST}},
		{`B'10101'`, []int{BITCONST}},
		{`e'a'`, []int{SCONST}},
		{`E'a'`, []int{SCONST}},
		{`NOT`, []int{NOT}},
		{`NOT BETWEEN`, []int{NOT, BETWEEN}},
		{`NOT IN`, []int{NOT, IN}},
		{`NOT SIMILAR`, []int{NOT, SIMILAR}},
		{`WITH`, []int{WITH}},
		{`WITH TIME`, []int{WITH, TIME}},
		{`WITH ORDINALITY`, []int{WITH, ORDINALITY}},
		{`1`, []int{ICONST}},
		{`0xa`, []int{ICONST}},
		{`x'2F'`, []int{BCONST}},
		{`X'2F'`, []int{BCONST}},
		{`1.0`, []int{FCONST}},
		{`1.0e1`, []int{FCONST}},
		{`1e+1`, []int{FCONST}},
		{`1e-1`, []int{FCONST}},
	}
	for i, d := range testData {
		s := makeSQLScanner(d.sql)
		var tokens []int
		for {
			var lval = &sqlSymType{}
			s.Scan(lval)
			if lval.ID() == 0 {
				break
			}
			tokens = append(tokens, int(lval.ID()))
		}

		if !reflect.DeepEqual(d.expected, tokens) {
			t.Errorf("%d: %q: expected %d, but found %d", i, d.sql, d.expected, tokens)
		}
	}
}

func TestScanComment(t *testing.T) {
	testData := []struct {
		sql       string
		err       string
		remainder string
	}{
		{`/* hello */world`, "", "world"},
		{`/* hello */
world`, "", "\nworld"},
		{`/* hello */*`, "", "*"},
		{`/* /* deeply /* nested */ comment */ */`, "", ""},
		{`/* /* */* */`, "", ""},
		{`/* /* /*/ */ */ */`, "", ""},
		{`/* multi line
comment */`, "", ""},
		{`-- hello world
foo`, "", "\nfoo"},
		{`/*`, "unterminated comment", ""},
		{`/*/`, "unterminated comment", ""},
		{`/* /* */`, "unterminated comment", ""},
	}
	for i, d := range testData {
		s := makeSQLScanner(d.sql)
		var lval = &sqlSymType{}
		present, ok := s.ScanComment(lval)
		if d.err == "" && (!present || !ok) {
			t.Fatalf("%d: expected success, but found %s", i, lval.Str())
		} else if d.err != "" && (present || ok || d.err != lval.Str()) {
			t.Fatalf("%d: expected %s, but found %s", i, d.err, lval.Str())
		}
		if r := s.In()[s.Pos():]; d.remainder != r {
			t.Fatalf("%d: expected '%s', but found '%s'", i, d.remainder, r)
		}
	}
}

func TestScanKeyword(t *testing.T) {
	for _, kwName := range lexbase.KeywordNames {
		s := makeSQLScanner(kwName)
		var lval = &sqlSymType{}
		s.Scan(lval)
		if id := lexbase.GetKeywordID(kwName); id != lval.ID() {
			t.Errorf("%s: expected %d, but found %d", kwName, id, lval.ID())
		}
	}
}

func TestScanNumber(t *testing.T) {
	testData := []struct {
		sql      string
		expected string
		id       int
	}{
		{`0`, `0`, ICONST},
		{`000`, `0`, ICONST},
		{`1`, `1`, ICONST},
		{`0x1`, `0x1`, ICONST},
		{`0X2`, `0X2`, ICONST},
		{`0xff`, `0xff`, ICONST},
		{`0xff.`, `0xff`, ICONST},
		{`12345`, `12345`, ICONST},
		{`08`, `8`, ICONST},
		{`0011`, `11`, ICONST},
		{`1.`, `1.`, FCONST},
		{`.1`, `.1`, FCONST},
		{`1..2`, `1`, ICONST},
		{`1.2`, `1.2`, FCONST},
		{`1.2e3`, `1.2e3`, FCONST},
		{`1e3`, `1e3`, FCONST},
		{`1e3.4`, `1e3`, FCONST},
		{`.1e3.4`, `.1e3`, FCONST},
		{`1e-3`, `1e-3`, FCONST},
		{`1e-3-`, `1e-3`, FCONST},
		{`1e+3`, `1e+3`, FCONST},
		{`1e+3+`, `1e+3`, FCONST},
		{`9223372036854775809`, `9223372036854775809`, ICONST},
	}
	for _, d := range testData {
		s := makeSQLScanner(d.sql)
		var lval = &sqlSymType{}
		s.Scan(lval)
		if d.id != int(lval.ID()) {
			t.Errorf("%s: expected %d, but found %d", d.sql, d.id, lval.ID())
		}
		if d.expected != lval.Str() {
			t.Errorf("%s: expected %s, but found %s", d.sql, d.expected, lval.Str())
		}
	}
}

func TestScanPlaceholder(t *testing.T) {
	testData := []struct {
		sql      string
		expected string
	}{
		{`$1`, "1"},
		{`$1a`, "1"},
		{`$123`, "123"},
	}
	for _, d := range testData {
		s := makeSQLScanner(d.sql)
		var lval = &sqlSymType{}
		s.Scan(lval)
		if lval.ID() != PLACEHOLDER {
			t.Errorf("%s: expected %d, but found %d", d.sql, PLACEHOLDER, lval.ID())
		}
		if d.expected != lval.Str() {
			t.Errorf("%s: expected %s, but found %s", d.sql, d.expected, lval.Str())
		}
	}
}

func TestScanString(t *testing.T) {
	testData := []struct {
		sql      string
		expected string
	}{
		{`"a"`, `a`},
		{`'a'`, `a`},
		{`"a""b"`, `a"b`},
		{`"a''b"`, `a''b`},
		{`'a""b'`, `a""b`},
		{`'a''b'`, `a'b`},
		{`"a" "b"`, `a`},
		{"\"a\"\n\"b\"", `a`},
		{`'a' 'b'`, `a`},
		{`'\n'`, `\n`},
		{`e'\n'`, "\n"},
		{`'\\n'`, `\\n`},
		{`'\'''`, `\'`},
		{`'\0\'`, `\0\`},
		{`"a"
	"b"`, `a`},
		{`"a"
	'b'`, `a`},
		{`'a'
	'b'`, `ab`},
		{`'a'
	"b"`, `a`},
		{`e'\"'`, `"`}, // redundant escape
		{"'\n\\'", "\n\\"},
		{`e'foo\"\'\\\a\b\f\n\r\t\vbar'`,
			strings.Join([]string{`foo"'\`, "\a\b\f\n\r\t\v", `bar`}, "")},
		{`e'\\0'`, `\0`},
		{`'\0'`, `\0`},
		{`e'\x'`, `invalid syntax`},
		{`e'\x4'`, `invalid syntax`},
		{`e'\xg'`, `invalid syntax`},
		{`e'\X4'`, `invalid syntax`},
		{`e'\x41'`, `A`},
		{`e'\X41B'`, `AB`},
		{`e'\0'`, `invalid syntax`},
		{`e'\00'`, `invalid syntax`},
		{`e'\009'`, `invalid syntax`},
		{`e'\101'`, `A`},
		{`e'\101B'`, `AB`},
		{`e'\xff'`, `invalid UTF-8 byte sequence`},
		{`e'\u1'`, `invalid syntax`},
		{`e'\U123'`, `invalid syntax`},
		{`e'\u0041'`, `A`},
		{`e'\u0041B'`, `AB`},
		{`e'\U00000041'`, `A`},
		{`e'\U00000041B'`, `AB`},
		{`"''"`, `''`},
		{`'""'''`, `""'`},
		{`""""`, `"`},
		{`''''`, `'`},
		{`''''''`, `''`},
		{`'hello
world'`, `hello
world`},
		{`x'666f6f'`, `foo`},
		{`X'626172'`, `bar`},
		{`X'FF'`, "\xff"},
		{`B'100101'`, "100101"},
		{`$$a$$`, "a"},
		{`$a$b$a$`, "b"},
		{`$a$b b$a$`, "b b"},
		{`$a$ $a$`, " "},
		{`$a$1$b$2$b$3$a$`, "1$b$2$b$3"},
		{`$a$1$b$2$b3$a$`, "1$b$2$b3"},
		{`$a$1$$3$a$`, "1$$3"},
		{`$a$1$3$a$`, "1$3"},
		{`$ab$1$a$ab$`, "1$a"},
		{`$ab1$ab$ab1$`, "ab"},
		{`$ab1$ab12$ab1$`, "ab12"},
		{`$$~!@#$%^&*()_+:",./<>?;'$$`, "~!@#$%^&*()_+:\",./<>?;'"},
		{`$$hello
world$$`, `hello
world`},
		{`$$a`, `unterminated string`},
		{`$a$a$$`, `unterminated string`},
	}
	for _, d := range testData {
		s := makeSQLScanner(d.sql)
		var lval = &sqlSymType{}
		s.Scan(lval)
		if d.expected != lval.Str() {
			t.Errorf("%s: expected %q, but found %q", d.sql, d.expected, lval.Str())
		}
	}
}

func TestScanError(t *testing.T) {
	testData := []struct {
		sql string
		err string
	}{
		{`1e`, "invalid floating point literal"},
		{`1e-`, "invalid floating point literal"},
		{`1e+`, "invalid floating point literal"},
		{`0x`, "invalid hexadecimal numeric literal"},
		{`1x`, "invalid hexadecimal numeric literal"},
		{`1.x`, "invalid hexadecimal numeric literal"},
		{`1.0x`, "invalid hexadecimal numeric literal"},
		{`0x0x`, "invalid hexadecimal numeric literal"},
		{`00x0x`, "invalid hexadecimal numeric literal"},
		{`x'zzz'`, "invalid hexadecimal bytes literal"},
		{`X'zzz'`, "invalid hexadecimal bytes literal"},
		{`x'beef\x41'`, "invalid hexadecimal bytes literal"},
		{`X'beef\x41\x41'`, "invalid hexadecimal bytes literal"},
		{`x'a'`, "invalid hexadecimal bytes literal"},
		{`$0`, "placeholder index must be between 1 and 65536"},
		{`$9223372036854775809`, "placeholder index must be between 1 and 65536"},
		{`B'123'`, `"2" is not a valid binary digit`},
		{`123foo`, "trailing junk after numeric literal at or near \"123f\""},
		{`1.23foo`, "trailing junk after numeric literal at or near \"1.23f\""},
		{`0x0afoo`, "trailing junk after numeric literal at or near \"0x0afo\""},
	}
	for _, d := range testData {
		s := makeSQLScanner(d.sql)
		var lval = &sqlSymType{}
		s.Scan(lval)
		if lval.ID() != ERROR {
			t.Errorf("%s: expected ERROR, but found %d", d.sql, lval.ID())
		}
		if !testutils.IsError(errors.Newf("%s", lval.Str()), d.err) {
			t.Errorf("%s: expected %s, but found %v", d.sql, d.err, lval.Str())
		}
	}
}

func TestSplitFirstStatement(t *testing.T) {
	defer leaktest.AfterTest(t)()
	tests := []struct {
		s, res string
	}{
		{
			s:   "SELECT 1",
			res: "",
		},
		{
			s:   "SELECT 1;",
			res: "SELECT 1;",
		},
		{
			s:   "SELECT 1  /* comment */ ;",
			res: "SELECT 1  /* comment */ ;",
		},
		{
			s:   "SELECT 1;SELECT 2",
			res: "SELECT 1;",
		},
		{
			s:   "SELECT 1  /* comment */ ;SELECT 2",
			res: "SELECT 1  /* comment */ ;",
		},
		{
			s:   "SELECT 1  /* comment */ ; /* comment */ SELECT 2",
			res: "SELECT 1  /* comment */ ;",
		},
		{
			s:   ";",
			res: ";",
		},
		{
			s:   "SELECT ';'",
			res: "",
		},
	}

	for i, tc := range tests {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			pos, ok := SplitFirstStatement(tc.s)
			if !ok && pos != 0 {
				t.Fatalf("!ok but nonzero pos")
			}
			if tc.res != tc.s[:pos] {
				t.Errorf("expected `%s` but got `%s`", tc.res, tc.s[:pos])
			}
		})
	}
}
