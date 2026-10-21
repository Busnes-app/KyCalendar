import React, { useEffect, useState } from 'react';
import { ShieldCheck, Loader2, AlertCircle } from 'lucide-react';

interface CaptchaProps {
  onToken: (token: string) => void;
}

interface Challenge {
  algorithm: string;
  salt: string;
  challenge: string;
  maxnumber: number;
  expires_at: number;
  signature: string;
}

export const CaptchaWidget: React.FC<CaptchaProps> = ({ onToken }) => {
  const [status, setStatus] = useState<'working' | 'done' | 'failed'>('working');
  const [progress, setProgress] = useState<number>(0);
  const [errorMsg, setErrorMsg] = useState<string>('');

  useEffect(() => {
    let cancelled = false;

    const runPoW = async () => {
      try {
        const resp = await fetch('/api/auth/pow-challenge');
        if (!resp.ok) throw new Error('Failed to fetch security challenge');
        const chal: Challenge = await resp.json();

        const enc = new TextEncoder();
        const max = chal.maxnumber;
        let solved = false;

        for (let i = 1; i <= max; i++) {
          if (cancelled) return;
          const target = `${chal.salt}${i}`;
          const hashBuf = await crypto.subtle.digest('SHA-256', enc.encode(target));
          const hashHex = Array.from(new Uint8Array(hashBuf))
            .map((b) => b.toString(16).padStart(2, '0'))
            .join('');

          if (hashHex === chal.challenge) {
            const sol = {
              algorithm: chal.algorithm,
              salt: chal.salt,
              challenge: chal.challenge,
              number: i,
              maxnumber: chal.maxnumber,
              expires_at: chal.expires_at,
              signature: chal.signature,
            };
            const token = btoa(JSON.stringify(sol));
            setStatus('done');
            onToken(token);
            solved = true;
            break;
          }

          if (i % 2000 === 0) {
            setProgress(Math.round((i / max) * 100));
            await new Promise((r) => setTimeout(r, 0)); // yield loop
          }
        }

        if (!solved && !cancelled) {
          throw new Error('Could not solve challenge');
        }
      } catch (err) {
        if (!cancelled) {
          setStatus('failed');
          setErrorMsg(err instanceof Error ? err.message : 'Security check failed');
        }
      }
    };

    runPoW();
    return () => {
      cancelled = true;
    };
  }, [onToken]);

  return (
    <div className={`auth-check ${status === 'done' ? 'ok' : status}`} aria-live="polite">
      {status === 'working' && (
        <>
          <Loader2 size={14} className="animate-spin" aria-hidden="true" />
          <span>Checking this browser… {progress}%</span>
        </>
      )}
      {status === 'done' && (
        <>
          <ShieldCheck size={14} aria-hidden="true" />
          <span>Browser check passed</span>
        </>
      )}
      {status === 'failed' && (
        <>
          <AlertCircle size={14} aria-hidden="true" />
          <span>Browser check failed: {errorMsg}. Reload the page to try again.</span>
        </>
      )}
    </div>
  );
};
