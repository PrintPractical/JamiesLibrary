package settings

// DefaultScheme is the warm scheme a new installation uses.
const DefaultScheme = "hearth"

// Scheme is a preset colour scheme: the colours of the admin-setup mockup.
// Primary is the accent that buttons and links use until a custom accent
// replaces it; Paper is the page background.
type Scheme struct {
	ID          string
	Name        string
	Description string

	Wood, Wood2, OnWood, OnWood2 string // the dark navigation bar and its text
	Primary, PrimaryHover        string
	PrimarySoft, OnPrimary       string
	Brass                        string
	Paper, Card, Ink, Ink2       string
	Line                         string
}

// Schemes returns the presets, the default first. The slice is a copy.
func Schemes() []Scheme {
	out := make([]Scheme, len(presets))
	copy(out, presets)
	return out
}

// SchemeByID finds a preset by its ID.
func SchemeByID(id string) (Scheme, bool) {
	for _, s := range presets {
		if s.ID == id {
			return s, true
		}
	}
	return Scheme{}, false
}

var presets = []Scheme{
	{
		ID: "hearth", Name: "Hearth", Description: "Warm leather & cream · default",
		Wood: "#3B2A1F", Wood2: "#4A3527", OnWood: "#F6EBDB", OnWood2: "#D9C6AC",
		Primary: "#7A2E22", PrimaryHover: "#63241A", PrimarySoft: "#F4E2D7", OnPrimary: "#FFF8F0",
		Brass: "#C08A2E",
		Paper: "#F7F0E3", Card: "#FFFBF3", Ink: "#2A1F17", Ink2: "#5C4A3B", Line: "#E4D5BF",
	},
	{
		ID: "reading-room", Name: "Reading Room", Description: "Green baize & brass",
		Wood: "#22362C", Wood2: "#2D463A", OnWood: "#EEF0E4", OnWood2: "#BFCDBF",
		Primary: "#2F5D46", PrimaryHover: "#24493A", PrimarySoft: "#DFEBE0", OnPrimary: "#F8FAF2",
		Brass: "#B8892F",
		Paper: "#F2F0E6", Card: "#FBFAF3", Ink: "#1C2A22", Ink2: "#475549", Line: "#D9D8C6",
	},
	{
		ID: "inkwell", Name: "Inkwell", Description: "Navy & parchment",
		Wood: "#1F2A3A", Wood2: "#2A3748", OnWood: "#EDEFF3", OnWood2: "#B9C3D2",
		Primary: "#2C4A6E", PrimaryHover: "#223A57", PrimarySoft: "#DEE6F0", OnPrimary: "#F8FAFC",
		Brass: "#C9A15A",
		Paper: "#F3F1EC", Card: "#FCFBF8", Ink: "#1A2230", Ink2: "#4B5566", Line: "#DAD6CB",
	},
	{
		ID: "heather", Name: "Heather", Description: "Plum & rose",
		Wood: "#3A2D3F", Wood2: "#483A4E", OnWood: "#F5ECF0", OnWood2: "#D2C0CC",
		Primary: "#6B3F69", PrimaryHover: "#563254", PrimarySoft: "#EFDFEA", OnPrimary: "#FDF7FA",
		Brass: "#D19A6A",
		Paper: "#F7EFEA", Card: "#FFFAF7", Ink: "#2A1E2C", Ink2: "#5A4A5C", Line: "#E6D5CF",
	},
}

// Tokens is the scheme's colours as the CSS custom properties the web shell
// reads, with an accent (#RRGGBB, or "" for none) in place of the primary. The
// caller has checked the accent with CheckAccent.
func (s Scheme) Tokens(accent string) map[string]string {
	primary, hover, soft := s.Primary, s.PrimaryHover, s.PrimarySoft
	if c, ok := parseHex(accent); ok {
		primary = c.hex()
		h, sat, l := c.hsl()
		hover = fromHSL(h, sat, l*0.82).hex()
		soft = fromHSL(h, sat, 0.92).hex()
	}
	return map[string]string{
		"--paper": s.Paper, "--card": s.Card, "--ink": s.Ink, "--ink-2": s.Ink2, "--line": s.Line,
		"--primary": primary, "--primary-hover": hover, "--primary-soft": soft, "--on-primary": s.OnPrimary,
		"--brass": s.Brass,
		"--wood":  s.Wood, "--wood-2": s.Wood2, "--on-wood": s.OnWood, "--on-wood-2": s.OnWood2,
	}
}
