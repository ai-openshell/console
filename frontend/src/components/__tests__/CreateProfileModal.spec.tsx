import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';

import CreateProfileModal from '../CreateProfileModal';
import type { ImportProfileRequest } from '../../types';

const mockImport = jest.fn();

jest.mock('../../api/providers', () => ({
  useImportProviderProfiles: jest.fn(() => ({
    mutate: mockImport,
    reset: jest.fn(),
    isPending: false,
    isError: false,
    error: null,
    data: undefined,
  })),
}));

const renderModal = () =>
  render(<CreateProfileModal workspace="team-a" isOpen onClose={jest.fn()} />);

const fillRequired = () => {
  fireEvent.change(screen.getByTestId('profile-id-input'), {
    target: { value: 'custom' },
  });
  fireEvent.change(screen.getByTestId('profile-display-name-input'), {
    target: { value: 'Custom' },
  });
};

// Types into a field one character at a time, each on top of what the field
// shows by then, which is how a person types and what a field that rewrites
// its own value gets wrong.
const type = (testId: string, text: string) => {
  for (const character of text) {
    const field = screen.getByTestId(testId) as HTMLInputElement;
    fireEvent.change(field, { target: { value: field.value + character } });
  }
};

const submitted = (): ImportProfileRequest =>
  (mockImport.mock.calls[0][0] as ImportProfileRequest[])[0];

beforeEach(() => jest.clearAllMocks());

// A credential may be injected under several environment variables, and the
// gateway takes its value under any of them. They are written in one field,
// separated by commas.
describe('CreateProfileModal environment variables', () => {
  it('takes a comma as it is typed', () => {
    renderModal();
    fireEvent.click(screen.getByTestId('cred-add'));

    type('cred-env-0', 'GITHUB_TOKEN,');
    expect(screen.getByTestId('cred-env-0')).toHaveValue('GITHUB_TOKEN,');

    type('cred-env-0', ' GH_TOKEN');
    expect(screen.getByTestId('cred-env-0')).toHaveValue(
      'GITHUB_TOKEN, GH_TOKEN',
    );
  });

  it('sends each variable that was typed, in order', () => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('cred-add'));
    type('cred-name-0', 'api_token');
    type('cred-env-0', 'GITHUB_TOKEN, GH_TOKEN,GITHUB_PAT');
    fireEvent.click(screen.getByTestId('create-profile-submit'));

    expect(submitted().credentials).toEqual([
      {
        name: 'api_token',
        required: false,
        envVars: ['GITHUB_TOKEN', 'GH_TOKEN', 'GITHUB_PAT'],
      },
    ]);
  });

  it('leaves out what is between two commas and nothing else', () => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('cred-add'));
    type('cred-name-0', 'api_token');
    type('cred-env-0', ' , GITHUB_TOKEN ,, GH_TOKEN , ');
    fireEvent.click(screen.getByTestId('create-profile-submit'));

    expect(submitted().credentials?.[0].envVars).toEqual([
      'GITHUB_TOKEN',
      'GH_TOKEN',
    ]);
  });

  // A credential the gateway brokers without injecting it declares none, and
  // is stored under its name.
  it('sends none for a credential that names none', () => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('cred-add'));
    type('cred-name-0', 'subject_token');
    fireEvent.click(screen.getByTestId('cred-required-0'));
    fireEvent.click(screen.getByTestId('create-profile-submit'));

    expect(submitted().credentials).toEqual([
      { name: 'subject_token', required: true },
    ]);
  });

  it('keeps each credential its own variables when one above it is removed', () => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('cred-add'));
    fireEvent.click(screen.getByTestId('cred-add'));
    type('cred-name-0', 'first');
    type('cred-env-0', 'FIRST');
    type('cred-name-1', 'second');
    type('cred-env-1', 'SECOND, SECOND_ALT');

    fireEvent.click(screen.getByTestId('cred-remove-0'));

    expect(screen.getByTestId('cred-env-0')).toHaveValue('SECOND, SECOND_ALT');
    fireEvent.click(screen.getByTestId('create-profile-submit'));
    expect(submitted().credentials).toEqual([
      { name: 'second', required: false, envVars: ['SECOND', 'SECOND_ALT'] },
    ]);
  });

  it('sends the rest of the profile as before', () => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('create-profile-submit'));

    expect(submitted()).toEqual({
      id: 'custom',
      displayName: 'Custom',
      description: undefined,
      category: 'OTHER',
      inferenceCapable: false,
      credentials: undefined,
      endpoints: undefined,
    });
  });
});

// A profile endpoint is a policy NetworkEndpoint, and the gateway refuses a
// credentialed one that is L4 only unless it allows uninspected credentials.
describe('CreateProfileModal network endpoints', () => {
  const addCredentialedEndpoint = () => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('cred-add'));
    type('cred-name-0', 'api_key');
    type('cred-env-0', 'CUSTOMER_API_KEY');
    fireEvent.click(screen.getByTestId('endpoint-add'));
    type('endpoint-host-0', 'api.example.com');
  };

  it('sends an inspected REST endpoint with an access preset by default', () => {
    addCredentialedEndpoint();
    fireEvent.click(screen.getByTestId('create-profile-submit'));

    expect(submitted().endpoints).toBeUndefined();
    expect(submitted().networkEndpoints).toEqual([
      {
        host: 'api.example.com',
        port: 443,
        protocol: 'rest',
        access: 'NETWORK_ACCESS_PRESET_READ_WRITE',
      },
    ]);
  });

  it('sends the access preset that was chosen', () => {
    addCredentialedEndpoint();
    fireEvent.change(screen.getByTestId('endpoint-protocol-0'), {
      target: { value: 'websocket' },
    });
    fireEvent.change(screen.getByTestId('endpoint-access-0'), {
      target: { value: 'NETWORK_ACCESS_PRESET_FULL' },
    });
    fireEvent.click(screen.getByTestId('create-profile-submit'));

    expect(submitted().networkEndpoints).toEqual([
      {
        host: 'api.example.com',
        port: 443,
        protocol: 'websocket',
        access: 'NETWORK_ACCESS_PRESET_FULL',
      },
    ]);
  });

  it('warns about a credentialed L4 endpoint until uninspected credentials are allowed', () => {
    addCredentialedEndpoint();
    fireEvent.change(screen.getByTestId('endpoint-protocol-0'), {
      target: { value: '' },
    });
    expect(
      screen.getByTestId('endpoint-uninspected-warning-0'),
    ).toBeInTheDocument();
    expect(screen.queryByTestId('endpoint-access-0')).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('endpoint-uninspected-0'));
    expect(
      screen.queryByTestId('endpoint-uninspected-warning-0'),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByTestId('create-profile-submit'));
    expect(submitted().networkEndpoints).toEqual([
      { host: 'api.example.com', port: 443, allowUninspectedCredentials: true },
    ]);
  });

  it('does not warn about an L4 endpoint of a profile without credentials', () => {
    renderModal();
    fireEvent.click(screen.getByTestId('endpoint-add'));
    fireEvent.change(screen.getByTestId('endpoint-protocol-0'), {
      target: { value: '' },
    });
    expect(
      screen.queryByTestId('endpoint-uninspected-warning-0'),
    ).not.toBeInTheDocument();
  });

  it.each([
    'api.example.com/',
    'https://api.example.com',
    'api.example.com/v1',
  ])('refuses the host %s before it is sent', (host) => {
    renderModal();
    fillRequired();
    fireEvent.click(screen.getByTestId('endpoint-add'));
    fireEvent.change(screen.getByTestId('endpoint-host-0'), {
      target: { value: host },
    });

    expect(screen.getByTestId('endpoint-host-error-0')).toBeInTheDocument();
    expect(screen.getByTestId('create-profile-submit')).toBeDisabled();
  });
});
