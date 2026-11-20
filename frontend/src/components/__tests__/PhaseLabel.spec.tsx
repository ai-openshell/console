import React from 'react';
import { render, screen } from '@testing-library/react';
import PhaseLabel from '../PhaseLabel';
import type { SandboxPhase, WorkspacePhase } from '../../types';

describe('PhaseLabel', () => {
  it.each<[SandboxPhase | WorkspacePhase]>([
    ['READY'],
    ['ACTIVE'],
    ['ERROR'],
    ['PROVISIONING'],
    ['DELETING'],
    ['TERMINATING'],
    ['STOPPING'],
    ['STOPPED'],
    ['STARTING'],
    ['COMPLETED'],
    ['SUSPENDING'],
    ['SUSPENDED'],
    ['UNKNOWN'],
    ['UNSPECIFIED'],
  ])('renders %s phase with correct text', (phase) => {
    render(<PhaseLabel phase={phase} />);
    const label = screen.getByTestId('phase-label');
    expect(label).toHaveTextContent(phase);
  });

  // The class PatternFly gives a label of one color, e.g. "pf-m-green".
  const colorOf = (ui: React.ReactElement): string => {
    const { container, unmount } = render(ui);
    const label = container.querySelector('.pf-v6-c-label');
    const color = Array.from(label?.classList ?? []).find(
      (name) => name.startsWith('pf-m-') && name !== 'pf-m-filled',
    );
    unmount();
    return color ?? '';
  };

  it('shows COMPLETED as the success READY is, not as an unknown phase', () => {
    expect(colorOf(<PhaseLabel phase="COMPLETED" />)).toBe(
      colorOf(<PhaseLabel phase="READY" />),
    );
    expect(colorOf(<PhaseLabel phase="COMPLETED" />)).not.toBe(
      colorOf(<PhaseLabel phase="UNKNOWN" />),
    );
    expect(colorOf(<PhaseLabel phase="COMPLETED" exitCode={0} />)).toBe(
      colorOf(<PhaseLabel phase="READY" />),
    );
  });

  it('shows a STOPPED sandbox with an exit code as the failure ERROR is', () => {
    expect(colorOf(<PhaseLabel phase="STOPPED" exitCode={143} />)).toBe(
      colorOf(<PhaseLabel phase="ERROR" />),
    );
    render(<PhaseLabel phase="STOPPED" exitCode={143} />);
    expect(screen.getByTestId('phase-label')).toHaveTextContent(
      'STOPPED (exit 143)',
    );
  });

  it('shows a sandbox somebody stopped as stopped', () => {
    expect(colorOf(<PhaseLabel phase="STOPPED" />)).not.toBe(
      colorOf(<PhaseLabel phase="ERROR" />),
    );
    expect(colorOf(<PhaseLabel phase="STOPPED" />)).not.toBe(
      colorOf(<PhaseLabel phase="READY" />),
    );
  });

  // As `openshell sandbox list` colors them: a suspend under way like any
  // other work in progress, a suspended sandbox at rest.
  it('shows SUSPENDING as work in progress', () => {
    expect(colorOf(<PhaseLabel phase="SUSPENDING" />)).toBe(
      colorOf(<PhaseLabel phase="STOPPING" />),
    );
  });

  it('shows SUSPENDED as a sandbox at rest, with an icon of its own', () => {
    expect(colorOf(<PhaseLabel phase="SUSPENDED" />)).toBe(
      colorOf(<PhaseLabel phase="STOPPED" />),
    );
    expect(colorOf(<PhaseLabel phase="SUSPENDED" />)).not.toBe(
      colorOf(<PhaseLabel phase="ERROR" />),
    );
    const iconOf = (ui: React.ReactElement): string => {
      const { container, unmount } = render(ui);
      const icon = container.querySelector('.pf-v6-c-label__icon')?.innerHTML;
      unmount();
      return icon ?? '';
    };
    const suspended = iconOf(<PhaseLabel phase="SUSPENDED" />);
    expect(suspended).not.toBe('');
    // Not the bare grey label of a phase this build does not know.
    expect(iconOf(<PhaseLabel phase="UNKNOWN" />)).toBe('');
    expect(suspended).not.toBe(iconOf(<PhaseLabel phase="STOPPED" />));
  });

  it('ignores an exit code on a phase it does not change', () => {
    render(<PhaseLabel phase="ERROR" exitCode={7} />);
    expect(screen.getByTestId('phase-label')).toHaveTextContent(/^ERROR$/);
  });

  it('renders a PF Label with color prop for READY', () => {
    const { container } = render(<PhaseLabel phase="READY" />);
    expect(container.innerHTML).toMatchSnapshot();
  });

  it('renders a PF Label with color prop for ERROR', () => {
    const { container } = render(<PhaseLabel phase="ERROR" />);
    expect(container.innerHTML).toMatchSnapshot();
  });

  it('renders distinct markup for READY vs ERROR', () => {
    const { container: c1 } = render(<PhaseLabel phase="READY" />);
    const { container: c2 } = render(<PhaseLabel phase="ERROR" />);
    expect(c1.innerHTML).not.toBe(c2.innerHTML);
  });

  it('renders distinct markup for PROVISIONING vs DELETING', () => {
    const { container: c1 } = render(<PhaseLabel phase="PROVISIONING" />);
    const { container: c2 } = render(<PhaseLabel phase="DELETING" />);
    expect(c1.innerHTML).not.toBe(c2.innerHTML);
  });

  it('renders UNKNOWN with default/grey styling', () => {
    const { container: cReady } = render(<PhaseLabel phase="READY" />);
    const { container: cUnknown } = render(<PhaseLabel phase="UNKNOWN" />);
    expect(cReady.innerHTML).not.toBe(cUnknown.innerHTML);
  });
});
