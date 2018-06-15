package pretty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
)

func Pretty(ctx context.Context, d Doc, w io.Writer, n int) error {
	b := best(ctx, n, 0, d)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return layout(w, b)
}

func PrettyString(ctx context.Context, d Doc, n int) (string, error) {
	var sb strings.Builder
	err := Pretty(ctx, d, &sb, n)
	return sb.String(), err
}

// w is the max line width, k is the current col.
func best(ctx context.Context, w, k int, x Doc) Doc {
	b := beExec{
		w:     w,
		done:  ctx.Done(),
		cache: make(map[string]Doc),
	}
	return b.be(k, IDoc{0, x})
}

type IDoc struct {
	i int
	d Doc
}

func (i IDoc) String() string {
	return fmt.Sprintf("{%d: %s}", i.i, i.d)
}

type beExec struct {
	w     int
	done  <-chan struct{}
	cache map[string]Doc
}

func (b beExec) be(k int, x ...IDoc) Doc {
	select {
	case <-b.done:
		return Nil
	default:
	}
	if len(x) == 0 {
		return Nil
	}
	d := x[0]
	z := x[1:]
	if d.d == Nil {
		return b.be(k, z...)
	}
	if t, ok := d.d.(concat); ok {
		return b.be(k, append([]IDoc{{d.i, t.a}, {d.i, t.b}}, z...)...)
	}
	if t, ok := d.d.(nest); ok {
		x[0] = IDoc{
			d: t.d,
			i: d.i + t.n,
		}
		return b.be(k, x...)
	}
	if t, ok := d.d.(text); ok {
		return textX{
			s: string(t),
			d: b.be(k+len(t), z...),
		}
	}
	if d.d == Line {
		return lineX{
			i: d.i,
			d: b.be(d.i, z...),
		}
	}
	t, ok := d.d.(union)
	if !ok {
		panic(fmt.Errorf("unknown type: %T", d.d))
	}

	var sb strings.Builder
	for _, xd := range x {
		sb.WriteString(xd.String())
	}
	s := sb.String()
	cached, ok := b.cache[s]
	if ok {
		return cached
	}

	n := append([]IDoc{{d.i, t.a}}, z...)
	res := better(b.w, k,
		b.be(k, n...),
		func() Doc {
			n[0].d = t.b
			return b.be(k, n...)
		},
	)
	b.cache[s] = res
	return res
}

func better(w, k int, x Doc, y func() Doc) Doc {
	if fits(w-k, x) {
		return x
	}
	return y()
}

func fits(w int, x Doc) bool {
	if w < 0 {
		return false
	}
	if x == Nil {
		return true
	}
	if t, ok := x.(textX); ok {
		return fits(w-len(t.s), t.d)
	}
	if _, ok := x.(lineX); ok {
		return true
	}
	panic(fmt.Errorf("unknown type: %T", x))
}

func layout(w io.Writer, d Doc) error {
	if d == Nil {
		return nil
	}
	switch d := d.(type) {
	case textX:
		_, err := w.Write([]byte(d.s))
		if err != nil {
			return err
		}
		return layout(w, d.d)
	case lineX:
		_, err := w.Write(append([]byte{'\n'}, bytes.Repeat([]byte{' '}, d.i)...))
		if err != nil {
			return err
		}
		return layout(w, d.d)
	}
	panic(fmt.Errorf("unknown type: %T", d))
}
