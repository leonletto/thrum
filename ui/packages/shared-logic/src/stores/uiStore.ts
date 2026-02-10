import { Store } from '@tanstack/store';

export type View = 'live-feed' | 'my-inbox' | 'agent-inbox' | 'who-has';

export interface UIState {
  selectedView: View;
  selectedAgentId: string | null;
}

const initialState: UIState = {
  selectedView: 'live-feed',
  selectedAgentId: null,
};

export const uiStore = new Store<UIState>(initialState);

// Actions
export const setSelectedView = (view: View) => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: view,
    // Clear selectedAgentId when navigating away from agent inbox
    selectedAgentId: view === 'agent-inbox' ? state.selectedAgentId : null,
  }));
};

export const selectAgent = (agentId: string) => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'agent-inbox',
    selectedAgentId: agentId,
  }));
};

export const selectMyInbox = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'my-inbox',
    selectedAgentId: null,
  }));
};

export const selectLiveFeed = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'live-feed',
    selectedAgentId: null,
  }));
};

export const selectWhoHas = () => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedView: 'who-has',
    selectedAgentId: null,
  }));
};
