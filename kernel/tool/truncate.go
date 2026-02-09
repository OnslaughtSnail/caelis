package tool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const approxBytesPerToken = 4

// TruncationPolicy defines limits for truncating tool output.
type TruncationPolicy struct {
	MaxTokens int
	MaxBytes  int
}

// DefaultTruncationPolicy returns default tool output truncation policy.
func DefaultTruncationPolicy() TruncationPolicy {
	return TruncationPolicy{MaxTokens: 10000}
}

// TruncationInfo describes truncation that was applied.
type TruncationInfo struct {
	Truncated       bool
	Policy          string
	MaxTokens       int
	MaxBytes        int
	EstimatedTokens int
	EstimatedBytes  int
	RemovedTokens   int
	RemovedBytes    int
	OmittedItems    int
}

// TruncateMap applies truncation to a tool result map and returns the updated
// map plus truncation info.
func TruncateMap(input map[string]any, policy TruncationPolicy) (map[string]any, TruncationInfo) {
	info := TruncationInfo{
		MaxTokens: policy.MaxTokens,
		MaxBytes:  policy.MaxBytes,
	}
	budgetTokens := policy.tokenBudget()
	if budgetTokens <= 0 {
		return input, info
	}
	info.Policy = "tokens"
	if policy.MaxBytes > 0 && policy.MaxTokens <= 0 {
		info.Policy = "bytes"
	}

	totalTokens := estimateTokensForValue(input)
	info.EstimatedTokens = totalTokens
	info.EstimatedBytes = totalTokens * approxBytesPerToken
	if totalTokens <= budgetTokens {
		return input, info
	}

	remaining := budgetTokens
	state := &truncationState{}
	out := truncateValue(input, &remaining, state)
	result, _ := out.(map[string]any)
	if result == nil {
		result = map[string]any{}
	}

	info.Truncated = true
	info.OmittedItems = state.omitted
	info.RemovedTokens = totalTokens - remaining
	if info.RemovedTokens < 0 {
		info.RemovedTokens = 0
	}
	info.RemovedBytes = info.RemovedTokens * approxBytesPerToken
	return result, info
}

type truncationState struct {
	omitted int
}

func truncateValue(value any, remaining *int, state *truncationState) any {
	if remaining == nil || *remaining <= 0 {
		if state != nil {
			state.omitted++
		}
		return nil
	}
	switch v := value.(type) {
	case string:
		cost := estimateTextTokens(v)
		if cost <= *remaining {
			*remaining -= cost
			return v
		}
		if truncated, ok := truncateJSONText(v, *remaining, state); ok {
			*remaining = 0
			return truncated
		}
		policy := TruncationPolicy{MaxTokens: *remaining}
		truncated, removed := TruncateText(v, policy)
		*remaining = 0
		if removed > 0 && state != nil {
			state.omitted++
		}
		return truncated
	case map[string]any:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(v))
		for _, key := range keys {
			if *remaining <= 0 {
				if state != nil {
					state.omitted++
				}
				continue
			}
			val := truncateValue(v[key], remaining, state)
			if val == nil {
				continue
			}
			out[key] = val
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for _, item := range v {
			if *remaining <= 0 {
				if state != nil {
					state.omitted++
				}
				continue
			}
			val := truncateValue(item, remaining, state)
			if val == nil {
				continue
			}
			out = append(out, val)
		}
		if state != nil && state.omitted > 0 && *remaining > 0 {
			marker := fmt.Sprintf("[omitted %d items]", state.omitted)
			cost := estimateTextTokens(marker)
			if cost <= *remaining {
				*remaining -= cost
				out = append(out, marker)
			}
		}
		return out
	default:
		text := fmt.Sprint(value)
		cost := estimateTextTokens(text)
		if cost <= *remaining {
			*remaining -= cost
			return value
		}
		if state != nil {
			state.omitted++
		}
		return nil
	}
}

// TruncateString truncates a string in the middle to fit the policy budget.
func TruncateString(s string, policy TruncationPolicy) (string, int) {
	if s == "" {
		return s, 0
	}
	budgetBytes := policy.byteBudget()
	if budgetBytes <= 0 || len(s) <= budgetBytes {
		return s, 0
	}
	leftBudget := budgetBytes / 2
	rightBudget := budgetBytes - leftBudget
	prefixEnd, suffixStart := splitUTF8Bounds(s, leftBudget, rightBudget)
	left := s[:prefixEnd]
	right := s[suffixStart:]
	removedBytes := len(s) - (len(left) + len(right))
	removedTokens := approxTokensFromBytes(removedBytes)
	marker := formatTruncationMarker(policy, removedTokens, removedBytes)
	return left + marker + right, removedTokens
}

// TruncateText truncates text and includes total line count when truncated.
func TruncateText(s string, policy TruncationPolicy) (string, int) {
	truncated, removed := TruncateString(s, policy)
	if removed == 0 {
		return truncated, removed
	}
	if strings.Contains(s, "\n") {
		lines := strings.Count(s, "\n") + 1
		truncated = fmt.Sprintf("Total output lines: %d\n\n%s", lines, truncated)
	}
	return truncated, removed
}

func truncateJSONText(s string, remaining int, state *truncationState) (string, bool) {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return "", false
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return "", false
	}
	subRemaining := remaining
	truncated := truncateValue(parsed, &subRemaining, state)
	if truncated == nil {
		return "", false
	}
	data, err := json.Marshal(truncated)
	if err != nil {
		return "", false
	}
	result := string(data)
	policy := TruncationPolicy{MaxTokens: remaining}
	if estimateTextTokens(result) > remaining {
		result, _ = TruncateText(result, policy)
	}
	return result, true
}

func splitUTF8Bounds(s string, leftBudget, rightBudget int) (int, int) {
	if leftBudget < 0 {
		leftBudget = 0
	}
	if rightBudget < 0 {
		rightBudget = 0
	}
	length := len(s)
	targetSuffix := max(length-rightBudget, 0)
	prefixEnd := 0
	suffixStart := length
	for idx, r := range s {
		end := idx + utf8.RuneLen(r)
		if end <= leftBudget {
			prefixEnd = end
		}
		if idx >= targetSuffix {
			suffixStart = idx
			break
		}
	}
	if suffixStart < prefixEnd {
		suffixStart = prefixEnd
	}
	return prefixEnd, suffixStart
}

func formatTruncationMarker(policy TruncationPolicy, removedTokens, removedBytes int) string {
	if policy.MaxTokens > 0 {
		if removedTokens <= 0 {
			return "...truncated..."
		}
		return fmt.Sprintf("...%d tokens truncated...", removedTokens)
	}
	if removedBytes <= 0 {
		return "...truncated..."
	}
	return fmt.Sprintf("...%d chars truncated...", removedBytes)
}

func estimateTokensForValue(value any) int {
	switch v := value.(type) {
	case string:
		return estimateTextTokens(v)
	case map[string]any:
		sum := 0
		for k, val := range v {
			sum += estimateTextTokens(k)
			sum += estimateTokensForValue(val)
		}
		return sum
	case []any:
		sum := 0
		for _, item := range v {
			sum += estimateTokensForValue(item)
		}
		return sum
	default:
		return estimateTextTokens(fmt.Sprint(value))
	}
}

func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	bytes := len(s)
	tokens := bytes / approxBytesPerToken
	if bytes%approxBytesPerToken != 0 {
		tokens++
	}
	return tokens
}

func approxTokensFromBytes(bytes int) int {
	if bytes <= 0 {
		return 0
	}
	tokens := bytes / approxBytesPerToken
	if bytes%approxBytesPerToken != 0 {
		tokens++
	}
	return tokens
}

func (p TruncationPolicy) tokenBudget() int {
	if p.MaxTokens > 0 {
		return p.MaxTokens
	}
	if p.MaxBytes > 0 {
		return p.MaxBytes / approxBytesPerToken
	}
	return 0
}

func (p TruncationPolicy) byteBudget() int {
	if p.MaxBytes > 0 {
		return p.MaxBytes
	}
	if p.MaxTokens > 0 {
		return p.MaxTokens * approxBytesPerToken
	}
	return 0
}

// AddTruncationMeta attaches truncation metadata to a tool result map.
func AddTruncationMeta(result map[string]any, info TruncationInfo) map[string]any {
	if !info.Truncated {
		return result
	}
	if result == nil {
		result = map[string]any{}
	}
	meta := map[string]any{
		"truncated":        info.Truncated,
		"policy":           info.Policy,
		"max_tokens":       info.MaxTokens,
		"max_bytes":        info.MaxBytes,
		"estimated_tokens": info.EstimatedTokens,
		"estimated_bytes":  info.EstimatedBytes,
		"removed_tokens":   info.RemovedTokens,
		"removed_bytes":    info.RemovedBytes,
		"omitted_items":    info.OmittedItems,
	}
	key := "_tool_truncation"
	if _, exists := result[key]; exists {
		key = "_tool_truncation_meta"
	}
	result[key] = compactMeta(meta)
	return result
}

func compactMeta(meta map[string]any) map[string]any {
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		switch val := v.(type) {
		case int:
			if val == 0 && k != "max_tokens" && k != "max_bytes" {
				continue
			}
		case bool:
			if !val && k != "truncated" {
				continue
			}
		case string:
			if strings.TrimSpace(val) == "" {
				continue
			}
		}
		out[k] = v
	}
	return out
}
