package main

import "github.com/ymsaki/githive/internal/app/idresolve"

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

// itemDisplayIDs must receive the complete collection, before --tail or any
// other view truncation, because event-reference lookup scans the whole entity.
func itemDisplayIDs(items []map[string]any) map[string]string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		if id, ok := item["id"].(string); ok {
			ids = append(ids, id)
		}
	}
	return idresolve.Abbreviate(ids)
}
