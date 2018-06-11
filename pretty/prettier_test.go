package pretty

import (
	"fmt"
	"testing"
)

func TestPrettier(t *testing.T) {
	d := Stack()
	last := ""
	for i := 0; i < 50; i++ {
		s, _ := PrettyString(d, i)
		if s == last {
			continue
		}
		last = s
		fmt.Printf("%d:\n%s\n\n", i, s)
	}
}
