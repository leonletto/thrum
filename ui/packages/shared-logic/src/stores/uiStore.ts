import { Store } from '@tanstack/store';

export type View = 'live-feed' | 'my-inbox' | 'agent-inbox' | 'group-channel' | 'who-has' | 'settings';

export interface UIState {
  selectedView: View;
  selectedAgentId: string | null;
  selectedGroupName: string | null;
}

const initialState: UIState = {
  selectedView: 'live-feed',
  selectedAgentId: null,
  selectedGroupName: null,
};

export const uiStore = new Store<UIState>(initialState);

// Actions
export const setSelectedView = (view: View) => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: view,
    selectedAgentId: view === 'agent-inbox' ? state.selectedAgentId : null,
    selectedGroupName: view === 'group-channel' ? state.selectedGroupName : null,
  }));
};

export const selectAgent = (agentId: string) => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'agent-inbox',
    selectedAgentId: agentId,
    selectedGroupName: null,
  }));
};

export const selectGroup = (groupName: string) => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'group-channel',
    selectedGroupName: groupName,
    selectedAgentId: null,
  }));
};

export const selectMyInbox = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'my-inbox',
    selectedAgentId: null,
    selectedGroupName: null,
  }));
};

export const selectLiveFeed = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'live-feed',
    selectedAgentId: null,
    selectedGroupName: null,
  }));
};

export const selectWhoHas = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'who-has',
    selectedAgentId: null,
    selectedGroupName: null,
  }));
};

export const selectSettings = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'settings',
    selectedAgentId: null,
    selectedGroupName: null,
  }));
};
