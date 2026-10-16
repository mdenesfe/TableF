package mongodb

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The relaxed parser turns the JavaScript-ish literals people type in mongosh
// into extended JSON that bson.UnmarshalExtJSON accepts. It understands:
//
//   - unquoted keys ({age: 1, $or: [...], a.b: 1}) and single-quoted strings
//   - trailing commas, // and /* */ comments
//   - shell helpers: ObjectId("…"), ISODate("…"), new Date("…"), Date(),
//     NumberLong(n), NumberInt(n), NumberDecimal("…"), UUID("…"),
//     Timestamp(t, i), BinData(sub, "base64"), MinKey, MaxKey
//   - regex literals /pattern/flags
//   - true, false, null, undefined (→ null), Infinity, NaN
//
// Output is plain extended JSON text (relaxed mode, so numbers stay numbers).

type parser struct {
	src string
	pos int
}

func (p *parser) errf(format string, args ...any) error {
	return fmt.Errorf("at position %d: %s", p.pos+1, fmt.Sprintf(format, args...))
}

func (p *parser) eof() bool { return p.pos >= len(p.src) }

func (p *parser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

// skip passes whitespace and comments.
func (p *parser) skip() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			p.pos++
		case c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '/':
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		case c == '/' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '*':
			end := strings.Index(p.src[p.pos+2:], "*/")
			if end < 0 {
				p.pos = len(p.src)
			} else {
				p.pos += 2 + end + 2
			}
		default:
			return
		}
	}
}

func (p *parser) expect(c byte) error {
	p.skip()
	if p.peek() != c {
		if p.eof() {
			return p.errf("expected %q, got end of input", c)
		}
		return p.errf("expected %q, got %q", c, p.peek())
	}
	p.pos++
	return nil
}

func isIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// ident reads a JavaScript identifier.
func (p *parser) ident() string {
	start := p.pos
	if p.pos < len(p.src) && isIdentStart(p.src[p.pos]) {
		p.pos++
		for p.pos < len(p.src) && isIdentPart(p.src[p.pos]) {
			p.pos++
		}
	}
	return p.src[start:p.pos]
}

// stringLit reads a single- or double-quoted string and returns its value.
func (p *parser) stringLit() (string, error) {
	q := p.src[p.pos]
	p.pos++
	var b strings.Builder
	for {
		if p.pos >= len(p.src) {
			return "", p.errf("unterminated string")
		}
		c := p.src[p.pos]
		switch {
		case c == q:
			p.pos++
			return b.String(), nil
		case c == '\\':
			p.pos++
			if p.pos >= len(p.src) {
				return "", p.errf("unterminated string")
			}
			e := p.src[p.pos]
			p.pos++
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'v':
				b.WriteByte('\v')
			case '0':
				b.WriteByte(0)
			case '\n':
				// line continuation
			case 'x':
				if p.pos+2 > len(p.src) {
					return "", p.errf("bad \\x escape")
				}
				v, err := strconv.ParseUint(p.src[p.pos:p.pos+2], 16, 8)
				if err != nil {
					return "", p.errf("bad \\x escape")
				}
				b.WriteRune(rune(v))
				p.pos += 2
			case 'u':
				r, err := p.unicodeEscape()
				if err != nil {
					return "", err
				}
				b.WriteRune(r)
			default:
				b.WriteByte(e)
			}
		default:
			b.WriteByte(c)
			p.pos++
		}
	}
}

func (p *parser) unicodeEscape() (rune, error) {
	read4 := func() (rune, error) {
		if p.pos+4 > len(p.src) {
			return 0, p.errf("bad \\u escape")
		}
		v, err := strconv.ParseUint(p.src[p.pos:p.pos+4], 16, 16)
		if err != nil {
			return 0, p.errf("bad \\u escape")
		}
		p.pos += 4
		return rune(v), nil
	}
	if p.peek() == '{' {
		end := strings.IndexByte(p.src[p.pos:], '}')
		if end < 0 {
			return 0, p.errf("bad \\u escape")
		}
		v, err := strconv.ParseUint(p.src[p.pos+1:p.pos+end], 16, 32)
		if err != nil {
			return 0, p.errf("bad \\u escape")
		}
		p.pos += end + 1
		return rune(v), nil
	}
	r, err := read4()
	if err != nil {
		return 0, err
	}
	// surrogate pair
	if r >= 0xD800 && r < 0xDC00 && strings.HasPrefix(p.src[p.pos:], "\\u") {
		save := p.pos
		p.pos += 2
		r2, err := read4()
		if err == nil && r2 >= 0xDC00 && r2 < 0xE000 {
			return (r-0xD800)<<10 + (r2 - 0xDC00) + 0x10000, nil
		}
		p.pos = save
	}
	return r, nil
}

// number reads a numeric literal and returns it as JSON number text.
func (p *parser) number() (string, error) {
	start := p.pos
	if c := p.peek(); c == '-' || c == '+' {
		p.pos++
	}
	if strings.HasPrefix(p.src[p.pos:], "Infinity") {
		p.pos += len("Infinity")
		if p.src[start] == '-' {
			return `{"$numberDouble":"-Infinity"}`, nil
		}
		return `{"$numberDouble":"Infinity"}`, nil
	}
	if strings.HasPrefix(p.src[p.pos:], "0x") || strings.HasPrefix(p.src[p.pos:], "0X") {
		p.pos += 2
		hs := p.pos
		for p.pos < len(p.src) && isHex(p.src[p.pos]) {
			p.pos++
		}
		v, err := strconv.ParseInt(p.src[hs:p.pos], 16, 64)
		if err != nil {
			return "", p.errf("bad hex number")
		}
		if p.src[start] == '-' {
			v = -v
		}
		return strconv.FormatInt(v, 10), nil
	}
	digits := func() int {
		n := 0
		for p.pos < len(p.src) && (p.src[p.pos] >= '0' && p.src[p.pos] <= '9' || p.src[p.pos] == '_') {
			p.pos++
			n++
		}
		return n
	}
	n := digits()
	if p.peek() == '.' {
		p.pos++
		n += digits()
	}
	if n == 0 {
		return "", p.errf("bad number")
	}
	if c := p.peek(); c == 'e' || c == 'E' {
		p.pos++
		if c := p.peek(); c == '-' || c == '+' {
			p.pos++
		}
		if digits() == 0 {
			return "", p.errf("bad number exponent")
		}
	}
	s := strings.ReplaceAll(p.src[start:p.pos], "_", "")
	s = strings.TrimPrefix(s, "+")
	// JSON doesn't allow ".5", "5." or leading zeros.
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	if strings.HasPrefix(s, ".") {
		s = "0" + s
	}
	s = strings.Replace(s, ".e", ".0e", 1)
	s = strings.Replace(s, ".E", ".0E", 1)
	if strings.HasSuffix(s, ".") {
		s += "0"
	}
	for len(s) > 1 && s[0] == '0' && s[1] >= '0' && s[1] <= '9' {
		s = s[1:]
	}
	if neg {
		s = "-" + s
	}
	return s, nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// regexLit reads /pattern/flags.
func (p *parser) regexLit() (string, error) {
	p.pos++ // opening /
	var b strings.Builder
	inClass := false
	for {
		if p.pos >= len(p.src) {
			return "", p.errf("unterminated regular expression")
		}
		c := p.src[p.pos]
		if c == '\n' {
			return "", p.errf("unterminated regular expression")
		}
		if c == '\\' && p.pos+1 < len(p.src) {
			if p.src[p.pos+1] == '/' {
				b.WriteByte('/')
			} else {
				b.WriteByte(c)
				b.WriteByte(p.src[p.pos+1])
			}
			p.pos += 2
			continue
		}
		if c == '[' {
			inClass = true
		} else if c == ']' {
			inClass = false
		} else if c == '/' && !inClass {
			p.pos++
			break
		}
		b.WriteByte(c)
		p.pos++
	}
	flags := p.ident()
	for _, f := range flags {
		if !strings.ContainsRune("imxslu", f) {
			return "", p.errf("unsupported regex flag %q", f)
		}
	}
	// Extended JSON wants the options sorted.
	opts := []byte(strings.ReplaceAll(flags, "u", ""))
	for i := 1; i < len(opts); i++ {
		for j := i; j > 0 && opts[j] < opts[j-1]; j-- {
			opts[j], opts[j-1] = opts[j-1], opts[j]
		}
	}
	return fmt.Sprintf(`{"$regularExpression":{"pattern":%s,"options":%s}}`, jsonString(b.String()), jsonString(string(opts))), nil
}

// value parses one value and writes it as extended JSON.
func (p *parser) value(out *strings.Builder) error {
	p.skip()
	if p.eof() {
		return p.errf("unexpected end of input")
	}
	c := p.peek()
	switch {
	case c == '{':
		return p.object(out)
	case c == '[':
		return p.array(out)
	case c == '"' || c == '\'' || c == '`':
		s, err := p.stringLit()
		if err != nil {
			return err
		}
		out.WriteString(jsonString(s))
		return nil
	case c == '-' || c == '+' || c == '.' || (c >= '0' && c <= '9'):
		n, err := p.number()
		if err != nil {
			return err
		}
		out.WriteString(n)
		return nil
	case c == '/':
		r, err := p.regexLit()
		if err != nil {
			return err
		}
		out.WriteString(r)
		return nil
	case isIdentStart(c):
		return p.word(out)
	}
	return p.errf("unexpected %q", c)
}

func (p *parser) object(out *strings.Builder) error {
	p.pos++ // {
	out.WriteByte('{')
	first := true
	for {
		p.skip()
		if p.peek() == '}' {
			p.pos++
			out.WriteByte('}')
			return nil
		}
		if !first {
			if err := p.expect(','); err != nil {
				return err
			}
			p.skip()
			if p.peek() == '}' { // trailing comma
				continue
			}
			out.WriteByte(',')
		}
		first = false
		key, err := p.key()
		if err != nil {
			return err
		}
		if err := p.expect(':'); err != nil {
			return err
		}
		out.WriteString(jsonString(key))
		out.WriteByte(':')
		if err := p.value(out); err != nil {
			return err
		}
	}
}

func (p *parser) key() (string, error) {
	p.skip()
	if p.eof() {
		return "", p.errf("unexpected end of input, expected a key")
	}
	c := p.peek()
	switch {
	case c == '"' || c == '\'' || c == '`':
		return p.stringLit()
	case isIdentStart(c):
		// Allow dotted paths like a.b.c unquoted, as people write them.
		start := p.pos
		for p.pos < len(p.src) && (isIdentPart(p.src[p.pos]) || p.src[p.pos] == '.') {
			p.pos++
		}
		return p.src[start:p.pos], nil
	case c >= '0' && c <= '9':
		start := p.pos
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
		return p.src[start:p.pos], nil
	}
	return "", p.errf("expected a key, got %q", c)
}

func (p *parser) array(out *strings.Builder) error {
	p.pos++ // [
	out.WriteByte('[')
	first := true
	for {
		p.skip()
		if p.peek() == ']' {
			p.pos++
			out.WriteByte(']')
			return nil
		}
		if !first {
			if err := p.expect(','); err != nil {
				return err
			}
			p.skip()
			if p.peek() == ']' {
				continue
			}
			out.WriteByte(',')
		}
		first = false
		if err := p.value(out); err != nil {
			return err
		}
	}
}

// args parses a parenthesised, comma-separated argument list (the opening
// parenthesis is the current byte) and returns each argument as JSON text.
func (p *parser) args() ([]string, error) {
	if err := p.expect('('); err != nil {
		return nil, err
	}
	var out []string
	for {
		p.skip()
		if p.peek() == ')' {
			p.pos++
			return out, nil
		}
		if len(out) > 0 {
			if err := p.expect(','); err != nil {
				return nil, err
			}
			p.skip()
			if p.peek() == ')' {
				continue
			}
		}
		var b strings.Builder
		if err := p.value(&b); err != nil {
			return nil, err
		}
		out = append(out, b.String())
	}
}

// word handles keywords and shell helper calls.
func (p *parser) word(out *strings.Builder) error {
	name := p.ident()
	switch name {
	case "true", "false", "null":
		out.WriteString(name)
		return nil
	case "undefined":
		out.WriteString("null")
		return nil
	case "Infinity":
		out.WriteString(`{"$numberDouble":"Infinity"}`)
		return nil
	case "NaN":
		out.WriteString(`{"$numberDouble":"NaN"}`)
		return nil
	case "MinKey", "MaxKey":
		p.skip()
		if p.peek() == '(' {
			if _, err := p.args(); err != nil {
				return err
			}
		}
		fmt.Fprintf(out, `{"$%s":1}`, strings.ToLower(name[:1])+name[1:])
		return nil
	case "new":
		p.skip()
		name = p.ident()
		if name == "" {
			return p.errf("expected a constructor after new")
		}
	}
	p.skip()
	if p.peek() != '(' {
		return p.errf("unknown value %q (quote strings)", name)
	}
	start := p.pos
	args, err := p.args()
	if err != nil {
		return err
	}
	strArg := func(i int) (string, bool) {
		if i >= len(args) {
			return "", false
		}
		var s string
		if err := jsonUnquote(args[i], &s); err != nil {
			return "", false
		}
		return s, true
	}
	switch name {
	case "ObjectId", "ObjectID":
		s, ok := strArg(0)
		if !ok {
			if len(args) == 0 {
				return p.errAt(start, "ObjectId() needs a hex string")
			}
			return p.errAt(start, "ObjectId needs a 24-character hex string")
		}
		if b, err := hex.DecodeString(s); err != nil || len(b) != 12 {
			return p.errAt(start, "ObjectId needs a 24-character hex string, got %q", s)
		}
		fmt.Fprintf(out, `{"$oid":%s}`, jsonString(strings.ToLower(s)))
	case "ISODate", "Date":
		if len(args) == 0 {
			return p.errAt(start, "%s() needs a date string", name)
		}
		if s, ok := strArg(0); ok {
			s = isoDate(s)
			fmt.Fprintf(out, `{"$date":%s}`, jsonString(s))
		} else if _, err := strconv.ParseFloat(args[0], 64); err == nil {
			// milliseconds since the epoch
			fmt.Fprintf(out, `{"$date":{"$numberLong":"%s"}}`, strings.SplitN(args[0], ".", 2)[0])
		} else {
			return p.errAt(start, "%s needs a date string", name)
		}
	case "NumberLong", "NumberInt", "NumberDecimal", "Decimal128", "Long", "Int32", "Double":
		if len(args) != 1 {
			return p.errAt(start, "%s needs one argument", name)
		}
		s, ok := strArg(0)
		if !ok {
			s = args[0]
		}
		s = strings.TrimSpace(s)
		kind := map[string]string{
			"NumberLong": "$numberLong", "Long": "$numberLong",
			"NumberInt": "$numberInt", "Int32": "$numberInt",
			"NumberDecimal": "$numberDecimal", "Decimal128": "$numberDecimal",
			"Double": "$numberDouble",
		}[name]
		fmt.Fprintf(out, `{%q:%s}`, kind, jsonString(s))
	case "UUID":
		s, ok := strArg(0)
		if !ok {
			return p.errAt(start, "UUID needs a string")
		}
		h := strings.ReplaceAll(s, "-", "")
		b, err := hex.DecodeString(h)
		if err != nil || len(b) != 16 {
			return p.errAt(start, "UUID needs 32 hex digits, got %q", s)
		}
		fmt.Fprintf(out, `{"$binary":{"base64":%s,"subType":"04"}}`, jsonString(b64(b)))
	case "BinData":
		if len(args) != 2 {
			return p.errAt(start, "BinData needs a subtype and a base64 string")
		}
		sub, err := strconv.Atoi(args[0])
		if err != nil || sub < 0 || sub > 255 {
			return p.errAt(start, "bad BinData subtype")
		}
		s, ok := strArg(1)
		if !ok {
			return p.errAt(start, "BinData needs a base64 string")
		}
		fmt.Fprintf(out, `{"$binary":{"base64":%s,"subType":"%02x"}}`, jsonString(s), sub)
	case "Timestamp":
		if len(args) == 1 && strings.HasPrefix(args[0], "{") {
			fmt.Fprintf(out, `{"$timestamp":%s}`, args[0])
			return nil
		}
		if len(args) != 2 {
			return p.errAt(start, "Timestamp needs (seconds, increment)")
		}
		fmt.Fprintf(out, `{"$timestamp":{"t":%s,"i":%s}}`, args[0], args[1])
	case "RegExp":
		s, ok := strArg(0)
		if !ok {
			return p.errAt(start, "RegExp needs a pattern string")
		}
		flags, _ := strArg(1)
		fmt.Fprintf(out, `{"$regularExpression":{"pattern":%s,"options":%s}}`, jsonString(s), jsonString(flags))
	default:
		return p.errAt(start, "unknown function %s()", name)
	}
	return nil
}

func (p *parser) errAt(pos int, format string, args ...any) error {
	p.pos = pos
	return p.errf(format, args...)
}

// isoDate makes the common short forms of ISODate valid RFC 3339.
func isoDate(s string) string {
	s = strings.TrimSpace(s)
	switch {
	case len(s) == 10: // 2024-01-02
		return s + "T00:00:00Z"
	case len(s) == 16 && s[10] == 'T': // 2024-01-02T03:04
		return s + ":00Z"
	case len(s) == 19 && (s[10] == 'T' || s[10] == ' '): // no zone
		return s[:10] + "T" + s[11:] + "Z"
	}
	if len(s) > 19 && s[10] == ' ' {
		s = s[:10] + "T" + s[11:]
	}
	// Fractional seconds without a zone.
	if len(s) > 19 && s[19] == '.' && !strings.ContainsAny(s[19:], "Z+-") {
		return s + "Z"
	}
	return s
}

// RelaxedToJSON converts one relaxed value (usually a document) to extended JSON.
func RelaxedToJSON(src string) (string, error) {
	p := &parser{src: src}
	var b strings.Builder
	if err := p.value(&b); err != nil {
		return "", err
	}
	p.skip()
	if !p.eof() {
		return "", p.errf("unexpected %q after the value", p.peek())
	}
	return b.String(), nil
}

func jsonString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == utf8.RuneError || r == 0x2028 || r == 0x2029 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsonUnquote decodes a JSON string literal produced by jsonString.
func jsonUnquote(s string, out *string) error {
	if len(s) < 2 || s[0] != '"' {
		return errors.New("not a string")
	}
	p := &parser{src: s}
	v, err := p.stringLit()
	if err != nil || !p.eof() {
		return errors.New("not a string")
	}
	*out = v
	return nil
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }
