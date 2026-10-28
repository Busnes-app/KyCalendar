"""Audit the single 04-confirmation batch; no browser, build, suite or network.
Run once after capture-inventory.mjs 04-confirmation; all outputs are exclusive.
OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1 python3 docs/ui-calendar-eight/capture-round3-confirmation-checks.py
"""
import hashlib
import json
import os
import subprocess
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path

from PIL import Image

D = Path(__file__).resolve().parent
ROOT = D.parent.parent
PRIVATE = Path('/tmp/kycalendar-eight-Fqv5Ll')
HEAD = '3aca6fcf934dacb0149aafce388cf38f02244015'


def sha(b):
    return hashlib.sha256(b).hexdigest()


def read(n):
    return json.loads((D / n).read_text())


def write(n, v):
    with (D / n).open('x') as f:
        json.dump(v, f, indent=2)
        f.write('\n')


def git(*args):
    return subprocess.check_output(['git', *args], cwd=ROOT, text=True).strip()


M = read('manifest-04-confirmation.json')
ST = read('artifacts-04-confirmation.json')
OLD = read('artifacts-v3.json')
BASE = read('manifest-00-before.json')
INSPECT = read('round3-confirmation-per-file-inspection.json')
checks = []


def check(name, passed, evidence):
    checks.append({'name': name, 'status': 'pass' if passed else 'fail', 'evidence': evidence})


protected = []
for r in read('round3-confirmation-preservation-before.json')['files']:
    p = Path(r['file']) if r['file'].startswith('/') else ROOT / r['file']
    actual = sha(p.read_bytes())
    protected.append({**r, 'after_sha256': actual, 'identical': actual == r['sha256'] and p.stat().st_size == r['bytes']})
write('round3-confirmation-preservation-proof.json', {'checked_at': datetime.now(timezone.utc).isoformat(), 'files': protected, 'protected_files': len(protected), 'prior_originals': 156, 'all_identical': all(r['identical'] for r in protected)})
check('all 287 protected files unchanged', len(protected) == 287 and all(r['identical'] for r in protected), 'round3-confirmation-preservation-proof.json; every prior evidence file plus all AGENTS bytes')

check('50 originals match original surface role and interaction order', len(M['files']) == 50 and [(r['observed_surface'], r['viewport'], r['theme'], r['role']) for r in M['files']] == [(r['observed_surface'], r['viewport'], r['theme'], r['role']) for r in BASE['files']], {'originals': len(M['files']), 'stage': '04-confirmation', 'review_round': '3; not inferred from stage number'})
source = []
for r in M['source_before']['files']:
    b = (ROOT / r['file']).read_bytes()
    blob = hashlib.sha1(b'blob ' + str(len(b)).encode() + b'\0' + b).hexdigest()
    expected = git('rev-parse', HEAD + ':' + r['file'])
    source.append({**r, 'terminal_sha256': sha(b), 'git_blob': blob, 'head_blob': expected, 'matches_head_blob': blob == expected, 'frozen': sha(b) == r['sha256']})
check('427 source files bound to Git blobs and frozen HEAD', len(source) == 427 and all(r['matches_head_blob'] and r['frozen'] for r in source) and git('rev-parse', 'HEAD') == HEAD and git('branch', '--show-current') == 'docs/calendar-council-results' and M['source_frozen'] and M['source_before']['runtime_sha256'] == M['source_after']['runtime_sha256'], {'head': HEAD, 'runtime_sha256': M['source_before']['runtime_sha256'], 'source_files': len(source)})

p = Path('/proc/798113')
actual = {'pid': 798113, 'startticks': p.joinpath('stat').read_text().rsplit(')', 1)[1].split()[19], 'sha256': sha(p.joinpath('exe').read_bytes()), 'executable': os.readlink(p / 'exe')}
buildinfo = subprocess.check_output(['go', 'version', '-m', str(p / 'exe')], text=True)
check('retained live PID hash ticks and actual Go metadata', actual['startticks'] == '5579845' and actual['sha256'] == 'cd10fe1adbd1b21c9b89935a63827f7c746b9cf83a75fd05e5556c79e2b8c762' and all(M['binary'][k] == actual[k] for k in actual) and 'vcs.revision=' + HEAD in buildinfo, actual)
check('unchanged fixture seed and fixed displayed reference date', sha((PRIVATE / 'seed.json').read_bytes()) == M['fixture']['seed_sha256'] == BASE['fixture']['seed_sha256'] and M['fixture']['display_date'] == '2026-10-27' and M['fixture']['origin'] == 'http://127.0.0.1:36827', 'No seed/event/account changes or blank form submissions. Real sign-ins necessarily update sessions and last-sign-in timestamps; database bytes are not claimed immutable.')
driver = (D / 'capture-round3-confirmation.mjs').read_text()
check('one capped Chromium with sequential contexts closed finally', M['browser_closed'] and len(M['context_order']) == 12 and "'--renderer-process-limit=2'" in driver and '--complete-missing' not in driver and 'bounded_completion' not in driver and "await everyday.context.close()" in driver and "await administrator.context.close()" in driver and "await login.context.close()" in driver, {'contexts': M['context_order'], 'browser': M['browser'], 'capture_retries': 0, 'prior_owned_capture_browsers': 'none; unrelated desktop and T3 browsers not signalled'})
batch = driver[driver.index('try {\n  for (const scheme'):]
check('all context screenshots precede supplemental Week and nav focus probes', batch.index("'phones-and-apps', size") < batch.index('await weekProbes(') < batch.index('await navProbe(') and batch.index("'admin-group-calendars', size") < batch.index("await navProbe(page,size,scheme,'administrator')") and batch.index("'login', size") < batch.index('beforeTyping=count()'), 'Driver control flow; original required Enter editor interaction preserved; read-only post-shot geometry has no focus/navigation mutation.')
check('no capture errors or correction attempts', M['failures'] == [] and not M['failure_history']['captures_retried'], M['failures'])

rows = []
for r in OLD['files']:
    v = {**r, 'scoring_candidate': False}
    if v.get('artifact_role') in ['current-quality', 'current-supporting']:
        v['artifact_role'] = 'diagnostic-round3-rejected-implementation'
        v['unscored_reason'] = 'Stage03 actual product regressions; parent rejected implementation, USER did not reject layout. Preserved diagnostic history; excluded from current quality.'
    elif v.get('artifact_role') == 'brand-reference':
        v['unscored_reason'] = 'Separate KyIdentity brand reference only; never current quality.'
    else:
        v['unscored_reason'] = 'Historical/diagnostic original; excluded from current quality.'
    rows.append(v)
current = [{**r, 'artifact_role': 'current-quality' if r['scoring_candidate'] else 'current-supporting'} for r in ST['files']]
allrows = sorted(rows + current, key=lambda r: r['file'])
for r in allrows:
    b = (D / r['file']).read_bytes()
    with Image.open(D / r['file']) as im:
        dimensions = im.size
        im.verify()
    assert sha(b) == r['sha256'] and len(b) == r['bytes'] and dimensions == (r['width'], r['height']), r['file']
check('206 original image hashes dimensions and identities', len(allrows) == len({r['file'] for r in allrows}) == len(list(D.rglob('*.png'))) == 206 and all(r.get('observed_product') and r.get('observed_surface') and r.get('artifact_role') for r in allrows), '156 preserved originals + 50 new; every PNG decoded/verified and rehashed.')
counts = {**ST['counts'], 'all_images': 206, 'current_images': 50, 'all_preserved_images': 156, 'brand_reference': 2, 'diagnostic_stage03': 50, 'other_historical_or_diagnostic': 104, 'current_supporting': 2}
artifact = {**ST, 'schema': 'kycalendar-artifacts/4', 'stage': '04-confirmation-current', 'capture_batch_status': 'terminal; capture evidence only; no Council rating or acceptance', 'files': allrows, 'counts': counts, 'same_size_pairs_scope': '601 same-size pairs within the 50 current originals only; all original RGBA pixels, no crops/resizes; one-thread numpy. No all-history exhaustive pair claim.'}
inv = {'schema': 'kycalendar-inventory/4', 'stage': '04-confirmation-current', 'capture_batch_status': artifact['capture_batch_status'], 'protocol': ST['protocol'], 'files': [r for r in current if r['scoring_candidate']], 'unscored': [{'file': r['file'], 'duplicate_of': r.get('duplicate_of'), 'reason': r.get('unscored_reason'), 'artifact_role': r['artifact_role']} for r in allrows if not r['scoring_candidate']], 'counts': counts, 'source_manifests': ['manifest-04-confirmation.json'], 'all_images': 'artifacts-v4.json', 'supersession': '04 supplies all50 current originals /48 unique candidates;03 diagnostic. Phone default-list representatives are first filename, scored once. Brand/history never pooled. Parent owns review-inputs-v3.json for actual Council review round3; v4 is artifact version only.'}
write('artifacts-v4.json', artifact)
write('inventory-v4.json', inv)
check('48 current members 601 pair comparisons duplicate representatives', len(inv['files']) == 48 and len(ST['same_size_pairs']) == 601 and len(ST['duplicate_groups']) == 2 and all(len(g) == 2 and 'default-phone-list' in min(g) and any(r['file'] == min(g) and r['scoring_candidate'] for r in current) for g in ST['duplicate_groups']), {'counts': counts, 'duplicate_groups': ST['duplicate_groups'], 'near_duplicate_nonexact_pairs': sum(p['near_duplicate'] and not p['exact'] for p in ST['same_size_pairs'])})
check('50 individual full-resolution content inspections', len(INSPECT['rows']) == 50 and {r['file'] for r in INSPECT['rows']} == {r['file'] for r in M['files']} and all(r['full_resolution_individual_inspection'] and r['filename_matches_content'] and r['observed_product'] == 'KyCalendar' for r in INSPECT['rows']), 'round3-confirmation-per-file-inspection.json; each original opened individually using view_image original; product/surface determined visually; no scores.')
check('matched viewport locale timezone theme fonts and initial scroll', all((r['width'], r['height']) == ((1280, 900) if r['viewport'] == 'desktop' else (390, 844)) and r['conditions']['viewport']['dpr'] == 1 and r['conditions']['locale'] == 'en-US' and r['conditions']['timezone'] == 'America/New_York' and r['conditions']['saved_theme'] is None and r['conditions']['os_dark'] == (r['theme'] == 'dark') and r['conditions']['scroll'] == {'x': 0, 'y': 0} and not r['conditions']['credential_inputs_filled'] and r['conditions']['font_probes']['fonts_status'] == 'loaded' for r in M['files']), 'All50 originals; neutral bottom-right pointer; fonts.ready,2rAF and400ms settle; real controls and clock; no credential capture.')
check('zero horizontal page overflow across all originals', all(r['conditions']['overflow_px'] == 0 for r in M['files']), 'Week internal horizontal scrolling is intentional and separate from page overflow.')
DIA = [r['diagnostics'] for r in M['files']] + [r['diagnostics'] for r in M['read_only_probes'] if 'diagnostics' in r]
check('no browser HTTP console CSP failed or external requests', all(not d[k] for d in DIA for k in ['page_errors', 'console_errors', 'csp', 'request_failures', 'http_errors', 'external_requests']), 'Real response/request diagnostics retained for captures and end-of-context probes.')
check('production CSP service workers and role-specific navigation', all("script-src 'self'" in r['diagnostics']['document_csp'] and r['conditions']['service_worker_controlled'] and (r['role'] == 'signed-out' or r['conditions']['left_regions']['primary_nav_count'] == 1) for r in M['files']), 'Driver asserts no People navigation for everyday and no Calendar for admin. One primary nav; no worker/auth mocks. Not a fresh complete authorization matrix.')
terminal_assets = read('sourceprovenance-round3-confirmation-terminal-assets.json')
asset_sets = [M['supplemental_assets_before'], M['supplemental_assets_after'], terminal_assets['assets'], M['served_assets']]
check('served assets bound before after and terminal with redirects refused', all(len(x) == 9 and all(a['matches_disk'] for a in x) for x in asset_sets[:3]) and len(asset_sets[3]) == 8 and all(a['matches_disk'] for a in asset_sets[3]) and {a['path'] for a in asset_sets[0]} == {a['path'] for a in asset_sets[1]} == {a['path'] for a in asset_sets[2]}, 'Nine assets including sw.js via validated fixtureOrigin/fetchFixture; eight independently observed browser response bodies; sourceprovenance-round3-confirmation.json.')

K = M['keyboard_probes']
check('Enter Escape logical event replacement focus 4 of 4', len(K) == 4 and len({(p['size'], p['scheme']) for p in K}) == 4 and all(p['list_enter_opens_real_event'] and p['escape_closes'] and p['original_disconnected'] and p['focus_returned_to_trigger'] and p['active']['tag'] == 'A' and p['active']['tabindex'] == '0' and p['active']['id'] == p['logical_event_id'] for p in K), K)
P = [p for p in M['read_only_probes'] if p['probe'].startswith('login challenge')]
check('login challenge stable after empty screenshot typing and clearing', len(P) == 4 and all(p['stable'] and p['beforeTyping'] == p['afterTyping'] == p['afterClear'] == 1 and not p['submitted'] for p in P), [{k: p[k] for k in ['size', 'scheme', 'beforeTyping', 'afterTyping', 'afterClear', 'submitted']} for p in P])
W = [p for p in M['read_only_probes'] if p['probe'].startswith('Week keyboard')]
check('phone Week native keyboard movement restores initial zero offset', len(W) == 2 and all(p['moved'] and p['direction'] == 'ArrowRight' and p['initial']['week']['scrollLeft'] == p['restored']['week']['scrollLeft'] == 0 and p['after']['week']['scrollLeft'] > 0 for p in W), [{'scheme': p['scheme'], 'offsets': [p[k]['week']['scrollLeft'] for k in ['initial', 'after', 'restored']]} for p in W])
G = [p for p in M['read_only_probes'] if p['probe'] == 'post-shot geometry']
weekshots = [p for p in G if p['surface'] == 'week' and p['size'] == 'mobile']
check('phone Week initial gutter actually painted and untransformed', len(weekshots) == 2 and all(any(a['text'] == '7am' and a['painted_hit_within_axis'] and a['box']['transform'] == 'none' for a in p['geometry']['week_axis_paint']) for p in weekshots) and all(any(a['text'] == '7am' and a['painted_hit_within_axis'] for a in p[k]['week_axis_paint']) for p in W for k in ['initial', 'restored']), 'Both original phone Week images independently show7am-2pm labels. elementFromPoint hits actual axis at initial/restored0. Native hscroll can move gutter offscreen after user movement; no fixed-axis or automatic-current-day claim.')
REG = [p for p in M['read_only_probes'] if p['probe'].startswith('Week region attributes')]
DAY = [p for p in M['read_only_probes'] if p['probe'].startswith('Day attributes')]
check('Day clears region attrs and Week reapplies after reused DOM', len(REG) == len(DAY) == 4 and all(p['attributes'] == {'role': 'region', 'label': 'Week calendar', 'tabindex': '0'} for p in REG) and all(p['attributes'] == {'role': None, 'label': None, 'tabindex': None} for p in DAY), {'day': DAY, 'week': REG})
N = [p for p in M['read_only_probes'] if p['probe'].startswith('settled navigation')]
check('all focused navigation rows fully within strip and unmasked', len(N) == 8 and all(i['focused'] and i['fully_visible'] and i['unmasked'] for p in N for i in p['items']), {'contexts': len(N), 'rows': sum(len(p['items']) for p in N), 'settle': '400ms plus350ms after focus', 'observations': N})
GROUP = [p for p in G if p['surface'] == 'admin-group-calendars']
check('Add group disabled zero-group fixture in all four conditions', len(GROUP) == 4 and all(p['geometry']['add_group'] and all(x['disabled'] for x in p['geometry']['add_group']) and any(x['disabled'] and x['value'] == '' and x['text'] == 'No groups available' for x in p['geometry']['group_fields']) for p in GROUP), 'No group created or blank form submitted; availability and disabled state observed only.')
check('phone Group and Role fields both fill card content width', len([p for p in GROUP if p['size'] == 'mobile']) == 2 and all(abs(p['geometry']['group_fields'][1]['box']['width'] - 324) < 1 and abs(p['geometry']['group_fields'][2]['box']['width'] - 324) < 1 for p in GROUP if p['size'] == 'mobile'), [{'size': p['size'], 'scheme': p['scheme'], 'field_widths': [x['box']['width'] for x in p['geometry']['group_fields']]} for p in GROUP])

textrows = []
for p in G:
    if p['surface'] not in ['month', 'week', 'day']:
        continue
    titles = [('month', t, None) for t in p['geometry']['month_titles']] + [('timed', t['title'], t['event']) for t in p['geometry']['timed_titles'] if t['title']]
    for kind, t, event in titles:
        b = t['box']
        lines = sorted({round(r['y'], 2) for r in t['rects'] if b['y'] <= (r['y'] + r['bottom']) / 2 < b['bottom'] and r['height'] > 0})
        textrows.append({'file': p['file'], 'kind': kind, 'text': t['text'], 'title_box': b, 'event_box': event, 'raw_range_rows': t['line_count'], 'visible_text_rows_midpoint_inside_clip': len(lines), 'visible_row_y': lines})
write('round3-confirmation-title-geometry.json', {'rows': textrows, 'method': 'Range line-box midpoint inside clip; one-pixel intersections excluded; corroborated by each original inspection; no OCR claim.'})
month = [r for r in textrows if r['kind'] == 'month' and '-month-mobile-' in r['file']]
check('phone Month two visible title rows and no page overflow', len(month) == 22 and all(r['visible_text_rows_midpoint_inside_clip'] == 2 and r['title_box']['lineClamp'] == '2' for r in month), '22 title boxes /2 phone Month originals; narrow prefixes still ellipsize; complete titles remain DOM text.')
tall = [r for r in textrows if r['kind'] == 'timed' and r['event_box']['height'] >= 60 and r['text'].startswith('Planning')]
check('tall timed titles retain two-line capacity', len(tall) == 8 and all(r['title_box']['lineClamp'] == '2' for r in tall) and all(r['visible_text_rows_midpoint_inside_clip'] == 2 for r in tall if '-week-' in r['file'] or '-mobile-' in r['file']), 'Eight Planning title blocks across4 conditions and Week/Day; desktop Day fits1 line, phone Day shows full2-line title, Week ellipsizes after2.')
headings = [p for p in G if p['surface'] in ['month', 'week', 'day']]
check('calendar workspace heading remains compact', len(headings) == 12 and all(p['geometry']['heading']['height'] <= (64 if p['size'] == 'mobile' else 55) and p['geometry']['heading']['scrollWidth'] <= p['geometry']['heading']['clientWidth'] + 1 for p in headings), [{'surface': p['surface'], 'size': p['size'], 'scheme': p['scheme'], 'heading': p['geometry']['heading']} for p in headings])

provenance = {'schema': 'kycalendar-source-provenance/4', 'checked_at': datetime.now(timezone.utc).isoformat(), 'head': HEAD, 'source_before': M['source_before'], 'source_after': M['source_after'], 'terminal_source_files': source, 'private_process': actual, 'actual_go_version_m': buildinfo, 'parent_process_record': M['binary'], 'metadata_note': 'Actual vcs.modified=true remains intact if present. Exact owner-supplied binary hash/revision plus427 runtime/dist Git-blob and SHA256 bindings independently verified; do not call the binary clean.', 'assets_before': asset_sets[0], 'assets_after': asset_sets[1], 'terminal_assets': terminal_assets, 'browser_assets': asset_sets[3], 'driver_bindings': [{'file': n, 'sha256': sha((D / n).read_bytes())} for n in ['capture.mjs', 'capture-round3.mjs', 'capture-round3-confirmation.mjs', 'capture-inventory.mjs', 'capture-round3-confirmation-checks.py', 'fixture-network.mjs']], 'rebuild_or_restart_by_worker': False, 'fixture_retained': True}
write('sourceprovenance-round3-confirmation.json', provenance)
limitations = ['Complete behavior, accessibility, integration and physical-device gates remain untested. No builds or test suites run: explicit capture-only scope. Parent prior tests are not fresh evidence from this worker.', 'Real Oct28 clock preserved; every image has UTC capture time and browser time. Today28/time indicators and People last-sign-in changes are clock effects, not UI improvement.', 'Native unsaved New event End segment selection is a capture artifact; original interaction and focus preserved.', 'Parent rejected stage03 implementation because actual regressions; USER did not reject layout. Original19pass/4fail reports retained byte-for-byte, including their historical wording. Stage03 excluded from current quality.', 'Earlier empty-Monday/clipped-Tuesday premise was disproved; no repair credit or scores assigned for it. Week starts at week start with native scrolling; no fixed gutter after scroll or automatic current-day claim.', 'Current evidence is50 originals/48 unique candidates in04; stage04 and inventory-v4 are artifact identifiers, not a fourth rubric/reviewer round. Parent owns review-inputs-v3 for actual review round3.', 'MySlop unavailable: user reports known revoked401; no retry or external request. DOX read/rechecked; all AGENTS intentionally unchanged under evidence-only ownership and explicit byte preservation.', 'No rerun, recapture, source edits, fixture reseed/account/event mutation, browser reopening, commits, pushes, subdelegation or new scores. Prior failures retained; any new product failure is reported without correction.']
result = {'schema': 'kycalendar-capture-checks/4', 'checked_at': datetime.now(timezone.utc).isoformat(), 'head': HEAD, 'capture_batch_status': 'terminal; bounded corrected confirmation only; no Council acceptance', 'checks': checks, 'summary': dict(Counter(c['status'] for c in checks)), 'raw_failures': M['failures'], 'prior_failure_history': M['failure_history'], 'behavior_accessibility_integration_physical_gates': 'untested', 'scores': None, 'acceptance': None, 'private_process': actual, 'current_membership': 'inventory-v4.json', 'all_artifacts': 'artifacts-v4.json', 'per_file_inspection': 'round3-confirmation-per-file-inspection.json', 'title_geometry': 'round3-confirmation-title-geometry.json', 'clock_states': [{'file': r['file'], 'captured_at_utc': r['captured_at_utc'], 'real_browser_time': r['conditions']['real_browser_time'], 'today_dates': r['conditions']['round2_geometry']['today_dates'], 'time_indicator': r['conditions']['round2_geometry']['time_indicator']} for r in M['files']], 'limitations': limitations}
write('capture-round3-confirmation-checks.json', result)
md = [f'Corrected Council round3 capture at `{HEAD}`. Single04-confirmation batch:50 new originals,48 unique current candidates;206 total originals. Chromium closed; fixture retained. No scores or progression decision.', '', f"Checks: {result['summary'].get('pass', 0)} pass / {result['summary'].get('fail', 0)} fail.", '', 'Current phone observations: Week actually paints7am-2pm labels at initial offset0; keyboard0→40→0; Day clears region attrs and Week restores them after reuse; focused navigation rows fit and are unmasked; Group/Role selectors324px each; Add group disabled with no groups. Month and tall title geometry retained. Enter/Escape replacement focus4/4; login challenge stable4/4.', '', 'Parent rejected03 implementation due actual regressions; USER did not reject layout. Prior19pass/4fail reports and all previous bytes retained.03 is diagnostic;04 is current. No new review scores. Stage04/inventory-v4 do not imply another rubric round.', '', 'Review: `inventory-v4.json`, `artifacts-v4.json`, `manifest-04-confirmation.json`, `sourceprovenance-round3-confirmation.json`, `round3-confirmation-preservation-proof.json`, `round3-confirmation-per-file-inspection.json`, `round3-confirmation-title-geometry.json`.', '', 'Checks:']
md += [f"- {c['status'].upper()}: {c['name']}" for c in checks]
md += ['', 'Limitations:'] + ['- ' + x for x in limitations]
with (D / 'capture-round3-confirmation-checks.md').open('x') as f:
    f.write('\n'.join(md) + '\n')
print(json.dumps({'summary': result['summary'], 'counts': counts, 'failed_checks': [c['name'] for c in checks if c['status'] == 'fail']}))
