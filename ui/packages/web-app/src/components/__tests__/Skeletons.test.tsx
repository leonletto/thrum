import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { ThreadListSkeleton } from '../inbox/ThreadListSkeleton';
import { AgentListSkeleton } from '../agents/AgentListSkeleton';
import { MessageSkeleton, MessageListSkeleton } from '../inbox/MessageSkeleton';

describe('Skeleton Components', () => {
  describe('ThreadListSkeleton', () => {
    it('renders 5 skeleton thread items', () => {
      const { container } = render(<ThreadListSkeleton />);

      const skeletons = container.querySelectorAll('.animate-pulse');
      // 5 threads * 2 skeletons per thread = 10 total
      expect(skeletons).toHaveLength(10);
    });

    it('renders within Card components', () => {
      const { container } = render(<ThreadListSkeleton />);

      const cards = container.querySelectorAll('[class*="border"]');
      expect(cards.length).toBeGreaterThan(0);
    });
  });

  describe('AgentListSkeleton', () => {
    it('renders 4 skeleton agent items', () => {
      const { container } = render(<AgentListSkeleton />);

      const skeletons = container.querySelectorAll('.animate-pulse');
      // 1 header + (4 agents * 2 skeletons) = 9 total
      expect(skeletons).toHaveLength(9);
    });

    it('has proper spacing', () => {
      const { container } = render(<AgentListSkeleton />);

      const wrapper = container.querySelector('.space-y-1');
      expect(wrapper).toBeInTheDocument();
    });
  });

  describe('MessageSkeleton', () => {
    it('renders skeleton for sender and message', () => {
      const { container } = render(<MessageSkeleton />);

      const skeletons = container.querySelectorAll('.animate-pulse');
      // 2 skeletons for header (name + timestamp) + 1 for message body = 3
      expect(skeletons.length).toBe(3);
    });

    it('has left border styling', () => {
      const { container } = render(<MessageSkeleton />);

      const wrapper = container.querySelector('.border-l-2');
      expect(wrapper).toBeInTheDocument();
    });
  });

  describe('MessageListSkeleton', () => {
    it('renders default 3 message skeletons', () => {
      const { container } = render(<MessageListSkeleton />);

      const messages = container.querySelectorAll('.border-l-2');
      expect(messages.length).toBe(3);
    });

    it('renders custom count of message skeletons', () => {
      const { container } = render(<MessageListSkeleton count={5} />);

      const messages = container.querySelectorAll('.border-l-2');
      expect(messages.length).toBe(5);
    });

    it('has proper spacing between messages', () => {
      const { container } = render(<MessageListSkeleton />);

      const wrapper = container.querySelector('.space-y-4');
      expect(wrapper).toBeInTheDocument();
    });
  });

  describe('Skeleton animations', () => {
    it('all skeletons have pulse animation', () => {
      const { container: threadContainer } = render(<ThreadListSkeleton />);
      const { container: agentContainer } = render(<AgentListSkeleton />);
      const { container: messageContainer } = render(<MessageSkeleton />);

      const threadSkeletons = threadContainer.querySelectorAll('.animate-pulse');
      const agentSkeletons = agentContainer.querySelectorAll('.animate-pulse');
      const messageSkeletons = messageContainer.querySelectorAll('.animate-pulse');

      expect(threadSkeletons).toHaveLength(10);
      expect(agentSkeletons).toHaveLength(9);
      expect(messageSkeletons).toHaveLength(3);
    });
  });
});
