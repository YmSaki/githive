package main

// displayID falls back to the full ID if a ref disappeared between reading
// the list snapshot and computing its current abbreviations.
func displayID(ids map[string]string, value any) string {
	full, _ := value.(string)
	if short, ok := ids[full]; ok {
		return short
	}
	return full
}

func displayIDWidth(ids map[string]string, items []map[string]any) int {
	width := 10
	for _, item := range items {
		if length := len(displayID(ids, item["id"])); length > width {
			width = length
		}
	}
	return width
}
