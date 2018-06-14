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
	return be(ctx.Done(), w, k, IDoc{0, x})
}

type IDoc struct {
	i int
	d Doc
}

func be(done <-chan struct{}, w, k int, x ...IDoc) Doc {
	select {
	case <-done:
		return Nil
	default:
	}
	if len(x) == 0 {
		return Nil
	}
	d := x[0]
	z := x[1:]
	if d.d == Nil {
		return be(done, w, k, z...)
	} else if t, ok := d.d.(concat); ok {
		return be(done, w, k, append([]IDoc{{d.i, t.a}, {d.i, t.b}}, z...)...)
	} else if t, ok := d.d.(nest); ok {
		x[0] = IDoc{
			d: t.d,
			i: d.i + t.n,
		}
		return be(done, w, k, x...)
	} else if t, ok := d.d.(text); ok {
		return textX{
			s: string(t),
			d: be(done, w, k+len(t), z...),
		}
	} else if d.d == Line {
		return lineX{
			i: d.i,
			d: be(done, w, d.i, z...),
		}
	} else if t, ok := d.d.(union); ok {
		n := append([]IDoc{{d.i, t.a}}, z...)
		return better(w, k,
			be(done, w, k, n...),
			func() Doc {
				n[0].d = t.b
				return be(done, w, k, n...)
			},
		)
	} else {
		panic(fmt.Errorf("unknown type: %T", d.d))
	}
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
