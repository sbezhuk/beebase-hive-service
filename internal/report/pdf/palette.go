package pdf

// RGB is the small presentation color primitive used by the report renderer.
// These values mirror the light BeeBase mobile palette; the PDF is a
// print-oriented light document, so it does not switch to mobile dark tokens.
type RGB struct {
	R int
	G int
	B int
}

type ReportPalette struct {
	Brand         RGB
	Background    RGB
	Card          RGB
	Border        RGB
	TextPrimary   RGB
	TextSecondary RGB
	Good          RGB
	Watch         RGB
	Concern       RGB
	Unknown       RGB
}

var beeBasePalette = ReportPalette{
	Brand:         RGB{R: 232, G: 172, B: 61},
	Background:    RGB{R: 251, G: 246, B: 234},
	Card:          RGB{R: 242, G: 233, B: 214},
	Border:        RGB{R: 225, G: 211, B: 183},
	TextPrimary:   RGB{R: 43, G: 27, B: 14},
	TextSecondary: RGB{R: 110, G: 93, B: 69},
	Good:          RGB{R: 154, G: 93, B: 20},
	Watch:         RGB{R: 181, G: 101, B: 29},
	Concern:       RGB{R: 199, G: 64, B: 45},
	Unknown:       RGB{R: 110, G: 93, B: 69},
}

func (p ReportPalette) HealthState(state string) RGB {
	switch state {
	case "GOOD":
		return p.Good
	case "WATCH":
		return p.Watch
	case "CONCERN":
		return p.Concern
	default:
		return p.Unknown
	}
}

// InsufficientDataZone mirrors the Flutter chart's neutral Card surface at
// 50% opacity over the light report page background. It is intentionally not
// one of the semantic health-state colors.
func (p ReportPalette) InsufficientDataZone() RGB {
	return RGB{
		R: (p.Card.R + 255) / 2,
		G: (p.Card.G + 255) / 2,
		B: (p.Card.B + 255) / 2,
	}
}
