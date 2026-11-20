// Mock data for every API surface the dashboard reads.
// Timestamps are relative to "now" so the UI always looks fresh.

// Typed against the dashboard's own types, so a fixture that drifts from
// what the BFF answers fails `npm run typecheck`.
import type {
  AuthConfig,
  CredentialRefreshStatus,
  CurrentUser,
  DraftPolicy,
  DraftSummary,
  EffectivePolicy,
  GatewayCompatibilityInfo,
  GatewayInfo,
  GatewaySettings,
  LogLine,
  Provider,
  ProviderProfile,
  Sandbox,
  SandboxLogs,
  SandboxPhase,
  SandboxPolicyView,
  SandboxTemplate,
  ServiceEndpoint,
  Workspace,
  WorkspaceMember,
} from '../src/types';

const now = Date.now();
const minutes = (n: number) => n * 60_000;
const hours = (n: number) => n * 60 * 60_000;
const days = (n: number) => n * 24 * 60 * 60_000;

// ---------------------------------------------------------------------------
// Auth
// ---------------------------------------------------------------------------

export const authConfig: AuthConfig = {
  authDisabled: true,
  adminRole: 'admin',
  features: {
    terminal: true,
    fileTransfer: true,
    settings: true,
    globalPolicy: true,
    credentialRefresh: true,
    services: true,
    draftPolicy: true,
  },
};

export const currentUser: CurrentUser = {
  subject: 'dev-user',
  displayName: 'Platform Admin',
  roles: ['admin', 'user'],
  scopes: ['openid', 'profile', 'email'],
};

// ---------------------------------------------------------------------------
// Gateway
// ---------------------------------------------------------------------------

export const gatewayInfo: GatewayInfo = {
  status: 'HEALTHY',
  gatewayVersion: '0.1.2',
  computeDrivers: [
    { name: 'podman', driverName: 'podman', driverVersion: '5.4.0' },
    { name: 'kubernetes', driverName: 'kubernetes', driverVersion: '1.31.2' },
  ],
  extensions: [
    {
      kind: 'COMPUTE_DRIVER',
      configuredName: 'podman',
      implementationName: 'podman-driver',
      implementationVersion: '0.1.2',
      protocolMajor: 1,
      protocolMinor: 0,
    },
    {
      kind: 'COMPUTE_DRIVER',
      configuredName: 'kubernetes',
      implementationName: 'kubernetes-driver',
      implementationVersion: '0.1.2',
      protocolMajor: 1,
      protocolMinor: 0,
    },
    {
      kind: 'GATEWAY_INTERCEPTOR',
      configuredName: 'nvidia-nim',
      implementationName: 'nvidia-nim-interceptor',
      implementationVersion: '0.1.0',
      protocolMajor: 1,
      protocolMinor: 0,
      supportedCapabilities: ['credential_injection', 'profile_vending'],
    },
  ],
  compatibility: {
    status: 'supported',
    supportedLine: '0.1',
  },
};

export const gatewayCompatibility: GatewayCompatibilityInfo = {
  gatewayVersion: '0.1.2',
  healthy: true,
  compatibility: {
    status: 'supported',
    supportedLine: '0.1',
  },
};

// ---------------------------------------------------------------------------
// Workspaces
// ---------------------------------------------------------------------------

const mkMeta = (
  name: string,
  workspace?: string,
  opts: {
    id?: string;
    labels?: Record<string, string>;
    createdAgo?: number;
    resourceVersion?: number;
  } = {},
) => ({
  id: opts.id ?? `${workspace ? workspace + '/' : ''}${name}`,
  name,
  ...(workspace ? { workspace } : {}),
  labels: opts.labels ?? {},
  annotations: {},
  createdAtMs: now - (opts.createdAgo ?? days(7)),
  resourceVersion: opts.resourceVersion ?? 1,
});

export const workspaces: Workspace[] = [
  {
    metadata: mkMeta('default', undefined, { createdAgo: days(30) }),
    phase: 'ACTIVE',
  },
  {
    metadata: mkMeta('ml-training', undefined, {
      createdAgo: days(14),
      labels: { team: 'ml-platform' },
    }),
    phase: 'ACTIVE',
  },
  {
    metadata: mkMeta('dev-sandbox', undefined, {
      createdAgo: days(5),
      labels: { team: 'devtools' },
    }),
    phase: 'ACTIVE',
  },
  {
    metadata: mkMeta('staging', undefined, {
      createdAgo: days(2),
      labels: { env: 'staging' },
    }),
    phase: 'ACTIVE',
  },
];

export const workspaceDetail: Record<string, Workspace> = {};
workspaces.forEach((ws) => {
  workspaceDetail[ws.metadata.name] = ws;
});

// ---------------------------------------------------------------------------
// Members (per workspace)
// ---------------------------------------------------------------------------

export const members: Record<string, WorkspaceMember[]> = {
  default: [
    {
      metadata: mkMeta('admin@test', 'default', { createdAgo: days(30) }),
      principalSubject: 'admin@test',
      role: 'ADMIN',
    },
    {
      metadata: mkMeta('alice@dev', 'default', { createdAgo: days(20) }),
      principalSubject: 'alice@dev',
      role: 'USER',
    },
    {
      metadata: mkMeta('bob@dev', 'default', { createdAgo: days(15) }),
      principalSubject: 'bob@dev',
      role: 'USER',
    },
  ],
  'ml-training': [
    {
      metadata: mkMeta('admin@test', 'ml-training', { createdAgo: days(14) }),
      principalSubject: 'admin@test',
      role: 'ADMIN',
    },
    {
      metadata: mkMeta('carol@ml', 'ml-training', { createdAgo: days(10) }),
      principalSubject: 'carol@ml',
      role: 'ADMIN',
    },
    {
      metadata: mkMeta('dave@ml', 'ml-training', { createdAgo: days(8) }),
      principalSubject: 'dave@ml',
      role: 'USER',
    },
  ],
  'dev-sandbox': [
    {
      metadata: mkMeta('admin@test', 'dev-sandbox', { createdAgo: days(5) }),
      principalSubject: 'admin@test',
      role: 'ADMIN',
    },
    {
      metadata: mkMeta('eve@devtools', 'dev-sandbox', { createdAgo: days(3) }),
      principalSubject: 'eve@devtools',
      role: 'USER',
    },
  ],
  staging: [
    {
      metadata: mkMeta('admin@test', 'staging', { createdAgo: days(2) }),
      principalSubject: 'admin@test',
      role: 'ADMIN',
    },
  ],
};

// ---------------------------------------------------------------------------
// Providers
// ---------------------------------------------------------------------------

const mkProviders = (workspace: string): Provider[] => [
  {
    metadata: mkMeta('openai', workspace, {
      createdAgo: days(25),
      labels: { tier: 'production' },
    }),
    type: 'openai',
    config: { model: 'gpt-4o' },
    credentialNames: ['OPENAI_API_KEY'],
    credentialExpiresAtMs: { OPENAI_API_KEY: now + days(60) },
    profileWorkspace: '',
  },
  {
    metadata: mkMeta('anthropic', workspace, {
      createdAgo: days(20),
      labels: { tier: 'production' },
    }),
    type: 'anthropic',
    config: {},
    credentialNames: ['ANTHROPIC_API_KEY'],
    credentialExpiresAtMs: { ANTHROPIC_API_KEY: now + days(45) },
    profileWorkspace: '',
  },
  {
    metadata: mkMeta('github', workspace, {
      createdAgo: days(15),
      labels: { category: 'source-control' },
    }),
    type: 'github',
    config: {},
    credentialNames: ['GITHUB_TOKEN'],
    credentialExpiresAtMs: { GITHUB_TOKEN: now + days(30) },
    profileWorkspace: '',
  },
];

export const providers: Record<string, Provider[]> = {
  default: mkProviders('default'),
  'ml-training': [
    ...mkProviders('ml-training'),
    {
      metadata: mkMeta('nvidia-nim', 'ml-training', {
        createdAgo: days(7),
        labels: { tier: 'production', category: 'inference' },
      }),
      type: 'nvidia-nim',
      config: { endpoint: 'https://integrate.api.nvidia.com' },
      credentialNames: ['NVIDIA_API_KEY'],
      credentialExpiresAtMs: { NVIDIA_API_KEY: now + days(90) },
      profileWorkspace: '',
    },
  ],
  'dev-sandbox': mkProviders('dev-sandbox').slice(0, 2),
  staging: mkProviders('staging').slice(0, 1),
};

export const allProviders = Object.values(providers).flat();

// ---------------------------------------------------------------------------
// Provider profiles (platform scope)
// ---------------------------------------------------------------------------

export const platformProfiles: ProviderProfile[] = [
  {
    id: 'openai',
    displayName: 'OpenAI',
    description: 'OpenAI API — GPT-4o, GPT-4o-mini, o1',
    category: 'INFERENCE',
    credentials: [
      {
        name: 'api_key',
        description: 'OpenAI API key',
        envVars: ['OPENAI_API_KEY'],
        required: true,
        authStyle: 'header',
        headerName: 'Authorization',
      },
    ],
    endpoints: ['api.openai.com:443'],
    networkEndpoints: [
      { host: 'api.openai.com', port: 443, protocol: 'HTTPS' },
    ],
    inferenceCapable: true,
    source: 'interceptor/openai',
    scope: 'platform',
    resourceVersion: 1,
  },
  {
    id: 'anthropic',
    displayName: 'Anthropic',
    description: 'Anthropic API — Claude 4 Opus, Claude 4 Sonnet',
    category: 'INFERENCE',
    credentials: [
      {
        name: 'api_key',
        description: 'Anthropic API key',
        envVars: ['ANTHROPIC_API_KEY'],
        required: true,
        authStyle: 'header',
        headerName: 'x-api-key',
      },
    ],
    endpoints: ['api.anthropic.com:443'],
    networkEndpoints: [
      { host: 'api.anthropic.com', port: 443, protocol: 'HTTPS' },
    ],
    inferenceCapable: true,
    source: 'interceptor/anthropic',
    scope: 'platform',
    resourceVersion: 1,
  },
  {
    id: 'github',
    displayName: 'GitHub',
    description: 'GitHub API — source control and issue tracking',
    category: 'SOURCE_CONTROL',
    credentials: [
      {
        name: 'token',
        description: 'GitHub personal access token',
        envVars: ['GITHUB_TOKEN'],
        required: true,
        authStyle: 'header',
        headerName: 'Authorization',
      },
    ],
    endpoints: ['api.github.com:443'],
    networkEndpoints: [
      { host: 'api.github.com', port: 443, protocol: 'HTTPS' },
    ],
    inferenceCapable: false,
    source: 'user',
    scope: 'platform',
    resourceVersion: 1,
  },
  {
    id: 'nvidia-nim',
    displayName: 'NVIDIA NIM',
    description: 'NVIDIA Inference Microservices — Llama, Mixtral, Nemotron',
    category: 'INFERENCE',
    credentials: [
      {
        name: 'api_key',
        description: 'NVIDIA API key',
        envVars: ['NVIDIA_API_KEY'],
        required: true,
        authStyle: 'header',
        headerName: 'Authorization',
      },
    ],
    endpoints: ['integrate.api.nvidia.com:443'],
    networkEndpoints: [
      { host: 'integrate.api.nvidia.com', port: 443, protocol: 'HTTPS' },
    ],
    inferenceCapable: true,
    source: 'interceptor/nvidia-nim',
    scope: 'platform',
    resourceVersion: 1,
  },
  {
    id: 'slack',
    displayName: 'Slack',
    description: 'Slack API — messaging and notifications',
    category: 'MESSAGING',
    credentials: [
      {
        name: 'bot_token',
        description: 'Slack bot token',
        envVars: ['SLACK_BOT_TOKEN'],
        required: true,
        authStyle: 'header',
        headerName: 'Authorization',
      },
    ],
    endpoints: ['slack.com:443'],
    networkEndpoints: [{ host: 'slack.com', port: 443, protocol: 'HTTPS' }],
    inferenceCapable: false,
    source: 'user',
    scope: 'platform',
    resourceVersion: 1,
  },
];

// ---------------------------------------------------------------------------
// Sandboxes
// ---------------------------------------------------------------------------

const mkSandbox = (
  name: string,
  workspace: string,
  phase: SandboxPhase,
  opts: {
    image?: string;
    labels?: Record<string, string>;
    createdAgo?: number;
    providers?: string[];
    exitCode?: number;
    policyVersion?: number;
  } = {},
): Sandbox => ({
  metadata: mkMeta(name, workspace, {
    createdAgo: opts.createdAgo ?? days(1),
    labels: opts.labels,
    resourceVersion: 2,
  }),
  spec: {
    image:
      opts.image ??
      'ghcr.io/nvidia/openshell-community/sandboxes/default:latest',
    providers: opts.providers ?? ['openai'],
    policy: {
      version: opts.policyVersion ?? 1,
      networkPolicies: {
        openai: {
          name: 'openai',
          endpoints: [{ host: 'api.openai.com', port: 443, protocol: 'HTTPS' }],
        },
      },
    },
    environment: {},
  },
  status: {
    phase,
    currentPolicyVersion: opts.policyVersion ?? 1,
    ...(opts.exitCode !== undefined ? { exitCode: opts.exitCode } : {}),
    conditions:
      phase === 'ERROR'
        ? [
            {
              type: 'Ready',
              status: 'False',
              reason: 'CrashLoopBackOff',
              message: 'Container exited with code 1',
            },
          ]
        : [],
    endpointStatuses:
      phase === 'READY'
        ? [
            {
              endpointId: 'openai-0',
              host: 'api.openai.com',
              ports: [443],
              lastResult: 'HTTP_RESPONSE_RECEIVED',
              lastReportedAt: new Date(now - minutes(2)).toISOString(),
            },
          ]
        : [],
  },
});

export const sandboxes: Record<string, Sandbox[]> = {
  default: [
    mkSandbox('code-assistant', 'default', 'READY', {
      createdAgo: days(3),
      providers: ['openai', 'github'],
      labels: { purpose: 'coding' },
      policyVersion: 3,
    }),
    mkSandbox('data-analysis', 'default', 'READY', {
      createdAgo: days(2),
      providers: ['openai'],
      labels: { purpose: 'analytics' },
    }),
    mkSandbox('web-scraper', 'default', 'STOPPED', {
      createdAgo: days(5),
      providers: [],
      labels: { purpose: 'data-collection' },
    }),
  ],
  'ml-training': [
    mkSandbox('training-run-42', 'ml-training', 'READY', {
      image: 'ghcr.io/nvidia/openshell-community/sandboxes/pytorch:latest',
      createdAgo: hours(6),
      providers: ['nvidia-nim', 'openai'],
      labels: { experiment: 'v42', gpu: 'true' },
      policyVersion: 2,
    }),
    mkSandbox('eval-pipeline', 'ml-training', 'READY', {
      createdAgo: hours(12),
      providers: ['openai', 'anthropic'],
      labels: { purpose: 'evaluation' },
    }),
    // Suspended: its memory state is kept, on a gateway whose compute driver
    // can do that. The phase and the Resume action are seen nowhere else
    // without such a gateway.
    mkSandbox('long-eval', 'ml-training', 'SUSPENDED', {
      createdAgo: days(2),
      providers: ['openai'],
      labels: { purpose: 'evaluation' },
    }),
    mkSandbox('notebook-dev', 'ml-training', 'PROVISIONING', {
      image: 'ghcr.io/nvidia/openshell-community/sandboxes/jupyter:latest',
      createdAgo: minutes(5),
      providers: ['nvidia-nim'],
      labels: { purpose: 'exploration' },
    }),
    mkSandbox('failed-experiment', 'ml-training', 'ERROR', {
      createdAgo: days(1),
      providers: ['openai'],
      labels: { experiment: 'v41' },
      exitCode: 1,
    }),
  ],
  'dev-sandbox': [
    mkSandbox('my-sandbox', 'dev-sandbox', 'READY', {
      createdAgo: hours(4),
      providers: ['openai', 'anthropic'],
      labels: { owner: 'eve' },
    }),
    mkSandbox('test-env', 'dev-sandbox', 'STOPPING', {
      createdAgo: hours(8),
      providers: ['openai'],
    }),
  ],
  staging: [
    mkSandbox('integration-test', 'staging', 'COMPLETED', {
      createdAgo: hours(2),
      providers: ['openai'],
      exitCode: 0,
    }),
  ],
};

export const allSandboxes = Object.values(sandboxes).flat();

// ---------------------------------------------------------------------------
// Sandbox logs
// ---------------------------------------------------------------------------

const mkLogLines = (sandbox: string): LogLine[] => [
  {
    timestampMs: now - minutes(10),
    level: 'INFO',
    target: 'openshell::supervisor',
    message: `Sandbox ${sandbox} started`,
    source: 'gateway',
  },
  {
    timestampMs: now - minutes(9),
    level: 'INFO',
    target: 'openshell::policy',
    message: 'Policy version 1 loaded successfully',
    source: 'gateway',
  },
  {
    timestampMs: now - minutes(8),
    level: 'INFO',
    target: 'sandbox',
    message: 'Initializing sandbox environment...',
    source: 'sandbox',
  },
  {
    timestampMs: now - minutes(7),
    level: 'INFO',
    target: 'sandbox',
    message: 'Installing dependencies from requirements.txt',
    source: 'sandbox',
  },
  {
    timestampMs: now - minutes(6),
    level: 'DEBUG',
    target: 'openshell::network',
    message: 'Endpoint api.openai.com:443 — HTTP_RESPONSE_RECEIVED',
    source: 'gateway',
    fields: { dst_host: 'api.openai.com', dst_port: '443', action: 'allow' },
  },
  {
    timestampMs: now - minutes(5),
    level: 'INFO',
    target: 'sandbox',
    message: 'Connected to OpenAI API successfully',
    source: 'sandbox',
  },
  {
    timestampMs: now - minutes(4),
    level: 'WARN',
    target: 'openshell::network',
    message: 'Rate limit approaching for api.openai.com',
    source: 'gateway',
    fields: { dst_host: 'api.openai.com', remaining: '12' },
  },
  {
    timestampMs: now - minutes(3),
    level: 'INFO',
    target: 'sandbox',
    message: 'Processing batch 1 of 5...',
    source: 'sandbox',
  },
  {
    timestampMs: now - minutes(2),
    level: 'INFO',
    target: 'sandbox',
    message: 'Batch 1 complete — 200 items processed',
    source: 'sandbox',
  },
  {
    timestampMs: now - minutes(1),
    level: 'INFO',
    target: 'sandbox',
    message: 'Processing batch 2 of 5...',
    source: 'sandbox',
  },
];

export const sandboxLogs = (sandbox: string): SandboxLogs => ({
  logs: mkLogLines(sandbox),
  bufferTotal: 10,
});

// ---------------------------------------------------------------------------
// Sandbox policy
// ---------------------------------------------------------------------------

export const sandboxPolicy = (workspace: string): SandboxPolicyView => ({
  activeVersion: 1,
  latest: {
    version: 1,
    policyHash: 'abc123def456',
    status: 'LOADED',
    createdAtMs: now - days(1),
    loadedAtMs: now - days(1) + 2000,
    policy: {
      version: 1,
      networkPolicies: {
        openai: {
          name: 'openai',
          endpoints: [
            {
              host: 'api.openai.com',
              port: 443,
              protocol: 'HTTPS',
              enforcement: 'NETWORK_ENFORCEMENT_MODE_ENFORCE',
              access: 'NETWORK_ACCESS_PRESET_FULL',
            },
          ],
        },
        ...(workspace === 'ml-training'
          ? {
              'nvidia-nim': {
                name: 'nvidia-nim',
                endpoints: [
                  {
                    host: 'integrate.api.nvidia.com',
                    port: 443,
                    protocol: 'HTTPS',
                    enforcement: 'NETWORK_ENFORCEMENT_MODE_ENFORCE',
                    access: 'NETWORK_ACCESS_PRESET_FULL',
                  },
                ],
              },
            }
          : {}),
      },
    },
  },
  revisions: [
    {
      version: 1,
      policyHash: 'abc123def456',
      status: 'LOADED',
      createdAtMs: now - days(1),
      loadedAtMs: now - days(1) + 2000,
    },
  ],
});

// ---------------------------------------------------------------------------
// Effective policy
// ---------------------------------------------------------------------------

export const effectivePolicy: EffectivePolicy = {
  policy: {
    version: 1,
    networkPolicies: {
      openai: {
        name: 'openai',
        endpoints: [
          {
            host: 'api.openai.com',
            port: 443,
            protocol: 'HTTPS',
            enforcement: 'NETWORK_ENFORCEMENT_MODE_ENFORCE',
            access: 'NETWORK_ACCESS_PRESET_FULL',
          },
        ],
      },
    },
  },
  version: 1,
  policyHash: 'abc123def456',
  policySource: 'SANDBOX',
};

// ---------------------------------------------------------------------------
// Global policy
// ---------------------------------------------------------------------------

export const globalPolicy: SandboxPolicyView = {
  activeVersion: 0,
  revisions: [],
};

// ---------------------------------------------------------------------------
// Draft policy
// ---------------------------------------------------------------------------

export const draftPolicy: DraftPolicy = {
  chunks: [
    {
      id: 'draft-001',
      // The gateway pins an approval to the candidate it evaluated with this
      // token, and approve-all skips a chunk sent without one (hard fact 25).
      reviewToken: 'mock-review-token-001',
      status: 'pending',
      ruleName: 'pypi-access',
      proposedRule: {
        name: 'pypi-access',
        endpoints: [{ host: 'pypi.org', port: 443, protocol: 'HTTPS' }],
      },
      rationale:
        'The sandbox attempted to install packages from PyPI. Allowing access to pypi.org enables pip install.',
      securityNotes:
        'PyPI is a public package registry. Packages are user-uploaded and may contain malicious code.',
      confidence: 0.85,
      createdAtMs: now - minutes(30),
      hitCount: 7,
      validationResult: 'VALID',
      firstSeenMs: now - minutes(45),
      lastSeenMs: now - minutes(5),
      stage: 'initial',
    },
    {
      id: 'draft-002',
      reviewToken: 'mock-review-token-002',
      status: 'pending',
      ruleName: 'huggingface-hub',
      proposedRule: {
        name: 'huggingface-hub',
        endpoints: [{ host: 'huggingface.co', port: 443, protocol: 'HTTPS' }],
      },
      rationale: 'Model weights download from Hugging Face Hub.',
      confidence: 0.92,
      createdAtMs: now - minutes(20),
      hitCount: 3,
      validationResult: 'VALID',
      firstSeenMs: now - minutes(25),
      lastSeenMs: now - minutes(10),
      stage: 'initial',
    },
  ],
  rollingSummary:
    'The sandbox is installing Python packages and downloading ML model weights.',
  draftVersion: 2,
  lastAnalyzedAtMs: now - minutes(5),
};

export const draftSummary: DraftSummary = {
  sandboxes: [
    {
      workspace: 'default',
      sandboxName: 'code-assistant',
      pendingCount: 2,
      hasSecurityFlags: false,
      latestDraftMs: now - minutes(20),
      latestRationale: 'Model weights download from Hugging Face Hub.',
    },
    {
      workspace: 'ml-training',
      sandboxName: 'training-run-42',
      pendingCount: 2,
      hasSecurityFlags: true,
      latestDraftMs: now - minutes(10),
      latestRationale:
        'The sandbox attempted to install packages from PyPI. Allowing access to pypi.org enables pip install.',
    },
  ],
  totalPending: 4,
};

export const workspaceDraftSummary: Record<string, DraftSummary> = {
  default: {
    sandboxes: [
      {
        workspace: 'default',
        sandboxName: 'code-assistant',
        pendingCount: 2,
        hasSecurityFlags: false,
        latestDraftMs: now - minutes(20),
        latestRationale: 'Model weights download from Hugging Face Hub.',
      },
    ],
    totalPending: 2,
  },
  'ml-training': {
    sandboxes: [
      {
        workspace: 'ml-training',
        sandboxName: 'training-run-42',
        pendingCount: 2,
        hasSecurityFlags: true,
        latestDraftMs: now - minutes(10),
        latestRationale:
          'The sandbox attempted to install packages from PyPI. Allowing access to pypi.org enables pip install.',
      },
    ],
    totalPending: 2,
  },
  'dev-sandbox': { sandboxes: [], totalPending: 0 },
  staging: { sandboxes: [], totalPending: 0 },
};

// ---------------------------------------------------------------------------
// Gateway settings
// ---------------------------------------------------------------------------

export const globalSettings: GatewaySettings = {
  settings: [
    {
      key: 'default_sandbox_image',
      value: 'ghcr.io/nvidia/openshell-community/sandboxes/default:latest',
    },
    { key: 'max_sandboxes_per_workspace', value: 50 },
    { key: 'allow_driver_config', value: false },
    { key: 'sandbox_idle_timeout_seconds', value: 3600 },
    { key: 'policy_validation_failure_mode', value: 'fail_closed' },
  ],
  settingsRevision: 3,
};

// ---------------------------------------------------------------------------
// Sandbox settings
// ---------------------------------------------------------------------------

export const sandboxSettings = {
  settings: [
    { key: 'proposal_approval_mode', value: 'manual', scope: 'SANDBOX' },
    { key: 'sandbox_idle_timeout_seconds', value: 3600, scope: 'GLOBAL' },
    {
      key: 'policy_validation_failure_mode',
      value: 'fail_closed',
      scope: 'GLOBAL',
    },
  ],
  policySource: 'SANDBOX',
  policyHash: 'abc123def456',
  policyVersion: 1,
  globalPolicyVersion: 0,
  configRevision: '12345678901234',
  providerEnvRevision: '98765432109876',
};

// ---------------------------------------------------------------------------
// Sandbox templates
// ---------------------------------------------------------------------------

export const templates: Record<string, SandboxTemplate[]> = {
  default: [
    {
      metadata: mkMeta('python-dev', 'default', {
        createdAgo: days(20),
        labels: { language: 'python' },
      }),
      spec: {
        workload: {
          image: 'ghcr.io/nvidia/openshell-community/sandboxes/python:latest',
          environment: { PYTHON_VERSION: '3.12' },
          resources: { cpu: '2', memory: '4Gi' },
        },
      },
    },
    {
      metadata: mkMeta('node-dev', 'default', {
        createdAgo: days(18),
        labels: { language: 'nodejs' },
      }),
      spec: {
        workload: {
          image: 'ghcr.io/nvidia/openshell-community/sandboxes/node:latest',
          environment: { NODE_VERSION: '22' },
          resources: { cpu: '2', memory: '2Gi' },
        },
      },
    },
  ],
  'ml-training': [
    {
      metadata: mkMeta('pytorch-gpu', 'ml-training', {
        createdAgo: days(10),
        labels: { framework: 'pytorch', gpu: 'true' },
      }),
      spec: {
        workload: {
          image: 'ghcr.io/nvidia/openshell-community/sandboxes/pytorch:latest',
          resources: { cpu: '8', memory: '32Gi', gpu: { count: 1 } },
        },
      },
    },
    {
      metadata: mkMeta('jupyter-notebook', 'ml-training', {
        createdAgo: days(8),
        labels: { purpose: 'exploration' },
      }),
      spec: {
        workload: {
          image: 'ghcr.io/nvidia/openshell-community/sandboxes/jupyter:latest',
          resources: { cpu: '4', memory: '8Gi' },
        },
      },
    },
  ],
  'dev-sandbox': [],
  staging: [],
};

export const allTemplates = Object.values(templates).flat();

// ---------------------------------------------------------------------------
// Services
// ---------------------------------------------------------------------------

export const services: Record<string, ServiceEndpoint[]> = {
  default: [
    {
      id: 'svc-1',
      workspace: 'default',
      sandboxId: 'code-assistant',
      sandboxName: 'code-assistant',
      serviceName: '',
      targetPort: 8080,
      domain: true,
      url: 'https://code-assistant.default.sandbox.openshell.dev',
    },
  ],
  'ml-training': [
    {
      id: 'svc-2',
      workspace: 'ml-training',
      sandboxId: 'training-run-42',
      sandboxName: 'training-run-42',
      serviceName: 'tensorboard',
      targetPort: 6006,
      domain: true,
      url: 'https://training-run-42-tensorboard.ml-training.sandbox.openshell.dev',
    },
    {
      id: 'svc-3',
      workspace: 'ml-training',
      sandboxId: 'notebook-dev',
      sandboxName: 'notebook-dev',
      serviceName: 'jupyter',
      targetPort: 8888,
      domain: true,
      url: 'https://notebook-dev-jupyter.ml-training.sandbox.openshell.dev',
    },
  ],
  'dev-sandbox': [
    {
      id: 'svc-4',
      workspace: 'dev-sandbox',
      sandboxId: 'my-sandbox',
      sandboxName: 'my-sandbox',
      serviceName: 'dev-server',
      targetPort: 3000,
      domain: true,
      url: 'https://my-sandbox-dev-server.dev-sandbox.sandbox.openshell.dev',
    },
  ],
  staging: [],
};

export const allServices = Object.values(services).flat();

// ---------------------------------------------------------------------------
// Credential refresh status
// ---------------------------------------------------------------------------

export const refreshStatus: Record<string, CredentialRefreshStatus[]> = {
  openai: [
    {
      credentialKey: 'OPENAI_API_KEY',
      strategy: 'static',
      status: 'active',
      expiresAtMs: now + days(60),
    },
  ],
  anthropic: [
    {
      credentialKey: 'ANTHROPIC_API_KEY',
      strategy: 'static',
      status: 'active',
      expiresAtMs: now + days(45),
    },
  ],
  github: [
    {
      credentialKey: 'GITHUB_TOKEN',
      strategy: 'static',
      status: 'active',
      expiresAtMs: now + days(30),
    },
  ],
  'nvidia-nim': [
    {
      credentialKey: 'NVIDIA_API_KEY',
      strategy: 'static',
      status: 'active',
      expiresAtMs: now + days(90),
    },
  ],
};

// ---------------------------------------------------------------------------
// Sandbox attached providers
// ---------------------------------------------------------------------------

export const attachedProviders = (
  workspace: string,
  sandbox: string,
): Provider[] => {
  const sbx = (sandboxes[workspace] ?? []).find(
    (s) => s.metadata.name === sandbox,
  );
  if (!sbx) return [];
  const attached = sbx.spec.providers ?? [];
  return (providers[workspace] ?? []).filter((p) =>
    attached.includes(p.metadata.name),
  );
};
