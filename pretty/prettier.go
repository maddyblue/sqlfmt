package pretty

import "fmt"

// :<> concat
// :<|> union

type Doc interface {
	isDoc()
	String() string
}

func (concat) isDoc() {}
func (union) isDoc()  {}
func (nest) isDoc()   {}
func (text) isDoc()   {}
func (_nil) isDoc()   {}
func (line) isDoc()   {}
func (textX) isDoc()  {}
func (lineX) isDoc()  {}

func (d text) String() string   { return fmt.Sprintf("(%q)", string(d)) }
func (line) String() string     { return "LINE" }
func (_nil) String() string     { return "NIL" }
func (d concat) String() string { return fmt.Sprintf("(%s <> %s)", d.a, d.b) }
func (d nest) String() string   { return fmt.Sprintf("(NEST %d %s)", d.n, d.d) }
func (d union) String() string  { return fmt.Sprintf("(%s :<|> %s)", d.a, d.b) }
func (d textX) String() string  { return fmt.Sprintf("(%s TEXTX %s)", d.s, d.d) }
func (d lineX) String() string  { return fmt.Sprintf("(%d LINEX %s)", d.i, d.d) }

type group Doc

func Group(d Doc) Doc {
	return union{flatten(d), d}
}

type text string

func Text(s string) Doc {
	return text(s)
}

type line struct{}

var Line line

type _nil struct{}

var Nil _nil

type nest struct {
	n int
	d Doc
}

func Nest(n int, d Doc) Doc {
	return nest{n, d}
}

type concat struct {
	a, b Doc
}

func Concat(a, b Doc) Doc {
	if a == nil {
		a = Nil
	}
	if b == nil {
		b = Nil
	}
	if a == Nil {
		return b
	}
	if b == Nil {
		return a
	}
	return concat{a, b}
}

func Join(s string, d ...Doc) Doc {
	switch len(d) {
	case 0:
		return Nil
	case 1:
		return d[0]
	default:
		return Fold(Concat, d[0], Text(s), Line, Join(s, d[1:]...))
	}
}

func ConcatLine(a, b Doc) Doc {
	return Concat(
		a,
		Concat(
			Line,
			b,
		),
	)
}

func JoinGroup(name, divider string, d ...Doc) Doc {
	return Group(Concat(
		Text(name),
		Nest(1, Concat(
			Line,
			Group(Join(divider, d...)),
		)),
	))
}

func Fold(f func(a, b Doc) Doc, d ...Doc) Doc {
	switch len(d) {
	case 0:
		return Nil
	case 1:
		return d[0]
	default:
		return f(d[0], Fold(f, d[1:]...))
	}
}

func Bracket(l string, x Doc, r string) Doc {
	// The unions with Text("") (instead of just Line) prevent a space being
	// printed when lines are concatenated.
	return Group(Fold(Concat,
		Text(l),
		Nest(2, Concat(union{Text(""), Line}, x)),
		union{Text(""), Line},
		Text(r),
	))
}

type union struct {
	a, b Doc
}

type textX struct {
	s string
	d Doc
}

type lineX struct {
	i int
	d Doc
}

func flatten(d Doc) Doc {
	if d == Nil {
		return Nil
	}
	if t, ok := d.(concat); ok {
		return Concat(flatten(t.a), flatten(t.b))
	}
	if t, ok := d.(nest); ok {
		return Nest(t.n, flatten(t.d))
	}
	if _, ok := d.(text); ok {
		return d
	}
	if d == Line {
		return Text(" ")
	}
	if t, ok := d.(union); ok {
		return flatten(t.a)
	}
	panic(fmt.Errorf("unknown type: %T", d))
}
