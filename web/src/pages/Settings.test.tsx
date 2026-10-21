import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import { Settings } from './Settings';

afterEach(cleanup);

describe('Settings', () => {
  it('shows the version the server reports', () => {
    render(<Settings settings={{ db_driver: 'sqlite', version: '0.5.0' }} />);
    expect(screen.getByText('Version').nextElementSibling?.textContent).toBe('0.5.0');
  });
});
