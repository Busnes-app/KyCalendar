import { useEffect, useState } from 'react';
import { Modal } from './ui';
import { saveTaskPrefs, taskPrefs, type TaskPrefs } from '../tasksApi';

// The user's switch, on KyCalendar's side, for tasks made from flagged KyPost mail. While KyPost
// is not linked the switch would do nothing, so only the note shows.
export function FlaggedMailDialog({ onClose }: { onClose: () => void }) {
  const [prefs, setPrefs] = useState<TaskPrefs | null>(null);
  const [error, setError] = useState('');
  useEffect(() => { void taskPrefs().then(setPrefs, () => setError('Settings could not be loaded.')); }, []);
  const linked = !!prefs?.kypost_linked;
  return (
    <Modal title="Flagged from mail" onClose={onClose}
      description={linked ? 'Flagging a message in KyPost adds a task to your Inbox list, with a link back to the message. Finishing the task clears the flag.' : undefined}>
      <div className="kc-form">
        {linked && (
          <label className="kc-check">
            <input type="checkbox" role="switch" checked={prefs.kypost_flags}
              onChange={(e) => void saveTaskPrefs(e.target.checked).then(setPrefs, () => setError('The setting could not be saved.'))} />
            Make tasks from flagged mail
          </label>
        )}
        {prefs && !linked && <p role="note">When KyPost is connected to KyCalendar, flagging a message in KyPost adds a task to your Inbox list. Until then, flags stay in KyPost.</p>}
        {error && <p role="alert">{error}</p>}
        <div className="modal-actions"><button type="button" onClick={onClose}>Done</button></div>
      </div>
    </Modal>
  );
}
