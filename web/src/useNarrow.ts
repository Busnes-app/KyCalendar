import { useEffect, useState } from 'react';

const NARROW = '(max-width: 720px)';
const narrow = () => typeof window.matchMedia === 'function' && window.matchMedia(NARROW).matches;

// useNarrow follows the phone breakpoint, where sidebar lists fold into a header disclosure.
export function useNarrow() {
  const [isNarrow, setNarrow] = useState(narrow);
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const m = window.matchMedia(NARROW);
    const sync = () => setNarrow(m.matches);
    m.addEventListener('change', sync);
    return () => m.removeEventListener('change', sync);
  }, []);
  return isNarrow;
}
