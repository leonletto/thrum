import { describe, it, expect, beforeEach } from 'vitest';
import {
  uiStore,
  setSelectedView,
  selectAgent,
  selectGroup,
  selectMyInbox,
  selectLiveFeed,
  selectSettings,
  setSelectedMessageId,
} from '../uiStore';

describe('uiStore', () => {
  beforeEach(() => {
    // Reset store to initial state
    uiStore.setState({
      selectedView: 'live-feed',
      selectedAgentId: null,
      selectedGroupName: null,
      selectedMessageId: null,
    });
  });

  it('should have correct initial state', () => {
    const state = uiStore.state;
    expect(state.selectedView).toBe('live-feed');
    expect(state.selectedAgentId).toBe(null);
    expect(state.selectedGroupName).toBe(null);
    expect(state.selectedMessageId).toBe(null);
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
    selectAgent('agent:test');

    setSelectedView('agent-inbox');
    expect(uiStore.state.selectedView).toBe('agent-inbox');
    expect(uiStore.state.selectedAgentId).toBe('agent:test');
  });

  it('should set group and view with selectGroup', () => {
    selectGroup('backend');
    expect(uiStore.state.selectedView).toBe('group-channel');
    expect(uiStore.state.selectedGroupName).toBe('backend');
    expect(uiStore.state.selectedAgentId).toBe(null);
  });

  it('should clear group when selecting agent', () => {
    selectGroup('backend');
    selectAgent('agent:test');
    expect(uiStore.state.selectedGroupName).toBe(null);
    expect(uiStore.state.selectedAgentId).toBe('agent:test');
  });

  it('should clear group when navigating away from group-channel', () => {
    selectGroup('backend');
    setSelectedView('live-feed');
    expect(uiStore.state.selectedGroupName).toBe(null);
  });

  it('should preserve selectedGroupName when navigating to group-channel', () => {
    selectGroup('backend');
    setSelectedView('group-channel');
    expect(uiStore.state.selectedView).toBe('group-channel');
    expect(uiStore.state.selectedGroupName).toBe('backend');
  });

  it('should set view to settings with selectSettings', () => {
    selectAgent('agent:test');
    selectSettings();
    expect(uiStore.state.selectedView).toBe('settings');
    expect(uiStore.state.selectedAgentId).toBe(null);
    expect(uiStore.state.selectedGroupName).toBe(null);
  });

  it('should set selectedMessageId with setSelectedMessageId', () => {
    setSelectedMessageId('msg-abc-123');
    expect(uiStore.state.selectedMessageId).toBe('msg-abc-123');
  });

  it('should clear selectedMessageId by passing null', () => {
    setSelectedMessageId('msg-abc-123');
    setSelectedMessageId(null);
    expect(uiStore.state.selectedMessageId).toBe(null);
  });

  it('should preserve other state when setting selectedMessageId', () => {
    selectAgent('agent:test');
    setSelectedMessageId('msg-xyz');
    expect(uiStore.state.selectedView).toBe('agent-inbox');
    expect(uiStore.state.selectedAgentId).toBe('agent:test');
    expect(uiStore.state.selectedMessageId).toBe('msg-xyz');
  });
});
