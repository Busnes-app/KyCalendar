import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { LicenseFooter } from './LicenseFooter';
import mitLicenseText from '../mit.txt?raw';

afterEach(cleanup);

describe('LicenseFooter', () => {
  it('opens the About window with the licence and version', () => {
    render(<LicenseFooter version="1.2.3" />);
    const dialog = screen.getByRole('dialog', { hidden: true });
    expect(dialog.hasAttribute('open')).toBe(false);
    fireEvent.click(screen.getByRole('button', { name: /MIT License/ }));
    expect(dialog.hasAttribute('open')).toBe(true);
    expect(screen.getByText('v1.2.3')).toBeTruthy();
    expect((screen.getByLabelText('MIT License') as HTMLTextAreaElement).value).toBe(mitLicenseText);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(dialog.hasAttribute('open')).toBe(false);
  });

  it('shows no version badge without a version', () => {
    render(<LicenseFooter />);
    fireEvent.click(screen.getByRole('button', { name: /MIT License/ }));
    expect(screen.queryByText(/^v\d/)).toBeNull();
  });
});
