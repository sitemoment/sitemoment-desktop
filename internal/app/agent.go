// SPDX-License-Identifier: AGPL-3.0-or-later
package app

import (
	"context"
	"errors"
	"time"

	"github.com/sitemoment/sitemoment-desktop/internal/agent"
	"github.com/sitemoment/sitemoment-desktop/internal/store"
)

// spendModeKey is the setting holding the spend mode new sessions start with.
const spendModeKey = "agent.spendMode"

// AgentService serves the Agent panel. Progress arrives as agent:event events.
type AgentService struct {
	Agent *agent.Agent
	Store *store.Store
	Now   func() time.Time // default time.Now
}

func (s *AgentService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Sessions lists a site's sessions (0: those without a site), newest first.
func (s *AgentService) Sessions(ctx context.Context, siteID int64) ([]agent.SessionView, error) {
	list, err := s.Store.AgentSessions(ctx, siteID)
	if err != nil {
		return nil, err
	}
	out := make([]agent.SessionView, len(list))
	for i, a := range list {
		out[i] = agent.ViewOf(a)
	}
	return out, nil
}

// Create starts an empty session for siteID in the last chosen spend mode.
func (s *AgentService) Create(ctx context.Context, siteID int64) (agent.SessionView, error) {
	mode, err := s.Store.Setting(ctx, spendModeKey)
	if err != nil {
		return agent.SessionView{}, err
	}
	if !agent.ValidMode(mode) {
		mode = agent.ModeConfirm
	}
	sess, err := s.Store.CreateAgentSession(ctx, siteID, mode, s.now().Unix())
	return agent.ViewOf(sess), err
}

// Delete removes a session that is not answering.
func (s *AgentService) Delete(ctx context.Context, id int64) error {
	sess, err := s.Store.AgentSession(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sess.Status == store.AgentRunning {
		return errors.New("这个会话正在回答，请先停止")
	}
	return s.Store.DeleteAgentSession(ctx, id)
}

// Transcript is the session's conversation for the panel.
func (s *AgentService) Transcript(ctx context.Context, id int64) (agent.Transcript, error) {
	return s.Agent.Transcript(ctx, id)
}

// Send asks the agent and returns once the turn has started.
func (s *AgentService) Send(ctx context.Context, id int64, text string) error {
	return s.Agent.Start(ctx, id, text)
}

// Stop stops the session's running turn.
func (s *AgentService) Stop(ctx context.Context, id int64) error {
	s.Agent.Stop(id)
	return nil
}

// Confirm answers the confirm bar: approve runs the held paid calls.
func (s *AgentService) Confirm(ctx context.Context, id int64, approve bool) error {
	return s.Agent.Resume(ctx, id, approve)
}

// SetMode changes the session's spend mode (from its next turn) and makes
// it the default for new sessions.
func (s *AgentService) SetMode(ctx context.Context, id int64, mode string) (agent.SessionView, error) {
	if !agent.ValidMode(mode) {
		return agent.SessionView{}, errors.New("未知的付费模式：" + mode)
	}
	if err := s.Store.SetAgentSpendMode(ctx, id, mode, s.now().Unix()); err != nil {
		return agent.SessionView{}, err
	}
	if err := s.Store.SetSetting(ctx, spendModeKey, mode); err != nil {
		return agent.SessionView{}, err
	}
	sess, err := s.Store.AgentSession(ctx, id)
	return agent.ViewOf(sess), err
}

// ModelLabel names the model the agent will use, or says why there is none.
func (s *AgentService) ModelLabel(ctx context.Context) (string, error) {
	m, err := s.Agent.Models(ctx)
	if err != nil {
		return "", err
	}
	return m.Label(), nil
}
