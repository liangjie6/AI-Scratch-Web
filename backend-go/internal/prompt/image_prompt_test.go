package prompt

import (
	"strings"
	"testing"
)

func TestImagePromptSeparatesSpriteBackgroundsFromScenes(t *testing.T) {
	for _, tc := range []struct {
		target, background  string
		required, forbidden []string
	}{
		{"sprite", "solid", []string{"#00ff00", "non-touching", "without cropping", "exact count requested"}, []string{"coherent composition", "true alpha"}},
		{"sprite", "transparent", []string{"true alpha", "fully transparent", "non-touching", "without cropping"}, []string{"#00ff00", "coherent composition"}},
		{"backdrop", "transparent", []string{"edge to edge", "coherent composition"}, []string{"#00ff00", "true alpha", "isolate every"}},
	} {
		t.Run(tc.target+"/"+tc.background, func(t *testing.T) {
			got, err := BuildImagePrompt("custom", "ink illustration", "three cats", 640, 360, tc.target, tc.background)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range append(tc.required, "three cats", "ink illustration", "640 by 360") {
				if !strings.Contains(got, required) {
					t.Errorf("missing requirement %q", required)
				}
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(got, forbidden) {
					t.Errorf("conflicting requirement %q", forbidden)
				}
			}
		})
	}
}
