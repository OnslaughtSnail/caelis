package cases

import (
	"fmt"
	"strings"

	"github.com/OnslaughtSnail/caelis/kernel/model"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

// Case defines one runtime eval scenario.
type Case struct {
	Name        string
	Description string
	Prompt      string
	Validate    func([]*session.Event) error
}

func Light() []Case {
	return []Case{
		{
			Name:        "basic_reply",
			Description: "assistant returns non-empty response",
			Prompt:      "请用一句话说明你的能力范围。",
			Validate:    validateAssistantNonEmpty,
		},
		{
			Name:        "basic_reply_en",
			Description: "assistant answers one concise English sentence",
			Prompt:      "Reply in exactly one short English sentence: what can you do?",
			Validate:    validateAssistantNonEmpty,
		},
		{
			Name:        "tool_echo",
			Description: "assistant calls echo tool and returns final text",
			Prompt:      "你必须先调用工具 echo，参数严格为 {\"text\":\"kernel-v2\"}。不要直接回答。收到工具结果后再给出一句总结。",
			Validate:    validateHasToolAndAssistant,
		},
		{
			Name:        "tool_now",
			Description: "assistant calls now tool before final response",
			Prompt:      "请先调用工具 now 一次，再返回一句话说明当前时间。",
			Validate:    validateHasToolAndAssistantByName("now"),
		},
		{
			Name:        "tool_echo_now_chain",
			Description: "assistant calls echo and now in one turn",
			Prompt:      "请先调用工具 echo 参数 {\"text\":\"chain\"}，再调用工具 now，最后输出一句总结。",
			Validate:    validateHasToolsAndAssistant("echo", "now"),
		},
		{
			Name:        "tool_echo_unicode",
			Description: "assistant handles non-ascii tool args",
			Prompt:      "必须调用工具 echo，参数严格为 {\"text\":\"你好，caelis\"}，然后返回一句确认。",
			Validate:    validateHasToolAndAssistantByName("echo"),
		},
	}
}

func Nightly() []Case {
	out := append([]Case{}, Light()...)
	for i := 1; i <= 30; i++ {
		idx := i
		out = append(out, Case{
			Name:        fmt.Sprintf("long_context_%02d", idx),
			Description: "long-context stability and answer continuity",
			Prompt:      fmt.Sprintf("这是夜间稳定性测试 #%d。请先简述问题，再给出两个可执行建议。", idx),
			Validate:    validateAssistantNonEmpty,
		})
	}
	return out
}

func validateAssistantNonEmpty(events []*session.Event) error {
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev == nil {
			continue
		}
		if ev.Message.Role == model.RoleAssistant && strings.TrimSpace(ev.Message.Text) != "" {
			return nil
		}
	}
	return fmt.Errorf("no non-empty assistant response")
}

func validateHasToolAndAssistant(events []*session.Event) error {
	return validateHasToolAndAssistantByName("echo")(events)
}

func validateHasToolAndAssistantByName(toolName string) func([]*session.Event) error {
	return func(events []*session.Event) error {
		hasTool := false
		hasAssistant := false
		for _, ev := range events {
			if ev == nil {
				continue
			}
			if ev.Message.ToolResponse != nil && ev.Message.ToolResponse.Name == toolName {
				hasTool = true
			}
			if ev.Message.Role == model.RoleAssistant && strings.TrimSpace(ev.Message.Text) != "" {
				hasAssistant = true
			}
		}
		if !hasTool {
			return fmt.Errorf("expected %s tool call", toolName)
		}
		if !hasAssistant {
			return fmt.Errorf("expected assistant final text")
		}
		return nil
	}
}

func validateHasToolsAndAssistant(toolNames ...string) func([]*session.Event) error {
	return func(events []*session.Event) error {
		toolSet := map[string]bool{}
		for _, name := range toolNames {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			toolSet[name] = false
		}
		hasAssistant := false
		for _, ev := range events {
			if ev == nil {
				continue
			}
			if ev.Message.ToolResponse != nil {
				if _, ok := toolSet[ev.Message.ToolResponse.Name]; ok {
					toolSet[ev.Message.ToolResponse.Name] = true
				}
			}
			if ev.Message.Role == model.RoleAssistant && strings.TrimSpace(ev.Message.Text) != "" {
				hasAssistant = true
			}
		}
		for name, ok := range toolSet {
			if !ok {
				return fmt.Errorf("expected %s tool call", name)
			}
		}
		if !hasAssistant {
			return fmt.Errorf("expected assistant final text")
		}
		return nil
	}
}
