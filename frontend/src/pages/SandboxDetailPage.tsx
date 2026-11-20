import { useMemo, useState } from 'react';
import {
  Alert,
  Bullseye,
  Button,
  Card,
  CardBody,
  CardTitle,
  DescriptionList,
  DescriptionListDescription,
  DescriptionListGroup,
  DescriptionListTerm,
  Flex,
  FlexItem,
  Label,
  LabelGroup,
  PageSection,
  Spinner,
  Stack,
  StackItem,
  Tab,
  TabTitleText,
  Tabs,
  Title,
} from '@patternfly/react-core';
import { Table, Tbody, Td, Th, Thead, Tr } from '@patternfly/react-table';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { useFeatureFlags } from '../api/auth';
import type { ApiError } from '../api/client';
import { useDraftPolicy, useSandboxPolicy } from '../api/policy';
import { useProviderExpiry } from '../api/providers';
import {
  useDeleteSandbox,
  useSandbox,
  useResumeSandbox,
  useStartSandbox,
  useStopSandbox,
  useSuspendSandbox,
} from '../api/sandboxes';
import { useAlerts } from '../app/AlertContext';
import ConfirmDeleteModal from '../components/ConfirmDeleteModal';
import ConnectCard from '../components/ConnectCard';
import LabelsList from '../components/LabelsList';
import PhaseLabel from '../components/PhaseLabel';
import RefreshErrorAlert, {
  isRefreshError,
} from '../components/RefreshErrorAlert';
import SandboxAttention from '../components/sandbox/SandboxAttention';
import SandboxDraftsTab from '../components/SandboxDraftsTab';
import SandboxFilesTab from '../components/sandbox/SandboxFilesTab';
import SandboxLogsTab from '../components/sandbox/SandboxLogsTab';
import SandboxTerminalTab from '../components/sandbox/SandboxTerminalTab';
import PolicyRuleEditor from '../components/PolicyRuleEditor';
import SandboxProvidersTab from '../components/sandbox/SandboxProvidersTab';
import SandboxServicesTab from '../components/sandbox/SandboxServicesTab';
import SandboxSettingsTab from '../components/sandbox/SandboxSettingsTab';
import SandboxSpecDetails from '../components/sandbox/SandboxSpecDetails';
import SandboxStatusCards from '../components/sandbox/SandboxStatusCards';
import { deletionOutcome, describeDeletion } from '../utils/deletion';
import { formatAge, formatTimestamp } from '../utils/formatters';
import { useI18n } from '../i18n';
import {
  canResumeSandbox,
  canStartSandbox,
  canStopSandbox,
  canSuspendSandbox,
} from '../utils/sandboxLifecycle';

type SandboxDetailPageProps = {
  workspace: string;
  sandboxName: string;
  activeTab?: string;
  onTabChange?: (tab: string) => void;
  // Called once the sandbox has been deleted from this page. Without it the
  // page goes back to the workspace, whose first tab is its sandbox list.
  onDeleted?: () => void;
};

const SandboxDetailPage: React.FC<SandboxDetailPageProps> = (props) => {
  const { workspace, sandboxName } = props;
  const navigate = useNavigate();
  const sandbox = useSandbox(workspace, sandboxName);
  const stopSandbox = useStopSandbox(workspace);
  const startSandbox = useStartSandbox(workspace);
  const suspendSandbox = useSuspendSandbox(workspace);
  const resumeSandbox = useResumeSandbox(workspace);
  const { t } = useI18n('sandboxes');
  const deleteSandbox = useDeleteSandbox(workspace);
  const [isDeleteOpen, setDeleteOpen] = useState(false);
  const { addAlert, addDanger } = useAlerts();
  const features = useFeatureFlags();
  const policyQuery = useSandboxPolicy(workspace, sandboxName);
  const draftsQuery = useDraftPolicy(workspace, sandboxName);
  const providerExpiry = useProviderExpiry(workspace);
  const [searchParams, setSearchParams] = useSearchParams();
  const resolvedTab = props.activeTab ?? (searchParams.get('tab') || 'details');
  const setActiveTab = (key: string | number) => {
    const tab = String(key);
    if (props.onTabChange) {
      props.onTabChange(tab);
    } else {
      setSearchParams(tab === 'details' ? {} : { tab }, { replace: true });
    }
  };

  // The terminal opens a WebSocket and a shell in the sandbox as soon as it is
  // mounted, so it is mounted when its tab is first shown and not with the
  // page: most visits never open it. From then on it stays mounted, like every
  // other tab, so that a session survives a look at another tab. It is kept
  // per sandbox, so that going on to another sandbox starts without one.
  const terminalOf = `${workspace}/${sandboxName}`;
  const [terminalOpenedFor, setTerminalOpenedFor] = useState<string | null>(
    null,
  );
  if (resolvedTab === 'terminal' && terminalOpenedFor !== terminalOf) {
    setTerminalOpenedFor(terminalOf);
  }
  const isTerminalMounted =
    resolvedTab === 'terminal' || terminalOpenedFor === terminalOf;

  const draftSummary = useMemo(() => {
    const chunks = draftsQuery.data?.chunks ?? [];
    const pending = chunks.filter((c) => c.status === 'pending');
    if (pending.length === 0) return undefined;
    return {
      workspace,
      sandboxName,
      pendingCount: pending.length,
      hasSecurityFlags: pending.some((c) => !!c.securityNotes),
      latestDraftMs: Math.max(...pending.map((c) => c.createdAtMs ?? 0)),
    };
  }, [draftsQuery.data, workspace, sandboxName]);

  if (sandbox.isLoading) {
    return (
      <PageSection>
        <Bullseye>
          <Spinner aria-label="Loading sandbox" />
        </Bullseye>
      </PageSection>
    );
  }

  // The sandbox is re-read every few seconds. A refresh that fails leaves the
  // page as it was, with a note under the heading: every tab holds something
  // that must not be thrown away because one request in the background did
  // not get through (a terminal session, an upload, a form half filled in).
  // Only a sandbox that never loaded, or one the gateway now says is gone,
  // takes the page.
  const isGone =
    sandbox.isError && (sandbox.error as ApiError | null)?.status === 404;
  const refreshFailed = isRefreshError(sandbox) && !isGone;
  if (sandbox.isError && !refreshFailed) {
    return (
      <PageSection>
        <Alert
          variant="danger"
          title={
            isGone && sandbox.data
              ? `Sandbox ${sandboxName} no longer exists`
              : `Failed to load sandbox ${sandboxName}`
          }
          actionLinks={
            <Button variant="link" onClick={() => sandbox.refetch()}>
              Retry
            </Button>
          }
        >
          {(sandbox.error as Error).message}
        </Alert>
      </PageSection>
    );
  }

  const data = sandbox.data;
  if (!data) {
    return null;
  }

  return (
    <>
      <PageSection>
        <Flex
          alignItems={{ default: 'alignItemsCenter' }}
          gap={{ default: 'gapMd' }}
        >
          <FlexItem>
            <Title headingLevel="h1">{data.metadata.name}</Title>
          </FlexItem>
          <FlexItem>
            <PhaseLabel
              phase={data.status.phase}
              exitCode={data.status.exitCode}
            />
          </FlexItem>
          {canResumeSandbox(data.status.phase) && (
            <FlexItem align={{ default: 'alignRight' }}>
              <Button
                variant="secondary"
                data-testid="sandbox-resume-button"
                isLoading={resumeSandbox.isPending}
                isDisabled={resumeSandbox.isPending}
                onClick={() =>
                  resumeSandbox.mutate(sandboxName, {
                    onError: (err) =>
                      addDanger(
                        t('lifecycle.resumeFailed', {
                          name: sandboxName,
                          message: (err as Error).message,
                        }),
                      ),
                  })
                }
              >
                {t('lifecycle.resume')}
              </Button>
            </FlexItem>
          )}
          {canSuspendSandbox(data.status.phase) && (
            <FlexItem align={{ default: 'alignRight' }}>
              <Button
                variant="secondary"
                data-testid="sandbox-suspend-button"
                isLoading={suspendSandbox.isPending}
                isDisabled={suspendSandbox.isPending}
                onClick={() =>
                  suspendSandbox.mutate(sandboxName, {
                    onError: (err) =>
                      addDanger(
                        t('lifecycle.suspendFailed', {
                          name: sandboxName,
                          message: (err as Error).message,
                        }),
                      ),
                  })
                }
              >
                {t('lifecycle.suspend')}
              </Button>
            </FlexItem>
          )}
          <FlexItem
            align={
              canResumeSandbox(data.status.phase) ||
              canSuspendSandbox(data.status.phase)
                ? undefined
                : { default: 'alignRight' }
            }
          >
            {canStartSandbox(data.status.phase) ? (
              <Button
                variant="secondary"
                data-testid="sandbox-start-button"
                isLoading={startSandbox.isPending}
                isDisabled={startSandbox.isPending}
                onClick={() =>
                  startSandbox.mutate(sandboxName, {
                    onError: (err) =>
                      addDanger(
                        `Failed to start sandbox: ${(err as Error).message}`,
                      ),
                  })
                }
              >
                Start
              </Button>
            ) : (
              <Button
                variant="secondary"
                data-testid="sandbox-stop-button"
                isLoading={stopSandbox.isPending}
                isDisabled={
                  stopSandbox.isPending || !canStopSandbox(data.status.phase)
                }
                onClick={() =>
                  stopSandbox.mutate(sandboxName, {
                    onError: (err) =>
                      addDanger(
                        `Failed to stop sandbox: ${(err as Error).message}`,
                      ),
                  })
                }
              >
                Stop
              </Button>
            )}
          </FlexItem>
          <FlexItem>
            <Button
              variant="secondary"
              isDanger
              data-testid="sandbox-delete-button"
              onClick={() => setDeleteOpen(true)}
            >
              Delete
            </Button>
          </FlexItem>
        </Flex>
        {refreshFailed && (
          <RefreshErrorAlert
            title="The sandbox could not be refreshed"
            error={sandbox.error}
            onRetry={() => sandbox.refetch()}
            className="pf-v6-u-mt-md"
            data-testid="sandbox-refresh-error"
          />
        )}
      </PageSection>
      <ConfirmDeleteModal
        title="Delete sandbox?"
        body={`Sandbox "${sandboxName}" will be permanently deleted. This cannot be undone.`}
        confirmName={sandboxName}
        isOpen={isDeleteOpen}
        isDeleting={deleteSandbox.isPending}
        error={
          deleteSandbox.isError
            ? (deleteSandbox.error as Error).message
            : undefined
        }
        onConfirm={() =>
          deleteSandbox.mutate(sandboxName, {
            onSuccess: (result) => {
              // What the gateway did, which is often only to accept the
              // delete and clean up afterwards.
              const notice = describeDeletion(
                { singular: 'sandbox', plural: 'sandboxes' },
                sandboxName,
                deletionOutcome(result),
              );
              addAlert(notice.title, notice.variant);
              setDeleteOpen(false);
              if (props.onDeleted) {
                props.onDeleted();
              } else {
                navigate(`/workspaces/${workspace}`);
              }
            },
          })
        }
        onCancel={() => {
          deleteSandbox.reset();
          setDeleteOpen(false);
        }}
      />
      <SandboxAttention
        sandbox={data}
        draftSummary={draftSummary}
        policyView={policyQuery.data}
        providerExpiry={providerExpiry}
        // The banner sits above the tabs, so on the Proposals tab the link
        // leads to the page the reader is already on. No link then: the
        // count is what the banner is for, and the proposals are below it.
        onReviewDrafts={
          resolvedTab === 'proposals'
            ? undefined
            : () => setActiveTab('proposals')
        }
        // The same on the Logs tab: the link would lead where the reader is.
        onViewLogs={
          resolvedTab === 'logs' ? undefined : () => setActiveTab('logs')
        }
        mode="detail"
        // No padding of its own: the banner then sits the heading's bottom
        // padding below the heading and the tabs' top padding above the
        // tabs, which are the same, so it is centred between the two. Its
        // own padding on either side added to one of those and not the
        // other, which is what put it against the heading.
        wrapper={(children) => (
          <PageSection className="pf-v6-u-py-0">{children}</PageSection>
        )}
      />
      <PageSection>
        <Tabs
          activeKey={resolvedTab}
          onSelect={(_event, key) => setActiveTab(key)}
          aria-label="Sandbox detail"
        >
          <Tab
            eventKey="details"
            title={<TabTitleText>Details</TabTitleText>}
            data-testid="tab-details"
          >
            <Stack hasGutter className="pf-v6-u-pt-lg">
              <StackItem>
                <Card data-testid="sandbox-details-card">
                  <CardTitle>Details</CardTitle>
                  <CardBody>
                    <DescriptionList isHorizontal>
                      <DescriptionListGroup>
                        <DescriptionListTerm>ID</DescriptionListTerm>
                        <DescriptionListDescription>
                          {data.metadata.id}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>Workspace</DescriptionListTerm>
                        <DescriptionListDescription>
                          {data.metadata.workspace || workspace}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>Image</DescriptionListTerm>
                        <DescriptionListDescription>
                          {data.spec.image || '-'}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>Created</DescriptionListTerm>
                        <DescriptionListDescription>
                          {formatTimestamp(data.metadata.createdAtMs)}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>Age</DescriptionListTerm>
                        <DescriptionListDescription data-testid="sandbox-age">
                          {formatAge(data.metadata.createdAtMs)}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>
                          Active policy version
                        </DescriptionListTerm>
                        <DescriptionListDescription>
                          {data.status.currentPolicyVersion || '-'}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      {data.status.exitCode !== undefined && (
                        <DescriptionListGroup>
                          <DescriptionListTerm>Exit code</DescriptionListTerm>
                          <DescriptionListDescription
                            data-testid="sandbox-exit-code"
                            className="pf-v6-u-font-family-monospace"
                          >
                            {data.status.exitCode}
                          </DescriptionListDescription>
                        </DescriptionListGroup>
                      )}
                      <DescriptionListGroup>
                        <DescriptionListTerm>
                          Attached providers
                        </DescriptionListTerm>
                        <DescriptionListDescription>
                          {(data.spec.providers ?? []).length > 0 ? (
                            <LabelGroup>
                              {(data.spec.providers ?? []).map((provider) => (
                                <Label key={provider} color="blue">
                                  {provider}
                                </Label>
                              ))}
                            </LabelGroup>
                          ) : (
                            'None'
                          )}
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <DescriptionListGroup>
                        <DescriptionListTerm>Labels</DescriptionListTerm>
                        <DescriptionListDescription>
                          <LabelsList labels={data.metadata.labels} />
                        </DescriptionListDescription>
                      </DescriptionListGroup>
                      <SandboxSpecDetails sandbox={data} />
                    </DescriptionList>
                  </CardBody>
                </Card>
              </StackItem>
              <StackItem>
                <Card data-testid="sandbox-conditions-card">
                  <CardTitle>Conditions</CardTitle>
                  <CardBody>
                    <Table aria-label="Sandbox conditions" variant="compact">
                      <Thead>
                        <Tr>
                          <Th>Type</Th>
                          <Th>Status</Th>
                          <Th>Reason</Th>
                          <Th>Message</Th>
                          <Th>Last transition</Th>
                        </Tr>
                      </Thead>
                      <Tbody>
                        {(data.status.conditions ?? []).map((condition) => (
                          <Tr key={condition.type}>
                            <Td dataLabel="Type">{condition.type}</Td>
                            <Td dataLabel="Status">{condition.status}</Td>
                            <Td dataLabel="Reason">
                              {condition.reason || '-'}
                            </Td>
                            <Td dataLabel="Message">
                              {condition.message || '-'}
                            </Td>
                            <Td dataLabel="Last transition">
                              {condition.lastTransitionTime || '-'}
                            </Td>
                          </Tr>
                        ))}
                        {(data.status.conditions ?? []).length === 0 && (
                          <Tr>
                            <Td colSpan={5}>No conditions reported</Td>
                          </Tr>
                        )}
                      </Tbody>
                    </Table>
                  </CardBody>
                </Card>
              </StackItem>
              <SandboxStatusCards sandbox={data} />
              <StackItem>
                <ConnectCard
                  sandboxName={data.metadata.name}
                  workspace={workspace}
                />
              </StackItem>
            </Stack>
          </Tab>
          <Tab
            eventKey="logs"
            title={<TabTitleText>Logs</TabTitleText>}
            data-testid="tab-logs"
          >
            <div className="pf-v6-u-pt-lg">
              <SandboxLogsTab
                workspace={workspace}
                sandboxName={sandboxName}
                isActive={resolvedTab === 'logs'}
              />
            </div>
          </Tab>
          {features.terminal && (
            <Tab
              eventKey="terminal"
              title={<TabTitleText>Terminal</TabTitleText>}
              data-testid="tab-terminal"
            >
              <div className="pf-v6-u-pt-lg">
                {isTerminalMounted && (
                  <SandboxTerminalTab
                    workspace={workspace}
                    sandboxName={sandboxName}
                  />
                )}
              </div>
            </Tab>
          )}
          <Tab
            eventKey="providers"
            title={<TabTitleText>Providers</TabTitleText>}
            data-testid="tab-sandbox-providers"
          >
            <div className="pf-v6-u-pt-lg">
              <SandboxProvidersTab
                workspace={workspace}
                sandboxName={sandboxName}
              />
            </div>
          </Tab>
          <Tab
            eventKey="policy"
            title={<TabTitleText>Policy</TabTitleText>}
            data-testid="tab-policy"
          >
            <div className="pf-v6-u-pt-lg">
              <PolicyRuleEditor
                workspace={workspace}
                sandboxName={sandboxName}
              />
            </div>
          </Tab>
          {features.draftPolicy && (
            <Tab
              eventKey="proposals"
              title={<TabTitleText>Proposals</TabTitleText>}
              data-testid="tab-proposals"
            >
              <div className="pf-v6-u-pt-lg">
                <SandboxDraftsTab
                  workspace={workspace}
                  sandboxName={sandboxName}
                />
              </div>
            </Tab>
          )}
          {features.services && (
            <Tab
              eventKey="services"
              title={<TabTitleText>Services</TabTitleText>}
              data-testid="tab-services"
            >
              <div className="pf-v6-u-pt-lg">
                <SandboxServicesTab
                  workspace={workspace}
                  sandboxName={sandboxName}
                />
              </div>
            </Tab>
          )}
          {features.settings && (
            <Tab
              eventKey="settings"
              title={<TabTitleText>Settings</TabTitleText>}
              data-testid="tab-sandbox-settings"
            >
              <div className="pf-v6-u-pt-lg">
                {/* Read only while the tab is open: it is one more call to
                    the gateway that most visits to this page do not need. */}
                {resolvedTab === 'settings' && (
                  <SandboxSettingsTab
                    workspace={workspace}
                    sandboxName={sandboxName}
                  />
                )}
              </div>
            </Tab>
          )}
          {features.fileTransfer && (
            <Tab
              eventKey="files"
              title={<TabTitleText>Files</TabTitleText>}
              data-testid="tab-files"
            >
              <div className="pf-v6-u-pt-lg">
                <SandboxFilesTab
                  workspace={workspace}
                  sandboxName={sandboxName}
                />
              </div>
            </Tab>
          )}
        </Tabs>
      </PageSection>
    </>
  );
};

export default SandboxDetailPage;
