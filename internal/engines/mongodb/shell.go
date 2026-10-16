package mongodb

import (
	"fmt"
	"strings"
)

// splitScript breaks an editor script into mongosh commands. A command ends at
// a top-level ";" or at a newline when the next line starts a new command
// (db…, show …, use …, or a bare { document }). Strings, regex literals,
// comments and anything inside (), [] or {} never split.
func splitScript(script string) []string {
	var out []string
	var cur strings.Builder
	depth := 0
	flush := func() {
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, s)
		}
		cur.Reset()
	}
	// prev is the last significant byte, to tell a regex from division.
	var prev byte
	n := len(script)
	for i := 0; i < n; i++ {
		c := script[i]
		switch {
		case c == '"' || c == '\'' || c == '`':
			j := i + 1
			for j < n && script[j] != c {
				if script[j] == '\\' {
					j++
				}
				j++
			}
			if j >= n {
				j = n - 1
			}
			cur.WriteString(script[i : j+1])
			i = j
			prev = c
		case c == '/' && i+1 < n && script[i+1] == '/':
			for i < n && script[i] != '\n' {
				i++
			}
			i-- // let the newline be handled
		case c == '/' && i+1 < n && script[i+1] == '*':
			end := strings.Index(script[i+2:], "*/")
			if end < 0 {
				i = n
			} else {
				i += 2 + end + 1
			}
			cur.WriteByte(' ')
		case c == '/' && regexAllowed(prev):
			j := i + 1
			inClass := false
			for j < n && script[j] != '\n' {
				if script[j] == '\\' {
					j += 2
					continue
				}
				if script[j] == '[' {
					inClass = true
				} else if script[j] == ']' {
					inClass = false
				} else if script[j] == '/' && !inClass {
					break
				}
				j++
			}
			if j >= n {
				j = n - 1
			}
			cur.WriteString(script[i : j+1])
			i = j
			prev = '/'
		case c == '(' || c == '[' || c == '{':
			depth++
			cur.WriteByte(c)
			prev = c
		case c == ')' || c == ']' || c == '}':
			if depth > 0 {
				depth--
			}
			cur.WriteByte(c)
			prev = c
		case c == ';' && depth == 0:
			flush()
			prev = 0
		case c == '\n' && depth == 0:
			rest := strings.TrimLeft(script[i+1:], " \t\r")
			if startsCommand(rest) && strings.TrimSpace(cur.String()) != "" {
				flush()
				prev = 0
			} else {
				cur.WriteByte(c)
			}
		default:
			cur.WriteByte(c)
			if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
				prev = c
			}
		}
	}
	flush()
	return out
}

func regexAllowed(prev byte) bool {
	switch prev {
	case 0, '(', '[', '{', ',', ':', '=', '!', '&', '|', '?', ';':
		return true
	}
	return false
}

func startsCommand(s string) bool {
	return strings.HasPrefix(s, "db.") || strings.HasPrefix(s, "db[") ||
		strings.HasPrefix(s, "show ") || strings.HasPrefix(s, "use ") ||
		strings.HasPrefix(s, "{")
}

// shellCall is a parsed mongosh command.
type shellCall struct {
	// Kind is "collection" (db.<coll>.<method>), "db" (db.<method>),
	// "command" (a bare document) or "show".
	Kind   string
	Coll   string
	Method string
	Args   []string // extended JSON per argument
	Chain  []chainCall
}

type chainCall struct {
	Method string
	Args   []string
}

// parseShell parses one mongosh-style command.
func parseShell(cmd string) (shellCall, error) {
	cmd = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(cmd), ";"))
	p := &parser{src: cmd}
	p.skip()
	if p.peek() == '{' {
		js, err := RelaxedToJSON(p.src[p.pos:])
		if err != nil {
			return shellCall{}, err
		}
		return shellCall{Kind: "command", Args: []string{js}}, nil
	}
	if f := strings.Fields(cmd); len(f) > 0 && (f[0] == "show" || f[0] == "use") {
		return shellCall{Kind: "show", Method: strings.Join(f, " ")}, nil
	}
	if p.ident() != "db" {
		return shellCall{}, fmt.Errorf("commands start with db. — e.g. db.users.find({age: {$gt: 30}})")
	}
	var call shellCall
	// db.<name> or db["name"] or db.getCollection("name")
	name, err := p.member()
	if err != nil {
		return shellCall{}, err
	}
	p.skip()
	if p.peek() == '(' {
		args, err := p.args()
		if err != nil {
			return shellCall{}, err
		}
		if name != "getCollection" && name != "getSiblingDB" {
			call = shellCall{Kind: "db", Method: name, Args: args}
			if err := p.chain(&call); err != nil {
				return shellCall{}, err
			}
			return call, nil
		}
		if name == "getSiblingDB" {
			return shellCall{}, fmt.Errorf("getSiblingDB isn't supported; switch the database in the sidebar")
		}
		if len(args) != 1 {
			return shellCall{}, fmt.Errorf("getCollection needs a name")
		}
		if err := jsonUnquote(args[0], &name); err != nil {
			return shellCall{}, fmt.Errorf("getCollection needs a string name")
		}
	}
	call.Kind = "collection"
	call.Coll = name
	method, err := p.member()
	if err != nil {
		return shellCall{}, fmt.Errorf("expected a method after %s, e.g. .find()", collRef(name))
	}
	// db.a.b.find(): collection names may contain dots.
	for {
		p.skip()
		if p.peek() == '(' {
			break
		}
		next, err := p.member()
		if err != nil {
			return shellCall{}, fmt.Errorf("expected ( after %s", method)
		}
		call.Coll += "." + method
		method = next
	}
	call.Method = method
	if call.Args, err = p.args(); err != nil {
		return shellCall{}, err
	}
	if err := p.chain(&call); err != nil {
		return shellCall{}, err
	}
	return call, nil
}

// member reads .name or ["name"].
func (p *parser) member() (string, error) {
	p.skip()
	switch p.peek() {
	case '.':
		p.pos++
		p.skip()
		if n := p.ident(); n != "" {
			return n, nil
		}
		return "", p.errf("expected a name after .")
	case '[':
		p.pos++
		p.skip()
		if c := p.peek(); c != '"' && c != '\'' {
			return "", p.errf("expected a quoted name")
		}
		s, err := p.stringLit()
		if err != nil {
			return "", err
		}
		if err := p.expect(']'); err != nil {
			return "", err
		}
		return s, nil
	}
	if p.eof() {
		return "", p.errf("unexpected end of command")
	}
	return "", p.errf("unexpected %q", p.peek())
}

// chain reads trailing .method(args) calls.
func (p *parser) chain(call *shellCall) error {
	for {
		p.skip()
		if p.eof() {
			return nil
		}
		if p.peek() != '.' {
			return p.errf("unexpected %q", p.peek())
		}
		m, err := p.member()
		if err != nil {
			return err
		}
		args, err := p.args()
		if err != nil {
			return err
		}
		call.Chain = append(call.Chain, chainCall{Method: m, Args: args})
	}
}
