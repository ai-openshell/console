import {
  keepPreviousData,
  useMutation,
  useQuery,
  useQueryClient,
} from '@tanstack/react-query';
import type { QueryClient } from '@tanstack/react-query';

import { SANDBOX_POLL_MS } from '../constants';
import {
  apiAuthHeaders,
  apiUrl,
  del,
  get,
  openEventSource,
  post,
  sessionExpiredError,
} from './client';
import { policyKeys, sandboxKeys } from './queryKeys';
import { pagedPath } from './pagination';
import type { ListPage, PageRequest } from './pagination';
import {
  LogEvent,
  type CreateSandboxRequest,
  type DeleteResult,
  type ExposeServiceRequest,
  type LogFilters,
  type LogLine,
  type Provider,
  type Sandbox,
  type SandboxLogs,
  type ServiceEndpoint,
} from '../types';

export const listSandboxes = (
  workspace: string,
  labelSelector?: string,
): Promise<Sandbox[]> =>
  get<Sandbox[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes${
      labelSelector ? `?labelSelector=${encodeURIComponent(labelSelector)}` : ''
    }`,
  );

export const listSandboxPage = (
  workspace: string,
  request: PageRequest,
): Promise<ListPage<Sandbox>> =>
  get<ListPage<Sandbox>>(
    pagedPath(
      `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes`,
      request,
    ),
  );

export const useSandboxPage = (workspace: string, request: PageRequest) =>
  useQuery({
    queryKey: sandboxKeys.page(workspace, request),
    queryFn: () => listSandboxPage(workspace, request),
    placeholderData: keepPreviousData,
    refetchInterval: SANDBOX_POLL_MS,
  });

export const getSandbox = (workspace: string, name: string): Promise<Sandbox> =>
  get<Sandbox>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}`,
  );

export const createSandbox = (
  workspace: string,
  body: CreateSandboxRequest,
): Promise<Sandbox> =>
  post<Sandbox>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes`,
    body,
  );

// The answer says what the gateway did, and it has often only accepted the
// delete: see DeletionOutcome.
export const deleteSandbox = (
  workspace: string,
  name: string,
): Promise<DeleteResult> =>
  del<DeleteResult>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}`,
  );

export const stopSandbox = (
  workspace: string,
  name: string,
): Promise<Sandbox> =>
  post<Sandbox>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/stop`,
  );

export const startSandbox = (
  workspace: string,
  name: string,
): Promise<Sandbox> =>
  post<Sandbox>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/start`,
  );

export const suspendSandbox = (
  workspace: string,
  name: string,
): Promise<Sandbox> =>
  post<Sandbox>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/suspend`,
  );

// ResumeSandboxRequest.restart_if_restore_fails: whether the sandbox may boot
// cleanly when its memory state cannot be restored. Left out, a resume that
// cannot restore fails and the sandbox stays suspended.
export type ResumeSandboxOptions = {
  restartIfRestoreFails?: boolean;
};

export const resumeSandbox = (
  workspace: string,
  name: string,
  options?: ResumeSandboxOptions,
): Promise<Sandbox> =>
  post<Sandbox>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/resume`,
    options,
  );

export const useSandboxes = (workspace: string, labelSelector?: string) =>
  useQuery({
    queryKey: sandboxKeys.list(workspace, labelSelector),
    queryFn: () => listSandboxes(workspace, labelSelector),
    refetchInterval: SANDBOX_POLL_MS,
  });

export const useSandbox = (workspace: string, name: string) =>
  useQuery({
    queryKey: sandboxKeys.detail(workspace, name),
    queryFn: () => getSandbox(workspace, name),
    refetchInterval: SANDBOX_POLL_MS,
  });

export const useCreateSandbox = (workspace: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: CreateSandboxRequest) => createSandbox(workspace, body),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: sandboxKeys.scope(workspace) }),
  });
};

// One-shot log fetch; the UI polls this (no streaming through the BFF).
export const getSandboxLogs = (
  workspace: string,
  name: string,
  filters: LogFilters = {},
): Promise<SandboxLogs> => {
  const params = new URLSearchParams();
  if (filters.lines) {
    params.set('lines', String(filters.lines));
  }
  if (filters.sinceMs) {
    params.set('sinceMs', String(filters.sinceMs));
  } else if (filters.sinceDurationMs) {
    // A window that ends now, resolved when the request is sent: each poll
    // of the same filters moves it forward.
    params.set('sinceMs', String(Date.now() - filters.sinceDurationMs));
  }
  for (const source of filters.sources ?? []) {
    params.append('source', source);
  }
  if (filters.level) {
    params.set('level', filters.level);
  }
  const query = params.toString();
  return get<SandboxLogs>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/logs${query ? `?${query}` : ''}`,
  );
};

export const useSandboxLogs = (
  workspace: string,
  name: string,
  filters: LogFilters,
  autoRefresh: boolean,
  enabled = true,
) =>
  useQuery({
    queryKey: sandboxKeys.logs(workspace, name, filters),
    queryFn: () => getSandboxLogs(workspace, name, filters),
    // A failed request stays stopped until an explicit refetch succeeds.
    enabled: (query) => enabled && query.state.status !== 'error',
    retry: false,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    refetchInterval: (query) =>
      autoRefresh && query.state.status !== 'error' ? SANDBOX_POLL_MS : false,
  });

export const watchSandboxLogs = (
  workspace: string,
  name: string,
  tailLines: number,
  onLog: (line: LogLine) => void,
  onWarning: (message: string) => void,
  onDisconnect: () => void,
): (() => void) => {
  const params = new URLSearchParams({
    lines: String(tailLines),
  });

  const source = openEventSource(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/logs/stream?${params.toString()}`,
  );

  source.addEventListener(LogEvent.Log, (event: MessageEvent<string>) => {
    onLog(JSON.parse(event.data));
  });

  source.addEventListener(LogEvent.Warning, (event: MessageEvent<string>) => {
    onWarning(JSON.parse(event.data));
  });

  const disconnect = () => {
    source.close();
    onDisconnect();
  };

  source.addEventListener(LogEvent.StreamError, disconnect);
  source.addEventListener(LogEvent.End, disconnect);
  source.onerror = disconnect;

  return () => source.close();
};

export const listAttachedProviders = (
  workspace: string,
  name: string,
): Promise<Provider[]> =>
  get<Provider[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/providers`,
  );

export const attachProvider = (
  workspace: string,
  name: string,
  provider: string,
  expectedResourceVersion?: number,
): Promise<{ attached: boolean }> =>
  post<{ attached: boolean }>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/providers/${encodeURIComponent(provider)}`,
    { expectedResourceVersion },
  );

export const detachProvider = (
  workspace: string,
  name: string,
  provider: string,
): Promise<{ detached: boolean }> =>
  del<{ detached: boolean }>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/providers/${encodeURIComponent(provider)}`,
  );

export const useAttachedProviders = (workspace: string, name: string) =>
  useQuery({
    queryKey: sandboxKeys.providers(workspace, name),
    queryFn: () => listAttachedProviders(workspace, name),
  });

// What attaching or detaching a provider changes: the sandbox's list of
// attached providers, the sandbox itself (spec.providers), and its effective
// policy, into which the gateway composes one rule per attached provider.
const invalidateAttachedProviders = (
  queryClient: QueryClient,
  workspace: string,
  name: string,
) => {
  queryClient.invalidateQueries({
    queryKey: sandboxKeys.providers(workspace, name),
  });
  queryClient.invalidateQueries({
    queryKey: sandboxKeys.detail(workspace, name),
  });
  queryClient.invalidateQueries({
    queryKey: policyKeys.effective(workspace, name),
  });
};

export const useAttachProvider = (workspace: string, name: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      provider,
      expectedResourceVersion,
    }: {
      provider: string;
      expectedResourceVersion?: number;
    }) => attachProvider(workspace, name, provider, expectedResourceVersion),
    onSuccess: () => invalidateAttachedProviders(queryClient, workspace, name),
  });
};

export const useDetachProvider = (workspace: string, name: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (provider: string) => detachProvider(workspace, name, provider),
    onSuccess: () => invalidateAttachedProviders(queryClient, workspace, name),
  });
};

export const useDeleteSandbox = (workspace: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => deleteSandbox(workspace, name),
    // The queries are marked for re-reading, and the mutation does not wait
    // for them. The page that deletes a sandbox is usually the one showing
    // it, and a refetch of a sandbox that is gone can only fail: a mutation
    // that waited for it would stay pending through the retry, which React
    // Query holds back for as long as the tab is hidden, and the delete
    // dialog would spin over a sandbox that no longer exists.
    onSuccess: () => {
      void queryClient.invalidateQueries({
        queryKey: sandboxKeys.scope(workspace),
      });
    },
  });
};

export const useStopSandbox = (workspace: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => stopSandbox(workspace, name),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: sandboxKeys.scope(workspace) }),
  });
};

export const useStartSandbox = (workspace: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => startSandbox(workspace, name),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: sandboxKeys.scope(workspace) }),
  });
};

export const useSuspendSandbox = (workspace: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => suspendSandbox(workspace, name),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: sandboxKeys.scope(workspace) }),
  });
};

// Resumes the way `openshell sandbox resume` does: from the memory state the
// sandbox kept, and not at all when that cannot be restored.
export const useResumeSandbox = (workspace: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (name: string) => resumeSandbox(workspace, name),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: sandboxKeys.scope(workspace) }),
  });
};

// --- Service endpoints ---

export const listServices = (
  workspace: string,
  sandbox: string,
): Promise<ServiceEndpoint[]> =>
  get<ServiceEndpoint[]>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(sandbox)}/services`,
  );

export const exposeService = (
  workspace: string,
  sandbox: string,
  body: ExposeServiceRequest,
): Promise<ServiceEndpoint> =>
  post<ServiceEndpoint>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(sandbox)}/services`,
    body,
  );

// An empty service name is the sandbox's unnamed endpoint. It has no name to
// put in the path, so it is deleted on the route that stops at /services.
export const deleteService = (
  workspace: string,
  sandbox: string,
  service: string,
): Promise<{ deleted: boolean }> =>
  del<{ deleted: boolean }>(
    `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(sandbox)}/services${
      service ? `/${encodeURIComponent(service)}` : ''
    }`,
  );

export const useServices = (workspace: string, sandbox: string) =>
  useQuery({
    queryKey: sandboxKeys.services(workspace, sandbox),
    queryFn: () => listServices(workspace, sandbox),
    refetchInterval: SANDBOX_POLL_MS,
  });

export const useExposeService = (workspace: string, sandbox: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: ExposeServiceRequest) =>
      exposeService(workspace, sandbox, body),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: sandboxKeys.services(workspace, sandbox),
      }),
  });
};

export const useDeleteService = (workspace: string, sandbox: string) => {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (service: string) => deleteService(workspace, sandbox, service),
    onSuccess: () =>
      queryClient.invalidateQueries({
        queryKey: sandboxKeys.services(workspace, sandbox),
      }),
  });
};

// --- File upload/download ---

// What became of one file of an upload.
export type UploadedFile = {
  path: string;
  // Bytes sent to the sandbox. For a file that failed it is not how many
  // were written.
  size: number;
  success: boolean;
  // Why the sandbox did not write the file.
  error?: string;
};

// The fields beside files describe the first file of the request, as they did
// when a request was always one file.
export type UploadResult = {
  exitCode: number;
  path: string;
  size: number;
  stdout: string;
  success: boolean;
  files?: UploadedFile[];
};

export type UploadOptions = {
  // Where below the destination directory the file goes, for a file that
  // comes out of a folder: "project/src/main.go". Without it the file lands
  // in the destination directory under its own name.
  relativePath?: string;
  // Called with how much of the request has been sent, from 0 to 1.
  onProgress?: (sent: number) => void;
  // Stops the upload when it is aborted. The promise then rejects with an
  // error named AbortError (see isUploadAborted). What the sandbox had been
  // sent until then may be left there as an incomplete file.
  signal?: AbortSignal;
};

type UploadFailure = {
  code?: string;
  message?: string;
  files?: UploadedFile[];
};

const parseUploadResponse = (text: string): unknown => {
  try {
    return JSON.parse(text);
  } catch {
    return {};
  }
};

const uploadAborted = (): Error =>
  new DOMException('The upload was stopped', 'AbortError');

// Whether an upload failed because it was stopped through its signal.
export const isUploadAborted = (error: unknown): boolean =>
  error instanceof Error && error.name === 'AbortError';

// Uploads one file. XMLHttpRequest and not fetch, which cannot say how much
// of a request has been sent, so the request is put together here from what
// the client gives every request: its URL, its auth headers, and what a 401
// means.
export const uploadFile = async (
  workspace: string,
  name: string,
  file: File,
  dest?: string,
  options: UploadOptions = {},
): Promise<UploadResult> => {
  const { relativePath, onProgress, signal } = options;
  const headers = await apiAuthHeaders();
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(uploadAborted());
      return;
    }
    const formData = new FormData();
    // The path has to arrive before the file it belongs to.
    if (relativePath) {
      formData.append('relativePath', relativePath);
    }
    formData.append('file', file);
    const params = dest ? `?dest=${encodeURIComponent(dest)}` : '';
    const request = new XMLHttpRequest();
    request.open(
      'POST',
      apiUrl(
        `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/files${params}`,
      ),
    );
    Object.entries(headers).forEach(([header, value]) =>
      request.setRequestHeader(header, value),
    );
    const abort = () => request.abort();
    const settled = () => signal?.removeEventListener('abort', abort);
    request.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) {
        onProgress?.(event.loaded / event.total);
      }
    };
    request.onload = () => {
      settled();
      const body = parseUploadResponse(request.responseText);
      if (request.status >= 200 && request.status < 300) {
        resolve(body as UploadResult);
        return;
      }
      const failure = body as UploadFailure;
      if (request.status === 401) {
        reject(sessionExpiredError(failure.code));
        return;
      }
      // The reason the sandbox gave for this file says more than the
      // request's own message.
      reject(
        new Error(
          failure.files?.find((f) => !f.success)?.error ||
            failure.message ||
            `Upload failed (${request.status})`,
        ),
      );
    };
    request.onerror = () => {
      settled();
      reject(new Error('Upload failed: the connection was lost'));
    };
    request.onabort = () => {
      settled();
      reject(uploadAborted());
    };
    signal?.addEventListener('abort', abort);
    request.send(formData);
  });
};

export type DownloadedFile = {
  fileName: string;
  // A directory, which arrives as a tar archive of its contents.
  isArchive: boolean;
  size: number;
};

export const downloadFile = async (
  workspace: string,
  name: string,
  path: string,
): Promise<DownloadedFile> => {
  // The body is read as a blob, which apiFetch does not do, so the request is
  // put together here from what the client gives every request.
  const response = await fetch(
    apiUrl(
      `/api/v1/workspaces/${encodeURIComponent(workspace)}/sandboxes/${encodeURIComponent(name)}/files?path=${encodeURIComponent(path)}`,
    ),
    { headers: await apiAuthHeaders() },
  );
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as {
      code?: string;
      message?: string;
    };
    if (response.status === 401) {
      throw sessionExpiredError(body.code);
    }
    throw new Error(body.message || `Download failed (${response.status})`);
  }
  const isArchive = (response.headers.get('Content-Type') ?? '').startsWith(
    'application/x-tar',
  );
  let blob: Blob;
  try {
    blob = await response.blob();
  } catch {
    // The download is streamed, so the status was sent before the sandbox
    // had finished. A failure after that can only break the connection.
    throw new Error(
      'Download failed part way: the sandbox stopped sending before the end. Nothing was saved.',
    );
  }
  // The last name in the path, with .tar for a directory. The root directory
  // has no name, so its archive takes the sandbox's. The BFF names the
  // attachment the same way.
  const base = path.split('/').filter(Boolean).pop();
  const fileName = isArchive ? `${base || name}.tar` : base || 'download';
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  a.click();
  URL.revokeObjectURL(url);
  return { fileName, isArchive, size: blob.size };
};

export const useUploadFile = (workspace: string, name: string) =>
  useMutation({
    mutationFn: ({ file, dest }: { file: File; dest?: string }) =>
      uploadFile(workspace, name, file, dest),
  });
