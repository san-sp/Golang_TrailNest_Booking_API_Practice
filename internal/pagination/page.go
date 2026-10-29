package pagination

import "strconv"

const MaxPage = 1_000_000

func ParsePage(value string) (int, bool) {
	page, err := strconv.Atoi(value)
	if err != nil || page < 1 || page > MaxPage {
		return 0, false
	}

	return page, true
}
