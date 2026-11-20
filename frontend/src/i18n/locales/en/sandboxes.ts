export default {
  // The cause the gateway gives for a failed exchange
  // (openshell.v1.EndpointFailure), in the words `openshell sandbox get` uses.
  // Codes, stages and request IDs are shown as the gateway sends them.
  failure: {
    ariaLabel: 'Failure cause',
    cause: 'Cause',
    // {{domain}} is one of the domain names below.
    causeValue: '{{code}} ({{domain}})',
    stage: 'Stage',
    upstreamStatus: 'Upstream HTTP status',
    credential: 'Credential',
    recovery: 'Recovery',
    requestId: 'Request ID',
    domain: {
      TOKEN_GRANT: 'token grant',
      UNSPECIFIED: 'unknown',
    },
    recoveryAction: {
      RETRY: 'Retry later; the failure can be temporary.',
      REAUTHORIZE: 'Reauthorize the provider credential.',
      FIX_CONFIGURATION:
        'An operator must fix the provider or identity configuration.',
      INVESTIGATE: 'An operator must investigate gateway and supervisor logs.',
      none: 'No recovery action provided.',
      // An action this build has no words for, by the gateway's name for it.
      other: 'The gateway reports the recovery action {{action}}.',
    },
  },
  tokenGrantFailure: {
    title: 'Last token grant failure',
    destination: 'Destination',
    destinationValue: '{{host}} (port {{port}})',
    help: "The latest token grant the sandbox refused, for any destination. It describes the attempted call, not the destination's current health, and clears once a grant to the same destination succeeds.",
  },
  // Suspend and resume, on the sandbox page and in a list row's actions.
  // {{message}} is the gateway's own, as it sent it.
  lifecycle: {
    suspend: 'Suspend',
    resume: 'Resume',
    suspending: 'Suspending sandbox {{name}}',
    resuming: 'Resuming sandbox {{name}}',
    suspendFailed: 'Failed to suspend sandbox {{name}}: {{message}}',
    resumeFailed: 'Failed to resume sandbox {{name}}: {{message}}',
  },
  create: {
    // The folded section of optional fields in the Create sandbox form, and
    // its counterpart in Create sandbox from template, which has no
    // resources. Only the name is generated when it is left empty.
    optionalSection: 'Name, labels and resources',
    optionalSectionFromTemplate: 'Name and labels',
    nameHelper: 'A name is generated when this is left empty.',
  },
  drafts: {
    status: {
      // The dropdown's accessible name; it names the filter, not its value.
      label: 'Proposal status',
      all: 'All statuses',
      pending: 'Pending',
      approved: 'Approved',
      rejected: 'Rejected',
    },
    summary: {
      source:
        'An LLM-generated analysis of the activity behind these proposals.',
      sourceAriaLabel: 'Where the analysis summary comes from',
    },
    chunk: {
      labels: 'Labels',
      securityNotes: 'Security notes',
    },
  },
};
