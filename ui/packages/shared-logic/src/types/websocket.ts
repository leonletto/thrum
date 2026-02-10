import { z } from 'zod';

/**
 * JSON-RPC 2.0 request schema
 */
export const JsonRpcRequestSchema = z.object({
  jsonrpc: z.literal('2.0'),
  method: z.string(),
  params: z.record(z.any()).optional(),
  id: z.union([z.string(), z.number()]),
});

export type JsonRpcRequest = z.infer<typeof JsonRpcRequestSchema>;

/**
 * JSON-RPC 2.0 success response schema
 */
export const JsonRpcSuccessResponseSchema = z.object({
  jsonrpc: z.literal('2.0'),
  result: z.any(),
  id: z.union([z.string(), z.number()]),
});

export type JsonRpcSuccessResponse = z.infer<typeof JsonRpcSuccessResponseSchema>;

/**
 * JSON-RPC 2.0 error response schema
 */
export const JsonRpcErrorResponseSchema = z.object({
  jsonrpc: z.literal('2.0'),
  error: z.object({
    code: z.number(),
    message: z.string(),
    data: z.any().optional(),
  }),
  id: z.union([z.string(), z.number(), z.null()]),
});

export type JsonRpcErrorResponse = z.infer<typeof JsonRpcErrorResponseSchema>;

/**
 * JSON-RPC 2.0 response (success or error)
 */
export const JsonRpcResponseSchema = z.union([
  JsonRpcSuccessResponseSchema,
  JsonRpcErrorResponseSchema,
]);

export type JsonRpcResponse = z.infer<typeof JsonRpcResponseSchema>;

/**
 * Event notification (no id field)
 */
export const JsonRpcNotificationSchema = z.object({
  jsonrpc: z.literal('2.0'),
  method: z.string(),
  params: z.record(z.any()).optional(),
});

export type JsonRpcNotification = z.infer<typeof JsonRpcNotificationSchema>;

/**
 * WebSocket connection state
 */
export const ConnectionState = {
  DISCONNECTED: 'disconnected',
  CONNECTING: 'connecting',
  CONNECTED: 'connected',
  RECONNECTING: 'reconnecting',
  FAILED: 'failed',
} as const;
export type ConnectionState = (typeof ConnectionState)[keyof typeof ConnectionState];

/**
 * WebSocket client configuration
 */
export interface WebSocketConfig {
  url: string;
  maxReconnectAttempts?: number;
  reconnectDelayMs?: number;
  maxReconnectDelayMs?: number;
  requestTimeoutMs?: number;
}

/**
 * WebSocket error types
 */
export class WebSocketError extends Error {
  code?: number;
  data?: unknown;

  constructor(message: string, code?: number, data?: unknown) {
    super(message);
    this.name = 'WebSocketError';
    this.code = code;
    this.data = data;
  }
}

export class WebSocketTimeoutError extends WebSocketError {
  constructor(method: string, timeoutMs: number) {
    super(`Request timeout after ${timeoutMs}ms: ${method}`);
    this.name = 'WebSocketTimeoutError';
  }
}

export class WebSocketConnectionError extends WebSocketError {
  constructor(message: string) {
    super(message);
    this.name = 'WebSocketConnectionError';
  }
}
