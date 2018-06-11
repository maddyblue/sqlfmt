package pretty

import "fmt"

// :<> concat
// :<|> union

type Doc interface {
	isDoc()
}

func (concat) isDoc() {}
func (union) isDoc()  {}
func (nest) isDoc()   {}
func (text) isDoc()   {}
func (_nil) isDoc()   {}
func (line) isDoc()   {}
func (textX) isDoc()  {}
func (lineX) isDoc()  {}

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
	return Group(Fold(Concat,
		Text(l),
		Nest(2, Concat(Line, x)),
		Line,
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
