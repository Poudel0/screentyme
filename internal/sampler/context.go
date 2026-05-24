package sampler

import "strings"

func CleanTitle(title string) string {
	i := strings.LastIndex(title, " - ")
	if i < 0 {
		return title
	}
	return strings.TrimSpace(title[:i])
}
