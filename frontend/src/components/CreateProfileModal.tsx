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
  ProfileEndpoint,
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
  const [endpoints, setEndpoints] = useState<ProfileEndpoint[]>([]);
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
      endpoints: endpoints.length > 0 ? endpoints : undefined,
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
            {endpoints.map((ep, index) => (
              <Grid hasGutter key={index} className="pf-v6-u-mb-sm">
                <GridItem span={6}>
                  <TextInput
                    id={`endpoint-host-${index}`}
                    data-testid={`endpoint-host-${index}`}
                    value={ep.host}
                    onChange={(_event, value) =>
                      setEndpoints((rows) =>
                        rows.map((r, i) =>
                          i === index ? { ...r, host: value } : r,
                        ),
                      )
                    }
                    placeholder="Host (e.g. api.example.com)"
                    aria-label="Endpoint host"
                  />
                </GridItem>
                <GridItem span={3}>
                  <TextInput
                    id={`endpoint-port-${index}`}
                    data-testid={`endpoint-port-${index}`}
                    type="number"
                    value={ep.port ?? ''}
                    onChange={(_event, value) =>
                      setEndpoints((rows) =>
                        rows.map((r, i) =>
                          i === index
                            ? { ...r, port: value ? Number(value) : undefined }
                            : r,
                        ),
                      )
                    }
                    placeholder="Port"
                    aria-label="Endpoint port"
                  />
                </GridItem>
                <GridItem span={3}>
                  <Button
                    variant="link"
                    onClick={() =>
                      setEndpoints((rows) => rows.filter((_, i) => i !== index))
                    }
                    data-testid={`endpoint-remove-${index}`}
                  >
                    Remove
                  </Button>
                </GridItem>
              </Grid>
            ))}
            <Button
              variant="link"
              isInline
              onClick={() => setEndpoints((rows) => [...rows, { host: '' }])}
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
          isDisabled={!id || !displayName || importProfiles.isPending}
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
