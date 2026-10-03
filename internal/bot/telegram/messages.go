package telegram

// SplitMessages bounds outbound text in UTF-16 units, including supplementary
// characters, so a long answer plus its label fits Telegram's 4096-unit limit.
func SplitMessages(messages []string) []string {
	var result []string
	for _, text := range messages {
		start, units := 0, 0
		for index, r := range text {
			n := 1
			if r > 0xffff {
				n = 2
			}
			if units+n > 4096 {
				result = append(result, text[start:index])
				start = index
				units = 0
			}
			units += n
		}
		if start < len(text) {
			result = append(result, text[start:])
		}
	}
	return result
}
