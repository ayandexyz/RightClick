package pdfops

import (
	"strings"
	"testing"
)

func TestBuildStampDescription(t *testing.T) {
	tests := []struct {
		name string
		p    StampParams
		want string
	}{
		{"defaults", StampParams{Position: "bc", Opacity: 100, Size: 12, Color: "#112233"}, "pos:bc, op:1, points:12, offset:0 36, rot:0, scalefactor:1 abs, fillcol:#112233"},
		{"rotation", StampParams{Position: "tr", Opacity: 50, Size: 20, Color: "#AABBCC", Rotation: 45}, "pos:tr, op:.5, points:20, offset:-36 -36, rot:45, scalefactor:1 abs, fillcol:#aabbcc"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BuildStampDescription(tc.p)
			if err != nil {
				t.Fatalf("BuildStampDescription: %v", err)
			}
			if got != tc.want {
				t.Fatalf("description = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildStampDescriptionRejectsUnsafeOrInvalidValues(t *testing.T) {
	base := StampParams{Position: "bc", Opacity: 100, Size: 12, Color: "#112233", Text: "ok"}
	cases := []StampParams{
		{Position: "xx", Opacity: 100, Size: 12, Color: "#112233"},
		{Position: "bc", Opacity: 101, Size: 12, Color: "#112233"},
		{Position: "bc", Opacity: 100, Size: 3, Color: "#112233"},
		{Position: "bc", Opacity: 100, Size: 12, Color: "red"},
		{Position: "bc", Opacity: 100, Size: 12, Color: "#112233", Text: "bad\ntext"},
		{Position: "bc", Opacity: 100, Size: 12, Color: "#112233", Text: strings.Repeat("x", 201)},
		{Position: "bc:evil", Opacity: 100, Size: 12, Color: "#112233"},
	}
	for i, p := range cases {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			if _, err := BuildStampDescription(p); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if _, err := BuildStampDescription(base); err != nil {
		t.Fatalf("valid stamp rejected: %v", err)
	}
}
