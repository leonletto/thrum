import { createContext, useContext, useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  useUserIdentify,
  useUserRegister,
  loadStoredUser,
  ensureConnected,
} from '@thrum/shared-logic';
import type { UserRegisterResponse } from '@thrum/shared-logic';

interface AuthContextValue {
  user: UserRegisterResponse | null;
  isLoading: boolean;
  error: string | null;
}

const AuthContext = createContext<AuthContextValue>({
  user: null,
  isLoading: true,
  error: null,
});

export function useAuth() {
  return useContext(AuthContext);
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const queryClient = useQueryClient();
  const identify = useUserIdentify();
  const register = useUserRegister();
  const [user, setUser] = useState<UserRegisterResponse | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [attempted, setAttempted] = useState(false);

  useEffect(() => {
    if (attempted) return;
    setAttempted(true);

    async function autoRegister() {
      try {
        // Check localStorage for stored user first
        const stored = loadStoredUser();

        // Always identify from git config to get current username
        let username: string;
        let display: string | undefined;

        try {
          const identifyResult = await identify.mutateAsync();
          username = identifyResult.username;
          display = identifyResult.display;
        } catch {
          // Fallback: use stored username or default
          if (stored) {
            username = stored.username;
            display = stored.display_name;
          } else {
            setError('Could not determine identity from git config');
            setIsLoading(false);
            return;
          }
        }

        // Register (idempotent — safe for re-registration)
        const result = await register.mutateAsync({
          username,
          display,
        });

        setUser(result);
        queryClient.setQueryData(['user', 'current'], result);
      } catch (err) {
        const msg = err instanceof Error ? err.message : 'Registration failed';
        setError(msg);
      } finally {
        setIsLoading(false);
      }
    }

    // Wait for WebSocket to actually connect before calling RPCs
    let cancelled = false;
    (async () => {
      try {
        await ensureConnected();
      } catch {
        // ensureConnected may throw if connection fails — proceed anyway
        // so the error is surfaced by the identify/register calls
      }
      if (!cancelled) {
        autoRegister();
      }
    })();
    return () => { cancelled = true; };
  }, [attempted, identify, register, queryClient]);

  return (
    <AuthContext.Provider value={{ user, isLoading, error }}>
      {children}
    </AuthContext.Provider>
  );
}
