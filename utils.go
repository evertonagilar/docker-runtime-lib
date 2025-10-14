package container

import "fmt"

func QuoteList(list []string) []string {
	quoted := make([]string, len(list))
	for i, s := range list {
		quoted[i] = fmt.Sprintf("%q", s)
	}
	return quoted
}
