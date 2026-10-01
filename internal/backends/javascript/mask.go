package javascript

import (
	"path/filepath"
	"strings"
)

// maxJSXFailures bounds speculative JSX scans per file. A failed scan rewinds
// and rescans its region as code, so the budget keeps pathological files (for
// example many type-level `<T>` generics in TSX) linear.
const maxJSXFailures = 64

// expressionKeywords are words after which an expression, and therefore a
// regular expression literal or JSX element, may begin.
var expressionKeywords = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "throw": true, "case": true, "do": true, "else": true,
	"yield": true, "await": true,
}

// jsxCapable reports whether a file may contain JSX. TypeScript's `.ts`
// family cannot, and there `<T>value` is a type assertion.
func jsxCapable(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ts", ".mts", ".cts":
		return false
	default:
		return true
	}
}

// maskSource blanks comments, string and template text, regular expression
// literals, and JSX text so pattern-based extraction only sees code. Template
// `${}` expressions, JSX attribute expressions, and JSX children expressions
// stay visible. Byte offsets and line breaks are preserved.
// Operations (Pure): transforms explicit source text.
func maskSource(source string, jsx bool) string {
	scanner := &maskScanner{src: source, out: []byte(source), jsx: jsx}

	start := 0
	if strings.HasPrefix(source, "#!") {
		start = scanner.maskLine(0)
	}
	scanner.scanCode(start, false)

	return string(scanner.out)
}

type maskScanner struct {
	src         string
	out         []byte
	jsx         bool
	jsxFailures int
}

func (scanner *maskScanner) mask(index int) {
	if scanner.out[index] != '\n' {
		scanner.out[index] = ' '
	}
}

func (scanner *maskScanner) maskRange(start, end int) {
	for index := start; index < end; index++ {
		scanner.mask(index)
	}
}

func (scanner *maskScanner) restore(start, end int) {
	copy(scanner.out[start:end], scanner.src[start:end])
}

func (scanner *maskScanner) at(index int) byte {
	if index < 0 || index >= len(scanner.src) {
		return 0
	}
	return scanner.src[index]
}

// scanCode scans JavaScript code from index. With stopAtBrace it returns the
// index of the unmatched `}` that closes a template or JSX expression;
// otherwise, or when the source ends first, it returns len(src).
func (scanner *maskScanner) scanCode(index int, stopAtBrace bool) int {
	source := scanner.src
	depth := 0
	expressionMayStart := true

	for index < len(source) {
		character := source[index]
		next := scanner.at(index + 1)

		switch {
		case character == '/' && next == '/':
			index = scanner.maskLine(index)
		case character == '/' && next == '*':
			index = scanner.maskBlockComment(index)
		case character == '\'' || character == '"':
			index = scanner.maskString(index)
			expressionMayStart = false
		case character == '`':
			index = scanner.scanTemplate(index)
			expressionMayStart = false
		case character == '/' && expressionMayStart:
			if end, ok := scanner.maskRegex(index); ok {
				index = end
				expressionMayStart = false
				continue
			}
			index++
		case character == '<' && expressionMayStart && scanner.jsx && jsxNameStart(next):
			end, ok := scanner.scanElement(index)
			if ok {
				index = end
				expressionMayStart = false
				continue
			}

			// Not an element after all: rewind and treat `<` as an operator.
			scanner.restore(index, end)
			scanner.jsxFailures++
			scanner.jsx = scanner.jsxFailures < maxJSXFailures
			index++
		case character == '{':
			depth++
			expressionMayStart = true
			index++
		case character == '}':
			if depth == 0 && stopAtBrace {
				return index
			}
			depth = max(0, depth-1)
			expressionMayStart = true
			index++
		case (character == '+' || character == '-') && next == character:
			// Increment and decrement keep the operand state: `count++ / total`.
			index += 2
		case identifierByte(character):
			end := index
			for end < len(source) && (identifierByte(source[end]) || isDigit(source[end])) {
				end++
			}
			expressionMayStart = expressionKeywords[source[index:end]] && scanner.at(index-1) != '.'
			index = end
		case isDigit(character):
			for index < len(source) && (identifierByte(source[index]) || isDigit(source[index]) || source[index] == '.') {
				index++
			}
			expressionMayStart = false
		case character == ')' || character == ']':
			expressionMayStart = false
			index++
		case character == ' ' || character == '\t' || character == '\n' || character == '\r':
			index++
		default:
			expressionMayStart = true
			index++
		}
	}
	return len(source)
}

// maskLine blanks a line comment and returns the index of its line break.
func (scanner *maskScanner) maskLine(index int) int {
	for index < len(scanner.src) && scanner.src[index] != '\n' {
		scanner.mask(index)
		index++
	}
	return index
}

func (scanner *maskScanner) maskBlockComment(index int) int {
	end := strings.Index(scanner.src[index+2:], "*/")
	if end < 0 {
		scanner.maskRange(index, len(scanner.src))
		return len(scanner.src)
	}

	end += index + 2 + len("*/")
	scanner.maskRange(index, end)
	return end
}

// maskString blanks a quoted string. An unescaped line break ends it so an
// unmatched quote cannot hide the rest of the file.
func (scanner *maskScanner) maskString(index int) int {
	source := scanner.src
	quote := source[index]
	scanner.mask(index)
	index++

	for index < len(source) {
		switch source[index] {
		case '\\':
			scanner.maskRange(index, min(index+2, len(source)))
			index += 2
		case quote:
			scanner.mask(index)
			return index + 1
		case '\n':
			return index
		default:
			scanner.mask(index)
			index++
		}
	}
	return len(source)
}

// scanTemplate blanks template text while scanning `${}` expressions as code.
func (scanner *maskScanner) scanTemplate(index int) int {
	source := scanner.src
	scanner.mask(index)
	index++

	for index < len(source) {
		switch {
		case source[index] == '\\':
			scanner.maskRange(index, min(index+2, len(source)))
			index += 2
		case source[index] == '`':
			scanner.mask(index)
			return index + 1
		case source[index] == '$' && scanner.at(index+1) == '{':
			scanner.maskRange(index, index+2)
			end := scanner.scanCode(index+2, true)
			if end >= len(source) {
				return len(source)
			}
			scanner.mask(end)
			index = end + 1
		default:
			scanner.mask(index)
			index++
		}
	}
	return len(source)
}

// maskRegex blanks a regular expression literal and its flags. It reports
// false, leaving the slash as division, when no closing slash is on the line.
func (scanner *maskScanner) maskRegex(index int) (int, bool) {
	source := scanner.src
	inClass := false

	for end := index + 1; end < len(source); end++ {
		switch source[end] {
		case '\\':
			end++
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '\n':
			return index, false
		case '/':
			if inClass {
				continue
			}
			end++
			for end < len(source) && identifierByte(source[end]) {
				end++
			}
			scanner.maskRange(index, end)
			return end, true
		}
	}
	return index, false
}

// scanElement scans a JSX element or fragment that starts at `<`. On failure
// it returns the furthest index it may have masked so the caller can restore it.
func (scanner *maskScanner) scanElement(start int) (int, bool) {
	source := scanner.src
	if scanner.at(start+1) == '>' {
		return scanner.scanChildren(start+2, "")
	}

	nameEnd := start + 1
	for nameEnd < len(source) && jsxNameByte(source[nameEnd]) {
		nameEnd++
	}
	name := source[start+1 : nameEnd]

	// `<T,>` and `<T extends U>` are generic arrow parameters, not elements.
	afterName := strings.TrimLeft(source[nameEnd:min(len(source), nameEnd+16)], " \t\r\n")
	if strings.HasPrefix(afterName, ",") || strings.HasPrefix(afterName, "extends ") {
		return start, false
	}

	typeArgumentDepth := 0
	for index := nameEnd; index < len(source); {
		character := source[index]
		next := scanner.at(index + 1)

		switch {
		case character == '/' && next == '>' && typeArgumentDepth == 0:
			return index + 2, true
		case character == '/' && next == '/':
			index = scanner.maskLine(index)
		case character == '/' && next == '*':
			index = scanner.maskBlockComment(index)
		case character == '<':
			typeArgumentDepth++
			index++
		case character == '>' && typeArgumentDepth > 0:
			typeArgumentDepth--
			index++
		case character == '>':
			return scanner.scanChildren(index+1, name)
		case character == '"' || character == '\'':
			end := strings.IndexByte(source[index+1:], character)
			if end < 0 {
				return len(source), false
			}
			end += index + 2
			scanner.maskRange(index, end)
			index = end
		case character == '{':
			end := scanner.scanCode(index+1, true)
			if end >= len(source) {
				return len(source), false
			}
			index = end + 1
		case character == ';' || character == '(' || character == ')':
			// These cannot appear between attributes, so this is not JSX.
			return index, false
		default:
			index++
		}
	}
	return len(source), false
}

// scanChildren blanks JSX text until the closing tag that matches name.
func (scanner *maskScanner) scanChildren(index int, name string) (int, bool) {
	source := scanner.src
	for index < len(source) {
		switch {
		case source[index] == '{':
			end := scanner.scanCode(index+1, true)
			if end >= len(source) {
				return len(source), false
			}
			index = end + 1
		case source[index] == '<' && scanner.at(index+1) == '/':
			closeEnd := strings.IndexByte(source[index:], '>')
			if closeEnd < 0 || strings.TrimSpace(source[index+2:index+closeEnd]) != name {
				return index, false
			}
			return index + closeEnd + 1, true
		case source[index] == '<':
			end, ok := scanner.scanElement(index)
			if !ok {
				return end, false
			}
			index = end
		default:
			scanner.mask(index)
			index++
		}
	}
	return len(source), false
}

func identifierByte(character byte) bool {
	return character == '_' || character == '$' || (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character >= 0x80
}

func isDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func jsxNameStart(character byte) bool {
	return identifierByte(character) || character == '>'
}

func jsxNameByte(character byte) bool {
	return identifierByte(character) || isDigit(character) || character == '.' || character == ':' || character == '-'
}
