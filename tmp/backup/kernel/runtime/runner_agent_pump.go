package runtime

import (
	"context"
	"fmt"

	"github.com/OnslaughtSnail/caelis/kernel/agent"
	"github.com/OnslaughtSnail/caelis/kernel/session"
)

type agentRunItem struct {
	event *session.Event
	err   error
}

type agentPanicError struct {
	value any
}

func (e *agentPanicError) Error() string {
	if e == nil {
		return "runtime: agent panic"
	}
	return fmt.Sprintf("runtime: agent panic: %v", e.value)
}

type agentRunPump struct {
	ctx    context.Context
	items  chan agentRunItem
	resume chan bool
	done   chan struct{}
}

func startAgentRunPump(ctx context.Context, ag agent.Agent, inv *invocationContext) *agentRunPump {
	pump := &agentRunPump{
		ctx:    ctx,
		items:  make(chan agentRunItem),
		resume: make(chan bool),
		done:   make(chan struct{}),
	}
	go func() {
		defer close(pump.done)
		defer close(pump.items)
		defer func() {
			if p := recover(); p != nil {
				select {
				case pump.items <- agentRunItem{err: &agentPanicError{value: p}}:
				case <-ctx.Done():
				}
			}
		}()
		for ev, err := range ag.Run(inv) {
			select {
			case pump.items <- agentRunItem{event: ev, err: err}:
			case <-ctx.Done():
				return
			}
			select {
			case cont := <-pump.resume:
				if !cont {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	return pump
}

func (p *agentRunPump) next() (agentRunItem, bool) {
	if p == nil {
		return agentRunItem{}, false
	}
	select {
	case item, ok := <-p.items:
		return item, ok
	case <-p.ctx.Done():
		return agentRunItem{}, false
	}
}

func (p *agentRunPump) respond(cont bool) bool {
	if p == nil {
		return false
	}
	select {
	case <-p.done:
		return false
	case p.resume <- cont:
		return true
	case <-p.ctx.Done():
		return false
	}
}
