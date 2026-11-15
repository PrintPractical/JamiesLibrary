package barcode

import (
	"fmt"
	"strings"
)

// SVG renders content as a standalone SVG document: black modules on a white
// background with a quiet zone, and a viewBox but no fixed size, so the viewer
// scales it. Invalid content returns an error and no image.
func SVG(kind Kind, content string) (string, error) {
	m, err := encode(kind, content)
	if err != nil {
		return "", err
	}
	q := m.quiet
	w := m.w + 2*q
	h := m.h + 2*q
	if m.oneD {
		h = barHeightModules + 2*q
	}

	var path strings.Builder
	if m.oneD {
		m.runs(0, func(x, n int) {
			fmt.Fprintf(&path, "M%d %dh%dv%dh-%dz", x+q, q, n, barHeightModules, n)
		})
	} else {
		for y := 0; y < m.h; y++ {
			m.runs(y, func(x, n int) {
				fmt.Fprintf(&path, "M%d %dh%dv1h-%dz", x+q, y+q, n, n)
			})
		}
	}

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`+
		`<rect width="%d" height="%d" fill="#fff"/><path d="%s" fill="#000"/></svg>`,
		w, h, w, h, path.String()), nil
}
