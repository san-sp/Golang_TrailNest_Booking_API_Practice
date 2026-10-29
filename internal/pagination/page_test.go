package pagination

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePage(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		want   int
		wantOK bool
	}{
		{name: "first page", value: "1", want: 1, wantOK: true},
		{
			name:   "maximum page",
			value:  strconv.Itoa(MaxPage),
			want:   MaxPage,
			wantOK: true,
		},
		{name: "zero", value: "0"},
		{name: "negative", value: "-1"},
		{name: "invalid text", value: "abc"},
		{name: "above maximum", value: "1000001"},
		{name: "integer overflow", value: "999999999999999999999999999999"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParsePage(tt.value)
			require.Equal(t, tt.wantOK, ok)

			if tt.wantOK {
				require.Equal(t, tt.want, got)
			}
		})
	}
}
