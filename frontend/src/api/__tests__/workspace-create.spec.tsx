import React from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';

import { get, post } from '../client';
import { workspaceKeys } from '../queryKeys';
import { useCreateWorkspace, useWorkspaces } from '../workspaces';

// The hooks are the real ones, on a real query client; only the HTTP calls
// under them are stubbed.
jest.mock('../client', () => ({
  apiFetch: jest.fn(),
  get: jest.fn(),
  post: jest.fn(),
  put: jest.fn(),
  del: jest.fn(),
}));

beforeEach(() => {
  jest.clearAllMocks();
});

// Issue #129: the workspace count in the navigation, which reads the
// unfiltered list, stayed stale after a workspace was created. Creating one
// has to read that list again, and the count has to follow it.
it('reads the unfiltered workspace list again after creating a workspace', async () => {
  const listed = [{ metadata: { name: 'default' } }];
  (get as jest.Mock).mockImplementation(() => Promise.resolve([...listed]));
  (post as jest.Mock).mockImplementation(() => {
    const created = { metadata: { name: 'team-a' } };
    listed.push(created);
    return Promise.resolve(created);
  });

  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const wrapper: React.FC<{ children: React.ReactNode }> = ({ children }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );

  const { result } = renderHook(
    () => ({
      // What the navigation mounts for its count.
      workspaces: useWorkspaces(undefined, { refetchInterval: 0 }),
      create: useCreateWorkspace(),
    }),
    { wrapper },
  );
  await waitFor(() => expect(result.current.workspaces.data).toHaveLength(1));
  expect(client.getQueryData(workspaceKeys.list())).toHaveLength(1);
  expect(get).toHaveBeenCalledTimes(1);
  expect(get).toHaveBeenLastCalledWith('/api/v1/workspaces');

  await act(async () => {
    await result.current.create.mutateAsync({ name: 'team-a' });
  });

  await waitFor(() => expect(result.current.workspaces.data).toHaveLength(2));
  expect(get).toHaveBeenCalledTimes(2);
  expect(get).toHaveBeenLastCalledWith('/api/v1/workspaces');
});
