import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ensureConnected, wsClient } from '../api/client';

export interface TelegramStatusResponse {
  configured: boolean;
  enabled: boolean;
  running: boolean;
  token?: string; // masked: "123456789:..."
  target: string;
  user_id: string;
  chat_id?: number;
  allow_all: boolean;
  allow_from?: number[];
  connected_at?: string;
  inbound_count: number;
  error?: string;
}

export function useTelegramStatus() {
  return useQuery({
    queryKey: ['telegram', 'status'],
    queryFn: async () => {
      await ensureConnected();
      return wsClient.call<TelegramStatusResponse>('telegram.status', {});
    },
    refetchInterval: 10000,
  });
}

export function useTelegramConfigure() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (config: Record<string, unknown>) => {
      await ensureConnected();
      return wsClient.call<{ status: string; message: string }>(
        'telegram.configure',
        config
      );
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['telegram'] });
    },
  });
}

export interface TelegramPairResponse {
  telegram_user_id: number;
  telegram_username: string;
  first_name: string;
  last_name: string;
  chat_id: number;
  message_text: string;
}

export function useTelegramPair() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (opts: { timeout?: number }) => {
      await ensureConnected();
      return wsClient.call<TelegramPairResponse>('telegram.pair', {
        timeout_seconds: opts.timeout || 60,
      });
    },
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ['telegram'] }),
  });
}
