package builder

import (
	"encoding/json"
	"sync"
	"unicode/utf8"

	"github.com/tiktoken-go/tokenizer"
)

// A fixed local encoding makes the compaction policy deterministic even when
// OPENROUTER_MODEL changes. Provider tokenization may differ: UI counts are
// estimates, never a replacement for provider-reported billable usage.
var memoryCodecs = sync.Pool{New: func() any {
	c, err := tokenizer.Get(tokenizer.O200kBase)
	if err != nil {
		panic(err)
	} // Embedded, supported vocabulary; no network I/O.
	return c
}}

const messageFramingTokens = 4

func tokenCount(text string) (int, error) {
	c := memoryCodecs.Get().(tokenizer.Codec)
	defer memoryCodecs.Put(c)
	return c.Count(text)
}

func memoryTokens(summary chatSummary, messages []Message) ([]int, int, error) {
	total := 0
	if summary.content != "" {
		n, err := tokenCount(memoryPrefix + summary.content)
		if err != nil {
			return nil, 0, err
		}
		total = n + messageFramingTokens
	}
	counts := make([]int, len(messages))
	for i, m := range messages {
		text := m.Content
		if m.Role == ResultRole {
			text = "Saved run outcome: " + text
		}
		n, err := tokenCount(text)
		if err != nil {
			return nil, 0, err
		}
		counts[i] = n + messageFramingTokens
		total += counts[i]
	}
	return counts, total, nil
}

type summarySegment struct {
	message  Message
	complete bool
	tokens   int
}

// Old installations can contain a single message too large for one summary
// request. Split only that model input at UTF-8 boundaries; display history is
// untouched. Partial-message summaries are never committed on a failed batch.
func summarySegments(messages []Message) ([]summarySegment, error) {
	var out []summarySegment
	for _, m := range messages {
		rest := []rune(m.Content)
		for len(rest) > 0 {
			end := len(rest)
			part := m
			part.Content = string(rest)
			encoded, err := json.Marshal(part)
			if err != nil {
				return nil, err
			}
			n, err := tokenCount(string(encoded))
			if err != nil {
				return nil, err
			}
			const segmentLimit = memoryTokenLimit / 2
			if n > segmentLimit {
				low, high := 1, len(rest)
				for low < high {
					middle := (low + high + 1) / 2
					part.Content = string(rest[:middle])
					encoded, err = json.Marshal(part)
					if err != nil {
						return nil, err
					}
					n, err = tokenCount(string(encoded))
					if err != nil {
						return nil, err
					}
					if n <= segmentLimit {
						low = middle
					} else {
						high = middle - 1
					}
				}
				end = low
				part.Content = string(rest[:end])
				encoded, err = json.Marshal(part)
				if err != nil {
					return nil, err
				}
				n, err = tokenCount(string(encoded))
				if err != nil {
					return nil, err
				}
			}
			out = append(out, summarySegment{message: part, complete: end == len(rest), tokens: n})
			rest = rest[end:]
		}
	}
	return out, nil
}

func summaryPayload(previous string, segments []summarySegment) ([]byte, error) {
	messages := make([]Message, len(segments))
	for i, segment := range segments {
		messages[i] = segment.message
	}
	return json.Marshal(struct {
		Previous string
		Messages []Message
	}{previous, messages})
}

// Batch admission uses tokenized serialized data (including the previous
// summary), instructions and an output reserve. There is no message-count or
// character-count batch limit. Verify the full payload after the cheap scan
// because BPE merges at JSON boundaries can change the additive estimate.
func summaryBatch(previous string, segments []summarySegment) ([]byte, int, error) {
	base, err := summaryPayload(previous, nil)
	if err != nil {
		return nil, 0, err
	}
	instructions, err := tokenCount(summaryInstructions)
	if err != nil {
		return nil, 0, err
	}
	overhead, err := tokenCount(string(base))
	if err != nil {
		return nil, 0, err
	}
	budget := memoryTokenLimit - summaryTokenLimit - 2*messageFramingTokens
	end, size := 0, overhead+instructions
	for end < len(segments) && size+segments[end].tokens+1 < budget {
		size += segments[end].tokens + 1
		end++
	}
	for end > 0 {
		data, err := summaryPayload(previous, segments[:end])
		if err != nil {
			return nil, 0, err
		}
		n, err := tokenCount(string(data))
		if err != nil {
			return nil, 0, err
		}
		if n+instructions < budget {
			return data, end, nil
		}
		end--
	}
	return nil, 0, ErrMessage
}

func validMemoryMessage(text string) bool {
	if !utf8.ValidString(text) {
		return false
	}
	n, err := tokenCount(text)
	// Leave space for a full historical summary and message framing, so even a
	// request with many multi-token Unicode characters can be retained verbatim.
	prefix, prefixErr := tokenCount(memoryPrefix)
	return err == nil && prefixErr == nil && n+prefix+summaryTokenLimit+2*messageFramingTokens < memoryTokenLimit
}
