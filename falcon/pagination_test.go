package falcon_test

import (
	"testing"

	"github.com/gollem-dev/tools/falcon"
	"github.com/m-mizutani/gt"
)

func TestClampLimit(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		max  int
		want int
	}{
		{"absent uses max", map[string]any{}, 100, 100},
		{"within range", map[string]any{"limit": float64(20)}, 100, 20},
		{"over max clamps", map[string]any{"limit": float64(500)}, 100, 100},
		{"zero uses max", map[string]any{"limit": float64(0)}, 100, 100},
		{"negative uses max", map[string]any{"limit": float64(-5)}, 100, 100},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gt.Number(t, falcon.ClampLimit(c.args, c.max)).Equal(c.want)
		})
	}
}

func TestClampIDs(t *testing.T) {
	kept, dropped := falcon.ClampIDs([]string{"a", "b", "c"}, 5)
	gt.Array(t, kept).Equal([]string{"a", "b", "c"})
	gt.Number(t, dropped).Equal(0)

	kept, dropped = falcon.ClampIDs([]string{"a", "b", "c", "d"}, 2)
	gt.Array(t, kept).Equal([]string{"a", "b"})
	gt.Number(t, dropped).Equal(2)
}
