# @thrum/shared-logic

Shared business logic, API clients, and state management for Thrum UI
applications.

## Package Structure

```
shared-logic/
├── src/
│   ├── api/           # WebSocket client, RPC calls
│   ├── hooks/         # Custom hooks (useAgentList, useInbox, etc.)
│   ├── stores/        # TanStack Store definitions
│   ├── types/         # TypeScript types, Zod schemas
│   └── index.ts       # Public exports
├── package.json
├── tsconfig.json
└── vitest.config.ts
```

## Purpose

This package provides framework-agnostic business logic and state management
that can be shared across:

- **web-app**: React web interface
- **tui-app**: Terminal UI application

## Key Features

- **API Layer**: WebSocket client and RPC method wrappers
- **State Management**: TanStack Store for global state
- **Type Safety**: TypeScript types with Zod runtime validation
- **Custom Hooks**: Reusable hooks for common UI patterns
- **Tested**: >80% code coverage target

## Scripts

- `pnpm build` - Type check the package
- `pnpm dev` - Watch mode type checking
- `pnpm lint` - Lint source code
- `pnpm test` - Run tests
- `pnpm test:watch` - Run tests in watch mode
- `pnpm test:coverage` - Run tests with coverage report
- `pnpm type-check` - Type check without emitting

## Dependencies

- **@tanstack/query-core**: Data fetching and caching
- **@tanstack/store**: Lightweight state management
- **zod**: Schema validation

## Development Status

Package structure initialized. Implementation in progress.
