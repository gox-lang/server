package gox

// ClientAdaptValues is the exhaustive, case-sensitive vocabulary used by the
// compiler and editor. Return a fresh slice so callers cannot mutate the contract.
func ClientAdaptValues() []string {
	return []string{"top", "bottom", "left", "right", "vertical-center", "horizontal-center", "top-left", "top-right", "bottom-left", "bottom-right", "center", "none"}
}

func validateAdaptAttrs(attrs []TemplateAttr, client bool) error {
	seen := false
	for _, attr := range attrs {
		if attr.Name != "adapt" {
			continue
		}
		if !client {
			return newTemplateErrorfWithHint("invalid-client-adapt", attr.Pos, "Use adapt only on a client component (<@Component>).", "adapt is only valid on client components")
		}
		if seen {
			return newTemplateErrorfWithHint("duplicate-client-adapt", attr.Pos, "Declare adapt only once.", "duplicate adapt attribute")
		}
		seen = true
		valid := false
		if attr.Kind == AttrStatic {
			for _, value := range ClientAdaptValues() {
				if attr.Value == value {
					valid = true
					break
				}
			}
		}
		if !valid {
			return newTemplateErrorfWithHint("invalid-client-adapt", attr.Pos, "Use a literal: top, bottom, left, right, vertical-center, horizontal-center, top-left, top-right, bottom-left, bottom-right, center, or none.", "invalid adapt attribute: expected one of the 12 literal values")
		}
	}
	return nil
}
