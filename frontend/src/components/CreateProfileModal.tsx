import { useState } from 'react';
import {
  Alert,
  Button,
  Checkbox,
  Form,
  FormGroup,
  FormHelperText,
  FormSelect,
  FormSelectOption,
  Grid,
  GridItem,
  HelperText,
  HelperTextItem,
  Modal,
  ModalBody,
  ModalFooter,
  ModalHeader,
  TextArea,
  TextInput,
} from '@patternfly/react-core';

import { useImportProviderProfiles } from '../api/providers';
import type {
  ImportProfileRequest,
  ProfileCredentialInput,
  ProfileNetworkEndpoint,
  ProviderProfileCategory,
} from '../types';

type CreateProfileModalProps = {
  workspace: string;
  isOpen: boolean;
  onClose: () => void;
  onSuccess?: () => void;
};

// A credential as the form holds it. The environment variables are kept as
// the text that was typed and split when the profile is sent: split on every
// keystroke and joined again for display, a comma vanished as soon as it was
// typed, and a second variable could not be entered.
type CredentialRow = {
  name: string;
  envVarsText: string;
  required: boolean;
};

// The environment variables a row names: what stands between the commas,
// without the space around it and without what is empty.
const envVarsOf = (text: string): string[] =>
  text
    .split(',')
    .map((name) => name.trim())
    .filter(Boolean);

// The credential a row describes. One that names no environment variable
// declares none, and is stored under its own name.
const credentialOf = (row: CredentialRow): ProfileCredentialInput => {
  const envVars = envVarsOf(row.envVarsText);
  return {
    name: row.name,
    required: row.required,
    ...(envVars.length > 0 ? { envVars } : {}),
  };
};

// An endpoint as the form holds it. A profile endpoint is the sandbox policy's
// NetworkEndpoint, and the gateway refuses one that carries credentials over
// L4 only unless it says allow_uninspected_credentials, so the form offers the
// protocols it inspects and that opt-out. An inspected protocol needs an
// access preset (or rules, which only a profile file can write).
type EndpointRow = {
  host: string;
  port: string;
  protocol: EndpointProtocol;
  access: string;
  allowUninspectedCredentials: boolean;
};

type EndpointProtocol = 'rest' | 'websocket' | '';

const ENDPOINT_PROTOCOLS: { value: EndpointProtocol; label: string }[] = [
  { value: 'rest', label: 'REST' },
  { value: 'websocket', label: 'WebSocket' },
  { value: '', label: 'Not inspected (L4)' },
];

const ACCESS_PRESETS = [
  { value: 'NETWORK_ACCESS_PRESET_READ_ONLY', label: 'Read-only' },
  { value: 'NETWORK_ACCESS_PRESET_READ_WRITE', label: 'Read-write' },
  { value: 'NETWORK_ACCESS_PRESET_FULL', label: 'Full' },
];

const newEndpointRow = (): EndpointRow => ({
  host: '',
  port: '443',
  protocol: 'rest',
  access: 'NETWORK_ACCESS_PRESET_READ_WRITE',
  allowUninspectedCredentials: false,
});

// What is wrong with a host as typed, if anything. The gateway stores a host
// with a scheme or a path as it is (lint passes `api.example.com/`), and such
// a host never matches a request, so the form says so before it is sent.
const endpointHostProblem = (host: string): string | undefined => {
  const value = host.trim();
  if (!value) return undefined;
  if (value.includes('://')) {
    return 'Enter the host name only, without a scheme like https://';
  }
  if (/[/\s?#]/.test(value)) {
    return 'A host has no path, spaces, query or trailing slash';
  }
  return undefined;
};

// The endpoint a row describes, in the gateway's own spelling. A field the
// chosen protocol does not take is left out rather than sent to be refused.
const endpointOf = (row: EndpointRow): ProfileNetworkEndpoint => {
  const endpoint: ProfileNetworkEndpoint = { host: row.host.trim() };
  const port = Number(row.port);
  if (row.port.trim() && Number.isInteger(port)) endpoint.port = port;
  if (row.protocol) {
    endpoint.protocol = row.protocol;
    endpoint.access = row.access;
  } else if (row.allowUninspectedCredentials) {
    endpoint.allowUninspectedCredentials = true;
  }
  return endpoint;
};

const CATEGORIES: { value: ProviderProfileCategory; label: string }[] = [
  { value: 'INFERENCE', label: 'Inference' },
  { value: 'AGENT', label: 'Agent' },
  { value: 'SOURCE_CONTROL', label: 'Source control' },
  { value: 'MESSAGING', label: 'Messaging' },
  { value: 'DATA', label: 'Data' },
  { value: 'KNOWLEDGE', label: 'Knowledge' },
  { value: 'OTHER', label: 'Other' },
];

const CreateProfileModal: React.FC<CreateProfileModalProps> = ({
  workspace,
  isOpen,
  onClose,
  onSuccess,
}) => {
  const [id, setId] = useState('');
  const [displayName, setDisplayName] = useState('');
  const [description, setDescription] = useState('');
  const [category, setCategory] = useState<ProviderProfileCategory>('OTHER');
  const [inferenceCapable, setInferenceCapable] = useState(false);
  const [credentials, setCredentials] = useState<CredentialRow[]>([]);
  const [endpoints, setEndpoints] = useState<EndpointRow[]>([]);
  const importProfiles = useImportProviderProfiles(workspace);

  const close = () => {
    setId('');
    setDisplayName('');
    setDescription('');
    setCategory('OTHER');
    setInferenceCapable(false);
    setCredentials([]);
    setEndpoints([]);
    importProfiles.reset();
    onClose();
  };

  const submit = () => {
    const profile: ImportProfileRequest = {
      id,
      displayName,
      description: description || undefined,
      category,
      inferenceCapable,
      credentials:
        credentials.length > 0 ? credentials.map(credentialOf) : undefined,
      networkEndpoints:
        endpoints.length > 0 ? endpoints.map(endpointOf) : undefined,
    };
    importProfiles.mutate([profile], {
      onSuccess: (resp) => {
        if (resp.imported) {
          onSuccess?.();
          close();
        }
      },
    });
  };

  const diagnostics = importProfiles.data?.diagnostics ?? [];
  const hasCredentials = credentials.length > 0;
  const hasHostProblem = endpoints.some((ep) => endpointHostProblem(ep.host));
  const updateEndpoint = (index: number, change: Partial<EndpointRow>) =>
    setEndpoints((rows) =>
      rows.map((r, i) => (i === index ? { ...r, ...change } : r)),
    );

  return (
    <Modal
      variant="medium"
      isOpen={isOpen}
      onClose={close}
      aria-label="Create provider profile"
    >
      <ModalHeader title="Create provider profile" />
      <ModalBody>
        <Form
          onSubmit={(event) => {
            event.preventDefault();
            submit();
          }}
        >
          <FormGroup label="Profile ID" isRequired fieldId="profile-id">
            <TextInput
              id="profile-id"
              data-testid="profile-id-input"
              isRequired
              value={id}
              onChange={(_event, value) => setId(value)}
              placeholder="e.g. my-custom-llm"
            />
            <FormHelperText>
              <HelperText>
                <HelperTextItem>
                  Lowercase slug used as the provider type when creating
                  providers
                </HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup
            label="Display name"
            isRequired
            fieldId="profile-display-name"
          >
            <TextInput
              id="profile-display-name"
              data-testid="profile-display-name-input"
              isRequired
              value={displayName}
              onChange={(_event, value) => setDisplayName(value)}
            />
          </FormGroup>
          <FormGroup label="Description" fieldId="profile-description">
            <TextArea
              id="profile-description"
              data-testid="profile-description-input"
              value={description}
              onChange={(_event, value) => setDescription(value)}
              rows={2}
            />
          </FormGroup>
          <FormGroup label="Category" isRequired fieldId="profile-category">
            <FormSelect
              id="profile-category"
              data-testid="profile-category-select"
              value={category}
              onChange={(_event, value) =>
                setCategory(value as ProviderProfileCategory)
              }
            >
              {CATEGORIES.map((cat) => (
                <FormSelectOption
                  key={cat.value}
                  value={cat.value}
                  label={cat.label}
                />
              ))}
            </FormSelect>
          </FormGroup>
          <FormGroup fieldId="profile-inference">
            <Checkbox
              id="profile-inference"
              data-testid="profile-inference-checkbox"
              label="Inference capable"
              isChecked={inferenceCapable}
              onChange={(_event, checked) => setInferenceCapable(checked)}
            />
          </FormGroup>
          <FormGroup
            label="Credentials"
            fieldId="profile-credentials"
            role="group"
          >
            {credentials.map((cred, index) => (
              <Grid hasGutter key={index} className="pf-v6-u-mb-sm">
                <GridItem span={4}>
                  <TextInput
                    id={`cred-name-${index}`}
                    data-testid={`cred-name-${index}`}
                    value={cred.name}
                    onChange={(_event, value) =>
                      setCredentials((rows) =>
                        rows.map((r, i) =>
                          i === index ? { ...r, name: value } : r,
                        ),
                      )
                    }
                    placeholder="Credential name"
                    aria-label="Credential name"
                  />
                </GridItem>
                <GridItem span={4}>
                  <TextInput
                    id={`cred-env-${index}`}
                    data-testid={`cred-env-${index}`}
                    value={cred.envVarsText}
                    onChange={(_event, value) =>
                      setCredentials((rows) =>
                        rows.map((r, i) =>
                          i === index ? { ...r, envVarsText: value } : r,
                        ),
                      )
                    }
                    placeholder="ENV_VARS (comma-separated)"
                    aria-label="Environment variables"
                  />
                </GridItem>
                <GridItem span={2}>
                  <Checkbox
                    id={`cred-required-${index}`}
                    data-testid={`cred-required-${index}`}
                    label="Required"
                    isChecked={cred.required}
                    onChange={(_event, checked) =>
                      setCredentials((rows) =>
                        rows.map((r, i) =>
                          i === index ? { ...r, required: checked } : r,
                        ),
                      )
                    }
                  />
                </GridItem>
                <GridItem span={2}>
                  <Button
                    variant="link"
                    onClick={() =>
                      setCredentials((rows) =>
                        rows.filter((_, i) => i !== index),
                      )
                    }
                    data-testid={`cred-remove-${index}`}
                  >
                    Remove
                  </Button>
                </GridItem>
              </Grid>
            ))}
            <Button
              variant="link"
              isInline
              onClick={() =>
                setCredentials((rows) => [
                  ...rows,
                  { name: '', envVarsText: '', required: false },
                ])
              }
              data-testid="cred-add"
            >
              Add credential
            </Button>
            <FormHelperText>
              <HelperText>
                <HelperTextItem>
                  Credential schema: what secrets providers of this type need
                </HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          <FormGroup
            label="Network endpoints"
            fieldId="profile-endpoints"
            role="group"
          >
            {endpoints.map((ep, index) => {
              const hostProblem = endpointHostProblem(ep.host);
              const uninspected =
                hasCredentials &&
                !ep.protocol &&
                !ep.allowUninspectedCredentials;
              return (
                <Grid hasGutter key={index} className="pf-v6-u-mb-md">
                  <GridItem span={5}>
                    <TextInput
                      id={`endpoint-host-${index}`}
                      data-testid={`endpoint-host-${index}`}
                      value={ep.host}
                      validated={hostProblem ? 'error' : 'default'}
                      onChange={(_event, value) =>
                        updateEndpoint(index, { host: value })
                      }
                      placeholder="Host (e.g. api.example.com)"
                      aria-label="Endpoint host"
                    />
                  </GridItem>
                  <GridItem span={2}>
                    <TextInput
                      id={`endpoint-port-${index}`}
                      data-testid={`endpoint-port-${index}`}
                      type="number"
                      value={ep.port}
                      onChange={(_event, value) =>
                        updateEndpoint(index, { port: value })
                      }
                      placeholder="Port"
                      aria-label="Endpoint port"
                    />
                  </GridItem>
                  <GridItem span={3}>
                    <FormSelect
                      id={`endpoint-protocol-${index}`}
                      data-testid={`endpoint-protocol-${index}`}
                      value={ep.protocol}
                      onChange={(_event, value) =>
                        updateEndpoint(index, {
                          protocol: value as EndpointProtocol,
                        })
                      }
                      aria-label="Endpoint protocol"
                    >
                      {ENDPOINT_PROTOCOLS.map((option) => (
                        <FormSelectOption
                          key={option.value}
                          value={option.value}
                          label={option.label}
                        />
                      ))}
                    </FormSelect>
                  </GridItem>
                  <GridItem span={2}>
                    <Button
                      variant="link"
                      onClick={() =>
                        setEndpoints((rows) =>
                          rows.filter((_, i) => i !== index),
                        )
                      }
                      data-testid={`endpoint-remove-${index}`}
                    >
                      Remove
                    </Button>
                  </GridItem>
                  {hostProblem && (
                    <GridItem span={12}>
                      <HelperText>
                        <HelperTextItem
                          variant="error"
                          data-testid={`endpoint-host-error-${index}`}
                        >
                          {hostProblem}
                        </HelperTextItem>
                      </HelperText>
                    </GridItem>
                  )}
                  <GridItem span={ep.protocol ? 5 : 12}>
                    {ep.protocol ? (
                      <FormSelect
                        id={`endpoint-access-${index}`}
                        data-testid={`endpoint-access-${index}`}
                        value={ep.access}
                        onChange={(_event, value) =>
                          updateEndpoint(index, { access: value })
                        }
                        aria-label="Endpoint access"
                      >
                        {ACCESS_PRESETS.map((option) => (
                          <FormSelectOption
                            key={option.value}
                            value={option.value}
                            label={`Access: ${option.label}`}
                          />
                        ))}
                      </FormSelect>
                    ) : (
                      <Checkbox
                        id={`endpoint-uninspected-${index}`}
                        data-testid={`endpoint-uninspected-${index}`}
                        label="Allow credentials on traffic OpenShell cannot inspect"
                        description="A security-sensitive escape hatch: credential-bearing requests go out without inspection or rewriting."
                        isChecked={ep.allowUninspectedCredentials}
                        onChange={(_event, checked) =>
                          updateEndpoint(index, {
                            allowUninspectedCredentials: checked,
                          })
                        }
                      />
                    )}
                  </GridItem>
                  {uninspected && (
                    <GridItem span={12}>
                      <HelperText>
                        <HelperTextItem
                          variant="warning"
                          data-testid={`endpoint-uninspected-warning-${index}`}
                        >
                          The gateway refuses credentials on an endpoint it
                          cannot inspect. Choose REST or WebSocket, or allow
                          uninspected credentials.
                        </HelperTextItem>
                      </HelperText>
                    </GridItem>
                  )}
                </Grid>
              );
            })}
            <Button
              variant="link"
              isInline
              onClick={() =>
                setEndpoints((rows) => [...rows, newEndpointRow()])
              }
              data-testid="endpoint-add"
            >
              Add endpoint
            </Button>
            <FormHelperText>
              <HelperText>
                <HelperTextItem>
                  Network endpoints sandboxes need to reach for this provider
                </HelperTextItem>
              </HelperText>
            </FormHelperText>
          </FormGroup>
          {importProfiles.isError && (
            <Alert variant="danger" isInline title="Import failed">
              {(importProfiles.error as Error).message}
            </Alert>
          )}
          {diagnostics.length > 0 && (
            <Alert variant="warning" isInline title="Validation diagnostics">
              <ul>
                {diagnostics.map((d, i) => (
                  <li key={i}>
                    {d.field ? `${d.field}: ` : ''}
                    {d.message}
                  </li>
                ))}
              </ul>
            </Alert>
          )}
        </Form>
      </ModalBody>
      <ModalFooter>
        <Button
          variant="primary"
          onClick={submit}
          isDisabled={
            !id || !displayName || hasHostProblem || importProfiles.isPending
          }
          isLoading={importProfiles.isPending}
          data-testid="create-profile-submit"
        >
          Create profile
        </Button>
        <Button variant="link" onClick={close}>
          Cancel
        </Button>
      </ModalFooter>
    </Modal>
  );
};

export default CreateProfileModal;
