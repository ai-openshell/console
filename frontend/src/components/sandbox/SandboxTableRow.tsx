import React from 'react';
import {
  Button,
  Content,
  Flex,
  FlexItem,
  Label,
  LabelGroup,
  Stack,
  StackItem,
  Timestamp,
  TimestampFormat,
  Tooltip,
  Truncate,
} from '@patternfly/react-core';
import { ExclamationCircleIcon, SecurityIcon } from '@patternfly/react-icons';
import { ActionsColumn, Td, Tr } from '@patternfly/react-table';

import LabelsList from '../LabelsList';
import PendingProposalsLabel from './PendingProposalsLabel';
import { getPolicySummary } from './SandboxEgressSummary';
import StatusDot from '../StatusDot';
import { formatAge } from '../../utils/formatters';
import { useI18n } from '../../i18n';
import {
  canResumeSandbox,
  canStartSandbox,
  canStopSandbox,
  canSuspendSandbox,
  getConfigurationRejection,
  sandboxPhaseTone,
} from '../../utils/sandboxLifecycle';
import type {
  DraftSandboxSummary,
  Sandbox,
  SandboxPolicyView,
} from '../../types';

type PolicySummary = ReturnType<typeof getPolicySummary>;

type SandboxTableRowProps = {
  sandbox: Sandbox;
  rowIndex: number;
  isSelected: boolean;
  onSelect: (isSelecting: boolean) => void;
  onNameClick?: () => void;
  onDelete: () => void;
  onStop?: () => void;
  onStart?: () => void;
  onSuspend?: () => void;
  onResume?: () => void;
  onViewLogs: () => void;
  onOpenTerminal?: () => void;
  policyView?: SandboxPolicyView;
  // The sandbox's pending rule proposals, when it has any or they could not
  // be read, and the way to its proposals.
  draftSummary?: DraftSandboxSummary;
  onReviewDrafts?: () => void;
};

// An image pinned by digest is some eighty characters that do not wrap, which
// is wider than the table has room for. The cut text is in the tooltip.
const IMAGE_MAX_CHARS = 32;

const getStatusText = (sandbox: Sandbox): string => {
  const { phase, exitCode, conditions } = sandbox.status;
  if (phase === 'READY') return 'Ready';
  if (phase === 'COMPLETED') return 'Completed';
  if (phase === 'ERROR') {
    return conditions?.find((c) => c.reason)?.reason ?? 'Error';
  }
  if (phase === 'STOPPED' && exitCode !== undefined) {
    return `STOPPED (exit ${exitCode})`;
  }
  return phase;
};

const SandboxTableRow: React.FC<SandboxTableRowProps> = ({
  sandbox,
  rowIndex,
  isSelected,
  onSelect,
  onNameClick,
  onDelete,
  onStop,
  onStart,
  onSuspend,
  onResume,
  onViewLogs,
  onOpenTerminal,
  policyView,
  draftSummary,
  onReviewDrafts,
}) => {
  const { t } = useI18n('sandboxes');
  const pc: PolicySummary = getPolicySummary(
    policyView,
    sandbox.spec.policy,
    sandbox.status.currentPolicyVersion,
  );
  const { name, createdAtMs } = sandbox.metadata;
  const { phase, exitCode } = sandbox.status;
  const providers = sandbox.spec.providers ?? [];
  const imageParts = (sandbox.spec.image || '').split('/');
  const imageShort = imageParts[imageParts.length - 1] || '-';
  const isFailure = sandboxPhaseTone(phase, exitCode) === 'danger';
  const rejection = getConfigurationRejection(sandbox);

  const actionItems = [
    ...(onOpenTerminal ? [{ title: 'Terminal', onClick: onOpenTerminal }] : []),
    { title: 'Logs', onClick: onViewLogs },
    ...(onResume && canResumeSandbox(phase)
      ? [{ title: t('lifecycle.resume'), onClick: onResume }]
      : []),
    ...(onStart && canStartSandbox(phase)
      ? [{ title: 'Start', onClick: onStart }]
      : []),
    ...(onSuspend && canSuspendSandbox(phase)
      ? [{ title: t('lifecycle.suspend'), onClick: onSuspend }]
      : []),
    ...(onStop && canStopSandbox(phase)
      ? [{ title: 'Stop', onClick: onStop }]
      : []),
    { title: 'Delete', onClick: onDelete },
  ];

  const invalidConfig = rejection && (
    <Label
      color="red"
      isCompact
      icon={<ExclamationCircleIcon />}
      data-testid={`sandbox-invalid-config-${name}`}
    >
      Invalid config
    </Label>
  );

  return (
    <Tr>
      <Td
        select={{
          rowIndex,
          onSelect: (_event, isSelecting) => onSelect(isSelecting),
          isSelected,
        }}
      />
      <Td dataLabel="Name">
        <Stack>
          <StackItem>
            <Flex
              alignItems={{ default: 'alignItemsCenter' }}
              gap={{ default: 'gapSm' }}
            >
              <FlexItem>
                <Button
                  variant="link"
                  isInline
                  onClick={onNameClick}
                  data-testid={`sandbox-link-${name}`}
                >
                  {name}
                </Button>
              </FlexItem>
              <FlexItem>
                <PendingProposalsLabel
                  sandboxName={name}
                  summary={draftSummary}
                  onReview={onReviewDrafts}
                />
              </FlexItem>
            </Flex>
          </StackItem>
          <StackItem>
            <Content
              component="small"
              className="pf-v6-u-font-family-monospace"
              data-testid={`sandbox-image-${name}`}
            >
              <Truncate
                content={imageShort}
                maxCharsDisplayed={IMAGE_MAX_CHARS}
                position="middle"
              />
            </Content>
          </StackItem>
        </Stack>
      </Td>
      <Td dataLabel="Status">
        <Stack>
          <StackItem>
            <Flex
              alignItems={{ default: 'alignItemsCenter' }}
              gap={{ default: 'gapSm' }}
              flexWrap={{ default: 'nowrap' }}
            >
              <FlexItem>
                <StatusDot phase={phase} exitCode={exitCode} />
              </FlexItem>
              <FlexItem
                className={
                  isFailure ? 'pf-v6-u-text-color-status-danger' : undefined
                }
                data-testid={`sandbox-status-${name}`}
              >
                {getStatusText(sandbox)}
              </FlexItem>
            </Flex>
          </StackItem>
          {invalidConfig && (
            <StackItem>
              {rejection?.message ? (
                <Tooltip content={rejection.message}>{invalidConfig}</Tooltip>
              ) : (
                invalidConfig
              )}
            </StackItem>
          )}
        </Stack>
      </Td>
      <Td dataLabel="Policy">
        <Stack>
          <StackItem>
            <Flex
              alignItems={{ default: 'alignItemsCenter' }}
              gap={{ default: 'gapSm' }}
              flexWrap={{ default: 'nowrap' }}
            >
              <FlexItem>
                <SecurityIcon style={{ color: pc.iconColor }} />
              </FlexItem>
              <FlexItem>
                <strong>{pc.title}</strong>
              </FlexItem>
            </Flex>
          </StackItem>
          {pc.subtitle && (
            <StackItem>
              <Content component="small">{pc.subtitle}</Content>
            </StackItem>
          )}
        </Stack>
      </Td>
      <Td dataLabel="Providers">
        {providers.length > 0 ? (
          <LabelGroup numLabels={2}>
            {providers.map((p) => (
              <Label key={p} color="teal" isCompact>
                {p}
              </Label>
            ))}
          </LabelGroup>
        ) : (
          <Content component="small">—</Content>
        )}
      </Td>
      <Td dataLabel="Labels">
        <LabelsList labels={sandbox.metadata.labels} numLabels={2} />
      </Td>
      <Td dataLabel="Created">
        {createdAtMs ? (
          <Stack>
            <StackItem>
              <Timestamp
                date={new Date(createdAtMs)}
                dateFormat={TimestampFormat.medium}
                timeFormat={TimestampFormat.short}
                data-testid={`sandbox-created-${name}`}
              />
            </StackItem>
            <StackItem>
              <Content component="small">{formatAge(createdAtMs)} ago</Content>
            </StackItem>
          </Stack>
        ) : (
          '-'
        )}
      </Td>
      <Td isActionCell>
        <ActionsColumn items={actionItems} />
      </Td>
    </Tr>
  );
};

export default SandboxTableRow;
