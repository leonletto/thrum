import { createContext, useContext, useEffect, useRef, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import {
  useUserIdentify,
  useUserRegister,
  loadStoredUser,
  ensureConnected,
  wsClient,
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

  // Capture mutation functions in refs so the effect has stable references
  // without needing them in the dependency array.
  const identifyRef = useRef(identify);
  const registerRef = useRef(register);
  identifyRef.current = identify;
  registerRef.current = register;

  useEffect(() => {
    let cancelled = false;

    async function autoRegister() {
      try {
        const stored = loadStoredUser();

        let username: string;
        let display: string | undefined;

        try {
          const identifyResult = await identifyRef.current.mutateAsync();
          if (cancelled) return;
          username = identifyResult.username;
          display = identifyResult.display;
        } catch {
          if (cancelled) return;
          if (stored) {
            username = stored.username;
            display = stored.display_name;
          } else {
            setError('Could not determine identity from git config');
            setIsLoading(false);
            return;
          }
        }

        const result = await registerRef.current.mutateAsync({
          username,
          display,
        });
        if (cancelled) return;

        // Start a session for the web UI user so they can send messages.
        // message.send requires an active session in the sessions table.
        try {
          await wsClient.call('session.start', { agent_id: result.user_id });
        } catch {
          // Session start may fail if one already exists — that's fine
        }

        // Subscribe to all notifications so the daemon pushes real-time
        // events (new messages, thread updates) over this WebSocket.
        try {
          await wsClient.call('subscribe', { all: true });
        } catch {
          // Subscribe may fail if session already has a subscription — that's fine
        }
        if (cancelled) return;

        setUser(result);
        queryClient.setQueryData(['user', 'current'], result);
      } catch (err) {
        if (cancelled) return;
        const msg = err instanceof Error ? err.message : 'Registration failed';
        setError(msg);
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    }

    (async () => {
      try {
        await ensureConnected();
      } catch {
        // Connection failure will surface via identify/register errors
      }
      if (!cancelled) {
        autoRegister();
      }
    })();

    return () => { cancelled = true; };
  }, [queryClient]);

  return (
    <AuthContext.Provider value={{ user, isLoading, error }}>
      {children}
    </AuthContext.Provider>
  );
}
