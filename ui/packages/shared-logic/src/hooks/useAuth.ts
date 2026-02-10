import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';
import type {
  UserRegisterRequest,
  UserRegisterResponse,
  UserIdentifyResponse,
} from '../types/api';

const STORAGE_KEY = 'thrum_user';

interface StoredUser {
  user_id: string;
  username: string;
  display_name?: string;
  token: string;
}

/** Persist user to localStorage */
function persistUser(data: UserRegisterResponse): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({
      user_id: data.user_id,
      username: data.username,
      display_name: data.display_name,
      token: data.token,
    }));
  } catch {
    // localStorage unavailable (SSR, private browsing)
  }
}

/** Load user from localStorage */
export function loadStoredUser(): StoredUser | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as StoredUser;
  } catch {
    return null;
  }
}

/** Clear stored user */
export function clearStoredUser(): void {
  try {
    localStorage.removeItem(STORAGE_KEY);
  } catch {
    // noop
  }
}

/**
 * Hook to identify the current git user (for auto-registration).
 * Calls user.identify RPC which returns git config user.name/email.
 */
export function useUserIdentify() {
  return useMutation({
    mutationFn: async () => {
      await ensureConnected();
      return wsClient.call<UserIdentifyResponse>('user.identify', {});
    },
  });
}

/**
 * Hook to register a user.
 * Idempotent — safe to call on every page load.
 * Persists result to localStorage.
 */
export function useUserRegister() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: async (request: UserRegisterRequest) => {
      await ensureConnected();
      return wsClient.call<UserRegisterResponse>('user.register', request);
    },
    onSuccess: (data) => {
      queryClient.setQueryData(['user', 'current'], data);
      persistUser(data);
    },
  });
}

/**
 * Hook to access current user data.
 * Returns cached user data from registration response, or undefined if not registered.
 */
export function useCurrentUser() {
  const queryClient = useQueryClient();
  return queryClient.getQueryData<UserRegisterResponse>(['user', 'current']);
}
