// TypeScript interfaces matching the SDK-backed BFF payloads (camelCase,
// protojson-style). The OpenShell Go SDK is the source of truth; see
// .claude/rules/openshell-api.md.

export type ObjectMeta = {
  id: string;
  name: string;
  workspace?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  createdAtMs: number;
  resourceVersion: number;
  deletionTimestampMs?: number;
};

// openshell.v1.DeletionOutcome, as every delete endpoint of the BFF reports it.
// A delete that returns without an error has not always deleted anything yet:
// "accepted" means the gateway queued the cleanup and the resource may still
// be there, which gateway 0.1.2 answers for sandboxes only, and often.
// "already_absent" is a resource that was gone before the call. "unspecified"
// is an answer the BFF could not read as any of them, and is not a deletion.
export type DeletionOutcome =
  'completed' | 'accepted' | 'already_absent' | 'unspecified';

// deleted is true for the two outcomes that establish the resource is gone.
export type DeleteResult = {
  outcome: DeletionOutcome;
  deleted: boolean;
};

// openshell.datamodel.v1.WorkspacePhase
export type WorkspacePhase = 'ACTIVE' | 'TERMINATING' | 'UNSPECIFIED';

export type Workspace = {
  metadata: ObjectMeta;
  phase: WorkspacePhase;
};

// openshell.v1.WorkspaceRole — USER or ADMIN. There is no role-update RPC;
// changing a role means remove + re-add.
export type WorkspaceRole = 'USER' | 'ADMIN';

export type WorkspaceMember = {
  metadata: ObjectMeta;
  principalSubject: string;
  role: WorkspaceRole | 'UNSPECIFIED';
};

// openshell.v1.SandboxPhase — the full lifecycle. A sandbox can be stopped
// (its persistent state is kept) and started again:
// PROVISIONING → READY → STOPPING → STOPPED → STARTING → READY, and, where the
// gateway's compute driver supports it, suspended (its memory state is kept
// too) and resumed: READY → SUSPENDING → SUSPENDED → STARTING → READY. ERROR
// and DELETING are the terminal and transitional states. COMPLETED is a
// sandbox whose main command exited with status 0; a nonzero exit is ERROR.
export type SandboxPhase =
  | 'PROVISIONING'
  | 'READY'
  | 'ERROR'
  | 'DELETING'
  | 'STOPPING'
  | 'STOPPED'
  | 'STARTING'
  | 'COMPLETED'
  | 'SUSPENDING'
  | 'SUSPENDED'
  | 'UNKNOWN'
  | 'UNSPECIFIED';

export type SandboxCondition = {
  type: string;
  status: string;
  reason?: string;
  message?: string;
  lastTransitionTime?: string;
};

// openshell.v1.EndpointResult — the last network result the gateway accepted
// for a tool server endpoint. A passive observation of real traffic: it does
// not say the endpoint is reachable now, or that a tool call succeeded.
export type EndpointResult =
  | 'UNSPECIFIED'
  | 'NO_OBSERVED_EXCHANGE'
  | 'HTTP_RESPONSE_RECEIVED'
  | 'POLICY_DENIED'
  | 'CREDENTIAL_UNAVAILABLE'
  | 'TLS_FAILED'
  | 'TRANSPORT_FAILED'
  | 'UPSTREAM_REJECTED';

// openshell.v1.EndpointFailureDomain without its prefix: the subsystem whose
// closed set of codes explains a failure.
export type EndpointFailureDomain =
  'UNSPECIFIED' | 'TOKEN_GRANT' | (string & NonNullable<unknown>);

// openshell.v1.EndpointFailure — why an observed exchange failed, in the
// gateway's closed sets of codes (code and stage, such as
// "provider_authorization_required" and "gateway_rpc"). It does not establish
// that the endpoint is unhealthy, and no field carries remote text.
// credentialKey is a credential's name, never its value; provider and
// credentialKey are absent when the failure came before a credential was
// selected. requestId is the x-openshell-request-id the workload got back.
export type EndpointFailure = {
  domain: EndpointFailureDomain;
  code: string;
  // Absent when the gateway gives none.
  recoveryAction?: RefreshRecoveryAction;
  provider?: string;
  credentialKey?: string;
  stage?: string;
  // What a token service answered; absent when none answered.
  upstreamHttpStatus?: number;
  requestId?: string;
};

// openshell.v1.TokenGrantFailureObservation — the latest token grant the
// sandbox refused, for any destination, including one no endpoint status
// lists. It describes the attempted call, not the destination's health.
export type TokenGrantFailureObservation = {
  destinationHost: string;
  destinationPort: number;
  failure?: EndpointFailure;
};

// openshell.v1.EndpointStatus — a tool server endpoint the sandbox's policy
// configures. lastReportedAt (RFC 3339) is when the gateway accepted the
// observation, not when the request was made, and is absent until there is one.
// lastFailure is the cause of lastResult when the sandbox classified one.
export type EndpointStatus = {
  endpointId: string;
  host: string;
  ports?: number[];
  path?: string;
  lastResult: EndpointResult;
  lastReportedAt?: string;
  lastFailure?: EndpointFailure;
};

export type ConfigurationAdmissionState =
  'UNSPECIFIED' | 'PENDING' | 'ACCEPTED' | 'REJECTED';

// openshell.v1.SandboxConfigurationAdmission — whether the sandbox validated
// the configuration the gateway wants it to run. configRevision and
// providerEnvRevision are 64-bit fingerprints, sent as decimal strings because
// a JavaScript number cannot hold them exactly: compare them, never calculate.
export type ConfigurationAdmission = {
  state: ConfigurationAdmissionState;
  policyVersion: number;
  policyHash?: string;
  configRevision: string;
  providerEnvRevision: string;
  error?: string;
};

export type SandboxStatus = {
  sandboxName?: string;
  agentPod?: string;
  conditions?: SandboxCondition[];
  phase: SandboxPhase;
  currentPolicyVersion: number;
  // Main process exit code once the sandbox has exited (undefined while
  // running). Signal exits are reported as 128+signal.
  exitCode?: number;
  endpointStatuses?: EndpointStatus[];
  // Absent when the gateway holds no refused grant.
  lastTokenGrantFailure?: TokenGrantFailureObservation;
  configurationAdmission?: ConfigurationAdmission;
};

// --- openshell.sandbox.v1.SandboxPolicy (protojson camelCase) ---

export type FilesystemPolicy = {
  includeWorkdir?: boolean;
  readOnly?: string[];
  readWrite?: string[];
};

export type LandlockPolicy = {
  compatibility?: string;
};

export type ProcessPolicy = {
  runAsUser?: string;
  runAsGroup?: string;
};

export type L7QueryMatcher = {
  glob?: string;
  any?: string[];
};

export type L7Allow = {
  method?: string;
  path?: string;
  command?: string;
  query?: Record<string, L7QueryMatcher>;
  operationType?: string;
  operationName?: string;
  fields?: string[];
  params?: Record<string, L7QueryMatcher>;
};

export type L7Rule = {
  allow?: L7Allow;
};

export type L7DenyRule = Omit<L7Allow, never>;

export type GraphqlOperation = {
  operationType?: string;
  operationName?: string;
  fields?: string[];
};

export type McpOptions = {
  strictToolNames?: boolean;
  allowAllKnownMcpMethods?: boolean;
  // Exact MCP protocol revisions the endpoint accepts. The gateway fills in
  // its pinned default when the list is empty.
  versions?: string[];
};

// Every field of openshell.sandbox.v1.NetworkEndpoint. The rule editor's form
// sets a few of them; the rest are shown as they are and edited as a document.
export type NetworkEndpoint = {
  host?: string;
  port?: number;
  ports?: number[];
  protocol?: string;
  tls?: string;
  enforcement?: string;
  access?: string;
  rules?: L7Rule[];
  denyRules?: L7DenyRule[];
  allowedIps?: string[];
  path?: string;
  advisorProposed?: boolean;
  allowEncodedSlash?: boolean;
  persistedQueries?: string;
  graphqlPersistedQueries?: Record<string, GraphqlOperation>;
  graphqlMaxBodyBytes?: number;
  websocketCredentialRewrite?: boolean;
  requestBodyCredentialRewrite?: boolean;
  allowUninspectedCredentials?: boolean;
  // Set by the gateway on endpoints of an attached credentialed provider.
  providerCredentialed?: boolean;
  credentialSigning?: string;
  signingService?: string;
  signingRegion?: string;
  jsonRpcMaxBodyBytes?: number;
  mcp?: McpOptions;
  credentialBinding?: { provider?: string };
};

export type NetworkBinary = {
  path?: string;
};

export type NetworkPolicyRule = {
  name?: string;
  endpoints?: NetworkEndpoint[];
  binaries?: NetworkBinary[];
};

export type NetworkMiddlewareConfig = {
  name?: string;
  middleware?: string;
  config?: Record<string, unknown>;
  onError?: string;
  endpoints?: { include?: string[]; exclude?: string[] };
  order?: number;
};

export type SandboxPolicy = {
  version?: number;
  filesystem?: FilesystemPolicy;
  landlock?: LandlockPolicy;
  process?: ProcessPolicy;
  networkPolicies?: Record<string, NetworkPolicyRule>;
  networkMiddlewares?: Record<string, NetworkMiddlewareConfig>;
};

// --- openshell.v1.PolicyMergeOperation (protojson camelCase) ---
// One incremental change to a sandbox's network policy, as
// `openshell policy update` sends it. Exactly one key is set.

export type L7RuleTarget = {
  ruleName: string;
  host: string;
  // Every port of the endpoint, not only the one being looked up.
  ports: number[];
  // Absent: the gateway requires a unique endpoint. Empty: the endpoint that
  // has no path scope.
  path?: string;
  // Every binary of the rule, or anyBinary for a rule that names none.
  binaries?: NetworkBinary[];
  anyBinary?: boolean;
};

export type PolicyMergeOperation =
  | { addRule: { ruleName: string; rule: NetworkPolicyRule } }
  | { removeEndpoint: { ruleName?: string; host: string; port: number } }
  | { removeRule: { ruleName: string } }
  | { addAllowRules: { target: L7RuleTarget; rules: L7Rule[] } }
  | { addDenyRules: { target: L7RuleTarget; denyRules: L7DenyRule[] } }
  | { removeBinary: { ruleName: string; binaryPath: string } };

// --- Sandbox ---

// openshell.v1.SandboxTemplate — the compute template inline in a sandbox's
// spec. Not SandboxTemplate below, the reusable resource. The image is
// SandboxSpec.image.
//
// resources and driverConfig are free-form on the wire. The dashboard and the
// CLI write a CPU or memory limit as { limits: { cpu, memory } }, and so does
// the gateway for a sandbox created from a workload template.
export type SandboxSpecTemplate = {
  runtimeClassName?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  environment?: Record<string, string>;
  // Absent when the sandbox follows the platform default.
  userNamespaces?: boolean;
  resources?: Record<string, unknown>;
  driverConfig?: Record<string, unknown>;
};

export type SandboxSpec = {
  logLevel?: string;
  environment?: Record<string, string>;
  image?: string;
  providers?: string[];
  policy?: SandboxPolicy;
  // Absent when the template holds nothing but the image.
  template?: SandboxSpecTemplate;
  // The argv of the sandbox's main process. Absent for a sandbox created
  // without one, which runs its image's login shell.
  command?: string[];
  // A pseudo-terminal for the main process. The gateway sets it for every
  // sandbox created without a command.
  tty?: boolean;
  // gpu is true for any GPU request; without gpuCount the compute driver
  // chose the assignment.
  gpu?: boolean;
  gpuCount?: number;
};

// openshell.v1.SandboxWorkloadTemplateProvenance — the workload template, and
// the revision of it, a sandbox was created from.
export type WorkloadTemplateProvenance = {
  name: string;
  resourceVersion?: string;
};

export type Sandbox = {
  metadata: ObjectMeta;
  spec: SandboxSpec;
  status: SandboxStatus;
  createdFromWorkloadTemplate?: WorkloadTemplateProvenance;
  // URLs of the services exposed as the sandbox was created, keyed by service
  // name ("" is the unnamed service). Only the answer to a create carries it;
  // the sandbox's services list has the endpoints afterwards.
  serviceUrls?: Record<string, string>;
};

// openshell.v1.SandboxServiceExposure — a loopback HTTP service to expose as
// the sandbox is created. Without a name it is the sandbox's unnamed service,
// which is what `openshell sandbox create --expose <port>` registers.
export type ServiceExposure = {
  service?: string;
  targetPort: number;
};

export type CreateSandboxRequest = {
  name?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  // Omit for the gateway's default image, which is what `openshell sandbox
  // create` without --from runs. The sandbox that comes back names it.
  image?: string;
  logLevel?: string;
  environment?: Record<string, string>;
  providers?: string[];
  // GPU request via ResourceRequirements.gpu; omit for none.
  gpuCount?: number;
  // K8s-style quantities applied as template resource limits.
  cpu?: string;
  memory?: string;
  // Required by the BFF. Gateway 0.1.2 accepts a sandbox without one and then
  // rejects its configuration, so it never becomes ready.
  policy: SandboxPolicy;
  // The argv of the main process, launched once and parsed by no shell. Omit
  // for the image's login shell.
  command?: string[];
  // A pseudo-terminal for the command. A sandbox without a command always
  // gets one.
  tty?: boolean;
  // For the Kubernetes compute driver. The Docker and VM drivers refuse a
  // sandbox that names one.
  runtimeClassName?: string;
  // Keyed by compute driver name. The gateway refuses it unless its
  // administrator enabled allow_driver_config.
  driverConfig?: Record<string, unknown>;
  // If one of them cannot be exposed the create fails and no sandbox is kept.
  serviceExposures?: ServiceExposure[];
};

// --- Sandbox templates ---
// openshell.v1.SandboxWorkloadTemplate — a reusable, workspace-scoped template
// resource. A sandbox is created from a template by name (see
// CreateSandboxFromTemplateRequest), supplying only governance fields.

export type SandboxGPU = {
  count?: number;
};

export type SandboxResources = {
  cpu?: string;
  memory?: string;
  gpu?: SandboxGPU;
};

export type SandboxWorkload = {
  // Absent for a template of the gateway's default image, which is resolved
  // when a sandbox is created from the template.
  image?: string;
  environment?: Record<string, string>;
  resources?: SandboxResources;
};

export type SandboxStartup = {
  readyWithinMs?: number;
  maxBurst?: number;
};

export type SandboxServiceLevel = {
  startup?: SandboxStartup;
};

export type SandboxTemplateSpec = {
  workload?: SandboxWorkload;
  driverConfig?: Record<string, unknown>;
  desiredServiceLevel?: SandboxServiceLevel;
};

export type SandboxTemplate = {
  metadata: ObjectMeta;
  spec: SandboxTemplateSpec;
};

export type CreateSandboxTemplateRequest = {
  name: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  spec: SandboxTemplateSpec;
};

// Only governance fields are accepted; the workload comes from the template.
// command, tty and serviceExposures are not part of a template and mean what
// they mean on CreateSandboxRequest.
export type CreateSandboxFromTemplateRequest = {
  name?: string;
  templateName: string;
  providers?: string[];
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  // Required — SandboxSpec.policy is a required field on CreateSandbox.
  policy: SandboxPolicy;
  command?: string[];
  tty?: boolean;
  serviceExposures?: ServiceExposure[];
};

// --- Providers ---

// openshell.datamodel.v1.Provider as returned by the BFF. Credential values
// are secret and never serialized — only key names.
export type Provider = {
  metadata: ObjectMeta;
  type: string;
  config?: Record<string, string>;
  credentialNames?: string[];
  credentialExpiresAtMs?: Record<string, number>;
  profileWorkspace?: string;
};

// Mirrors the gateway's Provider message; the BFF forwards it as it is.
export type CreateProviderRequest = {
  name: string;
  type: string;
  // Provider.profile_workspace: the scope `type` is looked up in. Empty is
  // the platform scope, which holds the built-in and the platform profiles;
  // the provider's own workspace resolves the profile that workspace sees.
  // The gateway accepts no other value (see profileWorkspaceFor).
  profileWorkspace?: string;
  // Keyed by the gateway's stored key: the credential's env var name when the
  // profile declares one, its name otherwise (see credentialStorageKey).
  credentials?: Record<string, string>;
  config?: Record<string, string>;
  labels?: Record<string, string>;
};

// openshell.v1.ProviderProfileCategory
export type ProviderProfileCategory =
  | 'OTHER'
  | 'INFERENCE'
  | 'AGENT'
  | 'SOURCE_CONTROL'
  | 'MESSAGING'
  | 'DATA'
  | 'KNOWLEDGE'
  | 'UNSPECIFIED';

// openshell.v1.ProviderCredentialRefreshStrategy without its prefix. A value
// the dashboard's copy of the API has no name for arrives as its number.
export type ProfileRefreshStrategy =
  | 'UNSPECIFIED'
  | 'STATIC'
  | 'EXTERNAL'
  | 'OAUTH2_REFRESH_TOKEN'
  | 'OAUTH2_CLIENT_CREDENTIALS'
  | 'GOOGLE_SERVICE_ACCOUNT_JWT'
  | 'AWS_STS_ASSUME_ROLE'
  | (string & NonNullable<unknown>);

// openshell.v1.ProviderCredentialRefreshMaterial: one input a refresh needs.
// `secret` says the input is a secret; its value is given when refresh is
// configured on a provider.
export type ProfileRefreshMaterial = {
  name: string;
  description?: string;
  required: boolean;
  secret: boolean;
};

export type ProfileRefreshOutput = {
  output: string;
  credential: string;
};

// openshell.v1.ProviderCredentialRefresh. The durations are protobuf durations
// in their JSON form ("300s") and absent when the profile does not set them,
// which is not the same as "0s".
export type ProfileCredentialRefresh = {
  strategy: ProfileRefreshStrategy;
  tokenUrl?: string;
  scopes?: string[];
  refreshBefore?: string;
  maxLifetime?: string;
  material?: ProfileRefreshMaterial[];
  additionalOutputs?: ProfileRefreshOutput[];
};

export type ProfileTokenGrantSubjectToken = {
  source: string;
  credential: string;
  subjectTokenType?: string;
};

export type ProfileTokenGrantAudienceOverride = {
  host?: string;
  port?: number;
  path?: string;
  audience: string;
  scopes?: string[];
};

// openshell.v1.ProviderCredentialTokenGrant: a credential the sandbox obtains
// through an OAuth2 grant when it needs it.
export type ProfileTokenGrant = {
  grantType:
    | 'UNSPECIFIED'
    | 'CLIENT_CREDENTIALS'
    | 'TOKEN_EXCHANGE'
    | (string & NonNullable<unknown>);
  tokenEndpoint: string;
  audience?: string;
  jwtSvidAudience?: string;
  clientAssertionType?: string;
  requestedTokenType?: string;
  scopes?: string[];
  cacheTtl?: string;
  subjectToken?: ProfileTokenGrantSubjectToken;
  audienceOverrides?: ProfileTokenGrantAudienceOverride[];
};

// openshell.v1.ProviderProfileCredential: the schema of one credential, never
// a value. The same shape read and written.
export type ProfileCredential = {
  name: string;
  description?: string;
  envVars?: string[];
  required: boolean;
  authStyle?: string;
  headerName?: string;
  queryParam?: string;
  pathTemplate?: string;
  refresh?: ProfileCredentialRefresh;
  tokenGrant?: ProfileTokenGrant;
  // Set by the gateway (OpenShell 0.1.3 and newer): the endpoints it lets
  // obtain the credential's token grant. Ignored when written, and kept so
  // that a profile that was read can be sent back as it is.
  tokenGrantOwners?: string[];
  // OpenShell 0.1.4 and newer: one opaque, identity-bound placeholder for the
  // credential across value-only provider updates. Absent when false.
  stablePlaceholder?: boolean;
};

// openshell.sandbox.v1.McpOptions (protojson camelCase).
export type ProfileMcpOptions = {
  versions?: string[];
  strictToolNames?: boolean;
  allowAllKnownMcpMethods?: boolean;
};

export type ProfileGraphqlOperation = {
  operationType?: string;
  operationName?: string;
  fields?: string[];
};

// An L7 allow or deny rule of a profile endpoint. Policy rules are the same
// message; `params` is the MCP matcher map the policy pages do not use.
export type ProfileL7Match = L7Allow & {
  params?: Record<string, L7QueryMatcher>;
};

// A profile endpoint whole: openshell.sandbox.v1.NetworkEndpoint as protojson
// (camelCase names, enum names), the spelling sandbox policies use. The enums
// can also be numbers, which is how a value without a name is carried.
export type ProfileNetworkEndpoint = {
  host?: string;
  port?: number;
  ports?: number[];
  protocol?: string;
  tls?: string | number;
  enforcement?: string | number;
  access?: string | number;
  rules?: { allow?: ProfileL7Match }[];
  denyRules?: ProfileL7Match[];
  allowedIps?: string[];
  allowEncodedSlash?: boolean;
  websocketCredentialRewrite?: boolean;
  requestBodyCredentialRewrite?: boolean;
  allowUninspectedCredentials?: boolean;
  persistedQueries?: string;
  graphqlPersistedQueries?: Record<string, ProfileGraphqlOperation>;
  graphqlMaxBodyBytes?: number;
  jsonRpcMaxBodyBytes?: number;
  mcp?: ProfileMcpOptions;
  path?: string;
  credentialSigning?: string;
  signingService?: string;
  signingRegion?: string;
};

export type ProfileBinary = {
  path: string;
};

export type ProfileDiscovery = {
  credentials?: string[];
};

// openshell.v1.ProviderProfileFile: a file that a sandbox with the provider
// attached finds under /run/openshell/providers/<provider>/. `content` is a
// template in which {{config.KEY}} stands for a value of the provider's
// config, and `envVar` names an environment variable that is given the file's
// path. Upstream added it in OpenShell 0.1.3 and marks it experimental.
export type ProfileFile = {
  path: string;
  content: string;
  envVar?: string;
};

export type ProviderProfile = {
  id: string;
  displayName: string;
  description?: string;
  category: ProviderProfileCategory;
  credentials: ProfileCredential[];
  files?: ProfileFile[];
  // host:port summaries of the profile's network endpoints.
  endpoints?: string[];
  // The endpoints whole. Absent, while `endpoints` is not, when the backend
  // cannot read a whole endpoint: such a profile cannot be exported or sent
  // back without losing what its endpoints allow.
  networkEndpoints?: ProfileNetworkEndpoint[];
  binaries?: ProfileBinary[];
  discovery?: ProfileDiscovery;
  annotations?: Record<string, string>;
  inferenceCapable: boolean;
  // Where the profile comes from: "user" for one that was imported, or
  // "interceptor/<name>" for one a gateway interceptor vends.
  source?: string;
  // "workspace" or "platform" for an imported profile; empty for one that no
  // scope owns.
  scope?: string;
  resourceVersion: number;
};

export type ProfileEndpoint = {
  host: string;
  port?: number;
  protocol?: string;
};

// A provider profile as it is written, for import, update and lint: every
// field of the gateway's ProviderProfile. The gateway replaces a stored
// profile with the one an update sends, so an update carries the whole
// profile.
export type ImportProfileRequest = {
  id: string;
  displayName: string;
  description?: string;
  category: ProviderProfileCategory;
  credentials?: ProfileCredentialInput[];
  files?: ProfileFile[];
  // The host-and-port form the profile form writes. A request sends this or
  // networkEndpoints, not both.
  endpoints?: ProfileEndpoint[];
  networkEndpoints?: ProfileNetworkEndpoint[];
  binaries?: ProfileBinary[];
  discovery?: ProfileDiscovery;
  annotations?: Record<string, string>;
  inferenceCapable: boolean;
  resourceVersion?: number;
  // Set by the gateway and ignored when written; accepted so that a profile
  // that was read or exported can be sent back as it is.
  source?: string;
  scope?: string;
  // Not part of the profile: a label the gateway repeats in the diagnostics
  // it returns for it. A file import sends the file name.
  importSource?: string;
};

export type ProfileCredentialInput = ProfileCredential;

export type ProfileDiagnostic = {
  source?: string;
  profileId?: string;
  field?: string;
  message: string;
  severity?: string;
};

export type ImportProfilesResponse = {
  diagnostics?: ProfileDiagnostic[];
  profiles: ProviderProfile[];
  imported: boolean;
};

export type UpdateProfileResponse = {
  diagnostics?: ProfileDiagnostic[];
  profile?: ProviderProfile;
  updated: boolean;
};

export type LintProfilesResponse = {
  diagnostics?: ProfileDiagnostic[];
  valid: boolean;
};

// --- Credential refresh ---

export type RefreshStrategy =
  | 'oauth2-refresh-token'
  | 'oauth2-client-credentials'
  | 'google-service-account-jwt'
  | 'aws-sts-assume-role'
  | 'static'
  | 'external';

export type ConfigureProviderRefreshRequest = {
  credentialKey: string;
  strategy: RefreshStrategy;
  material?: Record<string, string>;
  secretMaterialKeys?: string[];
  expiresAtMs?: number;
};

// openshell.v1.ProviderCredentialRefreshRecoveryAction without its prefix:
// what the gateway says a failed refresh needs.
export type RefreshRecoveryAction =
  | 'RETRY'
  | 'REAUTHORIZE'
  | 'FIX_CONFIGURATION'
  | 'INVESTIGATE'
  | (string & NonNullable<unknown>);

export type CredentialRefreshStatus = {
  credentialKey: string;
  strategy: string;
  status: string;
  expiresAtMs?: number;
  // Absent when no automatic refresh is scheduled; recoveryAction says
  // whether that is a failure waiting on someone.
  nextRefreshAtMs?: number;
  lastRefreshAtMs?: number;
  lastError?: string;
  // Absent when the refresh needs nothing.
  recoveryAction?: RefreshRecoveryAction;
  // The gateway's own stable identifier for the failure, such as
  // "oauth_invalid_grant", and a recognized refinement of it.
  failureCode?: string;
  providerErrorSubtype?: string;
  lastErrorAtMs?: number;
};

// --- Gateway ---

export type ServiceStatus =
  'HEALTHY' | 'DEGRADED' | 'UNHEALTHY' | 'UNSPECIFIED';

export type ComputeDriver = {
  name: string;
  driverName?: string;
  driverVersion?: string;
};

// Whether the gateway is on the release line this dashboard is built for. A
// line is every gateway release that shares a major.minor, such as 0.1:
//   supported   — on the line: any patch, pre-release or rebuild of it
//   unsupported — on another line, older or newer; calls may fail
//   unknown     — the version could not be read, the gateway does not know
//                 its own version (it reports 0.0.0), or the BFF has no line
export type GatewayCompatibilityStatus =
  'unsupported' | 'supported' | 'unknown';

// The dashboard's own verdict on the gateway — computed by the BFF, not
// reported by the gateway. supportedLine is the line the dashboard is built
// for, written major.minor ("0.1"); it is absent when the BFF has none.
export type GatewayCompatibility = {
  status: GatewayCompatibilityStatus;
  supportedLine?: string;
};

// openshell.v1.ExtensionKind without its prefix. UNSPECIFIED is a kind the
// BFF's SDK cannot name.
export type GatewayExtensionKind =
  | 'COMPUTE_DRIVER'
  | 'CREDENTIAL_DRIVER'
  | 'GATEWAY_INTERCEPTOR'
  | 'SUPERVISOR_MIDDLEWARE'
  | 'UNSPECIFIED';

// openshell.v1.NegotiatedExtensionInfo — what one initialized extension
// negotiated with the gateway. configuredName is the name the operator
// registered it under; the implementation fields are what the extension
// reports about itself; requiredCapabilities are what it needs from the
// gateway.
export type GatewayExtension = {
  kind: GatewayExtensionKind;
  configuredName: string;
  implementationName?: string;
  implementationVersion?: string;
  protocolMajor: number;
  protocolMinor: number;
  supportedCapabilities?: string[];
  requiredCapabilities?: string[];
};

// openshell.v1.GetGatewayInfoResponse — status, version, compute drivers and
// negotiated extensions are everything the gateway exposes about itself.
// compatibility is the one field the BFF adds. It and extensions are
// optional: the BFF leaves compatibility out when it has no verdict to give,
// and the UI treats a missing extensions list as empty.
//
// The gateway answers this for platform admins only. A user without that role
// gets a 403 here; GatewayCompatibilityInfo is what they can read.
export type GatewayInfo = {
  status: ServiceStatus;
  gatewayVersion: string;
  computeDrivers: ComputeDriver[];
  extensions?: GatewayExtension[];
  compatibility?: GatewayCompatibility;
};

// GET /gateway/compatibility — the gateway's version and the verdict on it,
// for every signed-in user. Both keys mean what they mean on GatewayInfo; the
// version here comes from the gateway's health check, which needs no role.
//
// healthy is what that same health check says about the gateway: true when it
// answered "healthy", false when it answered anything else (the BFF's SDK does
// not say whether that was degraded, unhealthy or unspecified). It is absent
// when the BFF had no health to report. A gateway that did not answer at all
// is not a value here: the request fails.
export type GatewayCompatibilityInfo = {
  gatewayVersion: string;
  healthy?: boolean;
  compatibility: GatewayCompatibility;
};

// --- Auth / misc ---

export type FeatureFlags = {
  terminal: boolean;
  fileTransfer: boolean;
  settings: boolean;
  globalPolicy: boolean;
  credentialRefresh: boolean;
  services: boolean;
  draftPolicy: boolean;
};

export type AuthConfig = {
  authDisabled: boolean;
  adminRole?: string;
  userRole?: string;
  logoutUrl?: string;
  features: FeatureFlags;
};

export type CurrentUser = {
  subject: string;
  displayName?: string;
  roles: string[];
  scopes?: string[];
  identityProvider?: string;
};

export type CreateWorkspaceRequest = {
  name: string;
  labels?: Record<string, string>;
};

export type AddMemberRequest = {
  principalSubject: string;
  role: WorkspaceRole;
};

// --- Logs (SandboxLogLine) ---

export type LogLine = {
  sandboxId?: string;
  timestampMs: number;
  level?: string;
  target?: string;
  message: string;
  // "gateway" or "sandbox".
  source?: string;
  // Structured decision context (dst_host, action, …) — the dashboard's only
  // window into security decisions; there is no events API.
  fields?: Record<string, string>;
};

export type SandboxLogs = {
  logs: LogLine[];
  bufferTotal: number;
};

export enum LogEvent {
  Log = 'log',
  Warning = 'warning',
  StreamError = 'stream-error',
  End = 'end',
}

// --- Policy revisions (SandboxPolicyRevision) ---

export type PolicyStatus =
  'PENDING' | 'LOADED' | 'FAILED' | 'SUPERSEDED' | 'UNSPECIFIED';

export type PolicyRevision = {
  version: number;
  policyHash?: string;
  status: PolicyStatus;
  loadError?: string;
  createdAtMs: number;
  loadedAtMs?: number;
  policy?: SandboxPolicy;
  provenance?: Record<string, string>;
};

export type SandboxPolicyView = {
  activeVersion: number;
  latest?: PolicyRevision;
  revisions: PolicyRevision[];
};

export type PolicyUpdateResult = {
  version: number;
  policyHash?: string;
};

// What the sandbox is given to enforce (`openshell policy get` without --rev).
// With policySource "GLOBAL" it is the gateway-global policy and the sandbox's
// own policy is dormant; otherwise it is the sandbox's own policy plus one
// `_provider_*` rule per attached provider.
export type EffectivePolicy = {
  policy?: SandboxPolicy;
  // The sandbox's own policy version, whichever policy is the source.
  version: number;
  policyHash?: string;
  policySource: PolicySource;
  globalPolicyVersion?: number;
  // What the gateway does with a revision the sandbox rejects: "fail_closed"
  // or "retain_last_valid".
  policyValidationFailureMode?: string;
};

// --- Draft policy advisor (PolicyChunk) ---

export type PolicyChunk = {
  id: string;
  // "pending", "approved", or "rejected".
  status: string;
  ruleName?: string;
  proposedRule?: NetworkPolicyRule;
  rationale?: string;
  securityNotes?: string;
  confidence: number;
  createdAtMs: number;
  decidedAtMs?: number;
  hitCount: number;
  binary?: string;
  // Gateway prover verdict — there is no separate verify RPC.
  validationResult?: string;
  rejectionReason?: string;
  // Pins an approval to the exact evaluated candidate (optimistic concurrency).
  reviewToken?: string;
  // Set when a prover-clean chunk still fails to apply to the complete
  // candidate policy.
  applicationError?: string;
  // Before/after effective policy identity and full policies for a diff view.
  currentEffectivePolicyHash?: string;
  candidateEffectivePolicyHash?: string;
  currentEffectivePolicy?: SandboxPolicy;
  candidateEffectivePolicy?: SandboxPolicy;
  // "initial" or "refined"; a refined chunk names the one it replaces.
  stage?: string;
  supersedesChunkId?: string;
  denialSummaryIds?: string[];
  // When the denial behind the proposal was first and last seen; hitCount is
  // how many times in between.
  firstSeenMs?: number;
  lastSeenMs?: number;
};

// One reviewed chunk of a bulk approval, bound to the review token it was
// fetched with.
export type DraftChunkApproval = {
  chunkId: string;
  reviewToken?: string;
};

export type ApproveAllResult = {
  policyVersion?: number;
  policyHash?: string;
  chunksApproved: number;
  // Skipped for any reason: security-flagged, stale, or in conflict with a
  // chunk approved earlier in the same batch.
  chunksSkipped: number;
};

export type DraftPolicy = {
  chunks: PolicyChunk[];
  rollingSummary?: string;
  draftVersion: number;
  lastAnalyzedAtMs?: number;
};

export type DraftHistoryEntry = {
  timestampMs: number;
  eventType: string;
  description: string;
  chunkId?: string;
};

// The pending draft chunks of one sandbox. unavailable marks a sandbox whose
// inbox could not be read: its count is unknown, which is not none pending,
// and pendingCount is then 0 and means nothing. latestRationale is the
// rationale of the newest pending chunk, when it has one.
export type DraftSandboxSummary = {
  workspace: string;
  sandboxName: string;
  pendingCount: number;
  hasSecurityFlags: boolean;
  latestDraftMs: number;
  latestRationale?: string;
  unavailable?: boolean;
};

// One entry per sandbox that has pending chunks or whose inbox could not be
// read. A sandbox without an entry has none pending.
export type DraftSummary = {
  sandboxes: DraftSandboxSummary[];
  totalPending: number;
};

// --- Service endpoints ---

// serviceName is empty for a sandbox's unnamed endpoint, of which it can have
// one. workspace is what tells endpoints apart in a list across workspaces.
export type ServiceEndpoint = {
  id?: string;
  workspace?: string;
  sandboxId?: string;
  sandboxName: string;
  serviceName: string;
  targetPort: number;
  domain: boolean;
  url?: string;
};

// Leave service out, or empty, for the sandbox's unnamed endpoint. Gateways
// 0.1.0 to 0.1.2 ignore domain and route every endpoint for the browser.
export type ExposeServiceRequest = {
  service?: string;
  targetPort: number;
  domain?: boolean;
};

// --- Gateway settings ---

// A gateway setting's value in the type the gateway has it in. Settings are
// typed (string, bool or int) and the gateway refuses a value of another type.
export type SettingValue = string | boolean | number;

// `value` is absent for a setting the gateway knows but that was never set.
// For those the gateway does not say which type the key takes.
export type SettingEntry = { key: string; value?: SettingValue };

export type GatewaySettings = {
  settings: SettingEntry[];
  settingsRevision: number;
};

// --- Sandbox settings ---

// Where a sandbox's setting gets its value: GLOBAL from the gateway, SANDBOX
// from the sandbox itself, UNSPECIFIED from neither (the setting has no
// value). The gateway's scope wins: while a key is set globally the gateway
// refuses to set or delete it on a sandbox.
export type SettingScope = 'SANDBOX' | 'GLOBAL' | 'UNSPECIFIED';

export type SandboxSettingEntry = SettingEntry & { scope: SettingScope };

// Whether a sandbox runs its own policy or the gateway-global one.
export type PolicySource = 'SANDBOX' | 'GLOBAL' | 'UNSPECIFIED';

// GetSandboxConfigResponse without the policy itself. configRevision and
// providerEnvRevision are 64-bit fingerprints sent as decimal strings (see
// ConfigurationAdmission). globalPolicyVersion is zero unless the global
// policy is the source.
export type SandboxSettings = {
  settings: SandboxSettingEntry[];
  policySource: PolicySource;
  policyHash?: string;
  policyVersion: number;
  globalPolicyVersion: number;
  configRevision: string;
  providerEnvRevision: string;
  // "fail_closed" or "retain_last_valid".
  policyValidationFailureMode?: string;
};

export type SettingSetResult = {
  updated: boolean;
  settingsRevision: number;
};

// The answer to deleting one setting, on a sandbox or on the gateway. deleted
// is false when the key was not set in that scope, so nothing was deleted.
export type SettingDeleteResult = {
  deleted: boolean;
  settingsRevision: number;
};

// sinceMs is a fixed point in time. sinceDurationMs is a window that ends now
// (the CLI's `logs --since 5m`): the request turns it into a point in time
// when it is sent, so a polled query keeps a stable key and a moving window.
// sinceMs wins when both are set.
export type LogFilters = {
  lines?: number;
  sinceMs?: number;
  sinceDurationMs?: number;
  sources?: string[];
  level?: string;
};

export type { CredentialInputSlot, ModelPickerSlot } from '../slots/types';
