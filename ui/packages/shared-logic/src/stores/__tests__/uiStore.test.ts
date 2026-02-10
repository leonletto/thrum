import { describe, it, expect, beforeEach } from 'vitest';
import {
  uiStore,
  setSelectedView,
  selectAgent,
  selectMyInbox,
  selectLiveFeed,
} from '../uiStore';

describe('uiStore', () => {
  beforeEach(() => {
    // Reset store to initial state
    uiStore.setState({
      selectedView: 'live-feed',
      selectedAgentId: null,
    });
  });

  it('should have correct initial state', () => {
    const state = uiStore.state;
    expect(state.selectedView).toBe('live-feed');
    expect(state.selectedAgentId).toBe(null);
  });

  it('should update view with setSelectedView', () => {
    setSelectedView('my-inbox');
    expect(uiStore.state.selectedView).toBe('my-inbox');
    expect(uiStore.state.selectedAgentId).toBe(null);
  });

  it('should clear selectedAgentId when navigating away from agent-inbox', () => {
    // First select an agent
    selectAgent('agent:test');
    expect(uiStore.state.selectedView).toBe('agent-inbox');
    expect(uiStore.state.selectedAgentId).toBe('agent:test');

    // Navigate to live feed - should clear agent
    setSelectedView('live-feed');
    expect(uiStore.state.selectedView).toBe('live-feed');
    expect(uiStore.state.selectedAgentId).toBe(null);
  });

  it('should set agent and view with selectAgent', () => {
    selectAgent('agent:claude-daemon');
    expect(uiStore.state.selectedView).toBe('agent-inbox');
    expect(uiStore.state.selectedAgentId).toBe('agent:claude-daemon');
  });

  it('should clear agent when selecting my inbox', () => {
    // First select an agent
    selectAgent('agent:test');

    // Then select my inbox
    selectMyInbox();
    expect(uiStore.state.selectedView).toBe('my-inbox');
    expect(uiStore.state.selectedAgentId).toBe(null);
  });

  it('should clear agent when selecting live feed', () => {
    // First select an agent
    selectAgent('agent:test');

    // Then select live feed
    selectLiveFeed();
    expect(uiStore.state.selectedView).toBe('live-feed');
    expect(uiStore.state.selectedAgentId).toBe(null);
  });

  it('should preserve selectedAgentId when navigating to agent-inbox', () => {
    // Set up initial state with an agent selected
    selectAgent('agent:test');

    // Navigate to agent-inbox explicitly (should preserve agent)
    setSelectedView('agent-inbox');
    expect(uiStore.state.selectedView).toBe('agent-inbox');
    expect(uiStore.state.selectedAgentId).toBe('agent:test');
  });
});
