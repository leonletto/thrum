import {
  ConnectionState,
  WebSocketConnectionError,
  WebSocketError,
  WebSocketTimeoutError,
} from '../types/websocket';
import type {
  JsonRpcErrorResponse,
  JsonRpcNotification,
  JsonRpcRequest,
  JsonRpcResponse,
  JsonRpcSuccessResponse,
  WebSocketConfig,
} from '../types/websocket';

type PendingRequest = {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  timeoutId: ReturnType<typeof setTimeout>;
};

type EventHandler<T = unknown> = (data: T) => void;

/**
 * WebSocket client for Thrum daemon communication
 *
 * Features:
 * - JSON-RPC 2.0 request/response handling
 * - Automatic reconnection with exponential backoff
 * - Event streaming and subscriptions
 * - Request timeout handling
 * - Connection state management
 */
export class ThrumWebSocket {
  private ws: WebSocket | null = null;
  private connectionState: ConnectionState = ConnectionState.DISCONNECTED;
  private reconnectAttempts = 0;
  private reconnectTimeoutId: ReturnType<typeof setTimeout> | null = null;
  private nextRequestId = 1;
  private pendingRequests = new Map<number | string, PendingRequest>();
  private eventHandlers = new Map<string, Set<EventHandler>>();
  private stateChangeHandlers = new Set<(state: ConnectionState) => void>();

  private readonly maxReconnectAttempts: number;
  private readonly reconnectDelayMs: number;
  private readonly maxReconnectDelayMs: number;
  private readonly requestTimeoutMs: number;
  private readonly url: string;

  constructor(config: WebSocketConfig) {
    this.url = config.url;
    this.maxReconnectAttempts = config.maxReconnectAttempts ?? 10;
    this.reconnectDelayMs = config.reconnectDelayMs ?? 1000;
    this.maxReconnectDelayMs = config.maxReconnectDelayMs ?? 30000;
    this.requestTimeoutMs = config.requestTimeoutMs ?? 30000;
  }

  /**
   * Connect to the WebSocket server
   */
  async connect(): Promise<void> {
    if (this.ws?.readyState === WebSocket.OPEN) {
      return;
    }

    if (this.ws?.readyState === WebSocket.CONNECTING) {
      // Already connecting, wait for it
      return new Promise((resolve, reject) => {
        const onOpen = () => {
          cleanup();
          resolve();
        };
        const onError = () => {
          cleanup();
          reject(new WebSocketConnectionError('Connection failed'));
        };
        const cleanup = () => {
          this.ws?.removeEventListener('open', onOpen);
          this.ws?.removeEventListener('error', onError);
        };
        this.ws?.addEventListener('open', onOpen);
        this.ws?.addEventListener('error', onError);
      });
    }

    return new Promise((resolve, reject) => {
      this.setState(ConnectionState.CONNECTING);

      try {
        this.ws = new WebSocket(this.url);
      } catch (error) {
        this.setState(ConnectionState.FAILED);
        reject(new WebSocketConnectionError(`Failed to create WebSocket: ${error}`));
        return;
      }

      const onOpen = () => {
        cleanup();
        this.reconnectAttempts = 0;
        this.setState(ConnectionState.CONNECTED);
        resolve();
      };

      const onError = () => {
        cleanup();
        this.setState(ConnectionState.FAILED);
        reject(new WebSocketConnectionError('Connection failed'));
        this.scheduleReconnect();
      };

      const cleanup = () => {
        this.ws?.removeEventListener('open', onOpen);
        this.ws?.removeEventListener('error', onError);
      };

      this.ws.addEventListener('open', onOpen);
      this.ws.addEventListener('error', onError);
      this.ws.addEventListener('message', this.handleMessage.bind(this));
      this.ws.addEventListener('close', this.handleClose.bind(this));
    });
  }

  /**
   * Disconnect from the WebSocket server
   */
  disconnect(): void {
    if (this.reconnectTimeoutId) {
      clearTimeout(this.reconnectTimeoutId);
      this.reconnectTimeoutId = null;
    }

    if (this.ws) {
      this.ws.close(1000, 'Client disconnect');
      this.ws = null;
    }

    // Reject all pending requests
    for (const [id, pending] of this.pendingRequests.entries()) {
      clearTimeout(pending.timeoutId);
      pending.reject(new WebSocketConnectionError('Connection closed'));
      this.pendingRequests.delete(id);
    }

    this.setState(ConnectionState.DISCONNECTED);
  }

  /**
   * Make a JSON-RPC call
   */
  async call<T = unknown>(method: string, params?: Record<string, unknown>): Promise<T> {
    if (!this.isConnected) {
      throw new WebSocketConnectionError('Not connected');
    }

    const id = this.nextRequestId++;
    const request: JsonRpcRequest = {
      jsonrpc: '2.0',
      method,
      params,
      id,
    };

    return new Promise((resolve, reject) => {
      const timeoutId = setTimeout(() => {
        this.pendingRequests.delete(id);
        reject(new WebSocketTimeoutError(method, this.requestTimeoutMs));
      }, this.requestTimeoutMs);

      this.pendingRequests.set(id, {
        resolve: resolve as (value: unknown) => void,
        reject,
        timeoutId,
      });

      this.ws!.send(JSON.stringify(request));
    });
  }

  /**
   * Subscribe to an event
   * @returns Unsubscribe function
   */
  on<T = unknown>(event: string, handler: EventHandler<T>): () => void {
    if (!this.eventHandlers.has(event)) {
      this.eventHandlers.set(event, new Set());
    }
    this.eventHandlers.get(event)!.add(handler as EventHandler);

    // Return unsubscribe function
    return () => {
      const handlers = this.eventHandlers.get(event);
      if (handlers) {
        handlers.delete(handler as EventHandler);
        if (handlers.size === 0) {
          this.eventHandlers.delete(event);
        }
      }
    };
  }

  /**
   * Subscribe to connection state changes
   * @returns Unsubscribe function
   */
  onStateChange(handler: (state: ConnectionState) => void): () => void {
    this.stateChangeHandlers.add(handler);
    // Immediately call with current state
    handler(this.connectionState);

    return () => {
      this.stateChangeHandlers.delete(handler);
    };
  }

  /**
   * Get current connection state
   */
  get state(): ConnectionState {
    return this.connectionState;
  }

  /**
   * Check if connected
   */
  get isConnected(): boolean {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  /**
   * Handle incoming WebSocket messages
   */
  private handleMessage(event: MessageEvent): void {
    try {
      const data = JSON.parse(event.data);

      // Check if it's a response to a pending request
      if ('id' in data && data.id !== undefined && data.id !== null) {
        this.handleResponse(data as JsonRpcResponse);
      }
      // Check if it's a notification (event)
      else if ('method' in data) {
        this.handleNotification(data as JsonRpcNotification);
      }
    } catch (error) {
      console.error('Failed to parse WebSocket message:', error);
    }
  }

  /**
   * Handle JSON-RPC response
   */
  private handleResponse(response: JsonRpcResponse): void {
    // Skip null IDs (shouldn't happen but be defensive)
    if (response.id === null) {
      console.warn('Received response with null ID');
      return;
    }

    const pending = this.pendingRequests.get(response.id);
    if (!pending) {
      console.warn('Received response for unknown request ID:', response.id);
      return;
    }

    clearTimeout(pending.timeoutId);
    this.pendingRequests.delete(response.id);

    if ('error' in response) {
      const errorResponse = response as JsonRpcErrorResponse;
      pending.reject(
        new WebSocketError(
          errorResponse.error.message,
          errorResponse.error.code,
          errorResponse.error.data
        )
      );
    } else {
      const successResponse = response as JsonRpcSuccessResponse;
      pending.resolve(successResponse.result);
    }
  }

  /**
   * Handle JSON-RPC notification (event)
   */
  private handleNotification(notification: JsonRpcNotification): void {
    const handlers = this.eventHandlers.get(notification.method);
    if (handlers) {
      for (const handler of handlers) {
        try {
          handler(notification.params);
        } catch (error) {
          console.error(`Error in event handler for ${notification.method}:`, error);
        }
      }
    }
  }

  /**
   * Handle WebSocket close
   */
  private handleClose(): void {
    this.setState(ConnectionState.DISCONNECTED);
    this.scheduleReconnect();
  }

  /**
   * Schedule reconnection attempt
   */
  private scheduleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      this.setState(ConnectionState.FAILED);
      return;
    }

    // Exponential backoff with jitter
    const delay = Math.min(
      this.reconnectDelayMs * Math.pow(2, this.reconnectAttempts) +
        Math.random() * 1000,
      this.maxReconnectDelayMs
    );

    this.reconnectAttempts++;
    this.setState(ConnectionState.RECONNECTING);

    this.reconnectTimeoutId = setTimeout(() => {
      this.reconnectTimeoutId = null;
      this.connect().catch(() => {
        // Error already handled in connect()
      });
    }, delay);
  }

  /**
   * Update connection state and notify handlers
   */
  private setState(state: ConnectionState): void {
    if (this.connectionState === state) {
      return;
    }

    this.connectionState = state;

    for (const handler of this.stateChangeHandlers) {
      try {
        handler(state);
      } catch (error) {
        console.error('Error in state change handler:', error);
      }
    }
  }
}
