import { Store } from '@tanstack/store';

export type View = 'live-feed' | 'my-inbox' | 'agent-inbox' | 'group-channel' | 'who-has' | 'settings';

export interface UIState {
  selectedView: View;
  selectedAgentId: string | null;
  selectedGroupName: string | null;
  selectedMessageId: string | null;
}

export const validViews: View[] = [
  'live-feed',
  'my-inbox',
  'agent-inbox',
  'group-channel',
  'who-has',
  'settings',
];

export function stateFromHash(): Partial<UIState> {
  if (typeof window === 'undefined') return {};
  const hash = window.location.hash.slice(1); // strip leading '#'
  if (!hash) return {};
  const params = new URLSearchParams(hash);
  const result: Partial<UIState> = {};
  const view = params.get('view');
  if (view && (validViews as string[]).includes(view)) {
    result.selectedView = view as View;
  }
  const agent = params.get('agent');
  if (agent) {
    result.selectedAgentId = agent;
  }
  const group = params.get('group');
  if (group) {
    result.selectedGroupName = group;
  }
  return result;
}

function applyStateToHash(state: UIState): void {
  if (typeof window === 'undefined') return;
  const params = new URLSearchParams();
  if (state.selectedView) {
    params.set('view', state.selectedView);
  }
  if (state.selectedAgentId !== null) {
    params.set('agent', state.selectedAgentId);
  }
  if (state.selectedGroupName !== null) {
    params.set('group', state.selectedGroupName);
  }
  window.history.replaceState(null, '', '#' + params.toString());
}

export function stateToHash(state: UIState): void {
  applyStateToHash(state);
}

const initialState: UIState = {
  selectedView: 'live-feed',
  selectedAgentId: null,
  selectedGroupName: null,
  selectedMessageId: null,
  ...stateFromHash(),
};

export const uiStore = new Store<UIState>(initialState);

uiStore.subscribe(({ currentVal }) => applyStateToHash(currentVal));
stateToHash(initialState);

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

export const setSelectedMessageId = (messageId: string | null) => {
  uiStore.setState((state: UIState) => ({
    ...state,
    selectedMessageId: messageId,
  }));
};
