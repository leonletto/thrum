import React from "react";
import { render, Box, Text } from "ink";

/**
 * Thrum TUI - Placeholder
 *
 * Full implementation coming in a later epic.
 * This is a minimal Ink-based terminal UI application.
 */

const App: React.FC = () => {
  return (
    <Box flexDirection="column" padding={1}>
      <Box borderStyle="round" borderColor="cyan" padding={1} marginBottom={1}>
        <Text bold color="cyan">
          ╔═══════════════════════════════════════╗
        </Text>
      </Box>
      <Box borderStyle="round" borderColor="cyan" padding={1} marginBottom={1}>
        <Text bold color="cyan">
          ║       Thrum Terminal UI (TUI)        ║
        </Text>
      </Box>
      <Box borderStyle="round" borderColor="cyan" padding={1} marginBottom={1}>
        <Text bold color="cyan">
          ╚═══════════════════════════════════════╝
        </Text>
      </Box>

      <Text>
        <Text color="green">✓</Text> Package initialized and ready for development
      </Text>
      <Box marginTop={1}>
        <Text>
          <Text color="yellow">Coming Soon:</Text> Interactive terminal interface for
          managing agents and messages
        </Text>
      </Box>
      <Box marginTop={1}>
        <Text dimColor>
          Tech Stack: Ink 5.x, React 18.x, TypeScript 5.x
        </Text>
      </Box>
    </Box>
  );
};

render(<App />);
