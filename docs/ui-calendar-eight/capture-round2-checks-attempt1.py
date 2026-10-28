"""Round2 capture audit. No browser, builds, suites or product writes.
Run with OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1 python3 this_file.py.
Requires completed 01-after and the single four-Appearance 02-confirmation.
"""
import hashlib,itertools,json,os,subprocess,urllib.request
from collections import Counter
from datetime import datetime,timezone
from pathlib import Path
from PIL import Image
import numpy as np
D=Path(__file__).resolve().parent
ROOT=D.parent.parent
PRIVATE=Path('/tmp/kycalendar-eight-Fqv5Ll')
HEAD='d7e3b89843c5893af0d18cc7e3ac147880cc91c4'
def read(n): return json.loads((D/n).read_text())
def sha(b): return hashlib.sha256(b).hexdigest()
def write(n,v):
 p=D/n
 assert not p.exists(), f'refuse overwriting audit output {n}'
 p.write_text(json.dumps(v,indent=2)+'\n')
before=read('manifest-00-before.json'); after=read('manifest-01-after.json'); confirmation=read('manifest-02-confirmation.json')
assert len(after['files'])==50 and len(confirmation['files'])==4
assert all(r['observed_surface']=='appearance' for r in confirmation['files'])
assert after['browser_closed'] and confirmation['browser_closed']
assert not after['failures'] and not confirmation['failures']
current=sorted([r for r in after['files'] if r['observed_surface']!='appearance']+confirmation['files'],key=lambda r:r['file'])
assert len(current)==50
assert len({(r['observed_surface'],r['viewport'],r['theme']) for r in current if r['membership']=='candidate'})==48
protocol=read('artifacts-01-after.json')['protocol']
arrays={}
def image_row(r):
 p=D/r['file']; b=p.read_bytes()
 with Image.open(p) as im:
  w,h=im.size; arrays[r['file']]=np.array(im.convert('RGBA'),dtype=np.uint8)
 assert sha(b)==r['sha256'] and (w,h)==(r['width'],r['height'])
 return {k:v for k,v in r.items() if k not in ['conditions','diagnostics']}
rows=[image_row(r) for r in current]
def diff(a,b):
 delta=np.abs(arrays[a['file']].astype(np.int16)-arrays[b['file']].astype(np.int16))
 changed=int(np.count_nonzero(np.any(delta!=0,axis=2))); count=a['width']*a['height']; maximum=int(delta.max())
 return {'a':a['file'],'b':b['file'],'changed_pixels':changed,'pixel_count':count,'fraction_changed':changed/count,'max_channel_delta':maximum,'exact':a['sha256']==b['sha256'],'near_duplicate':changed/count<.001 and maximum<=8}
pairs=[diff(a,b) for a,b in itertools.combinations(rows,2) if (a['width'],a['height'])==(b['width'],b['height'])]
# ponytail: at most 50 current originals. Keep the pinned transitive/first-filename rule.
parent={r['file']:r['file'] for r in rows}
def find(x):
 while parent[x]!=x: x=parent[x]
 return x
for p in pairs:
 if p['exact'] or p['near_duplicate']:
  a,b=find(p['a']),find(p['b'])
  if a!=b: parent[max(a,b)]=min(a,b)
groups={}
for r in rows: groups.setdefault(find(r['file']),[]).append(r['file'])
known={r['file']:r for r in current}
for r in rows:
 group=groups[find(r['file'])]; representative=min(group)
 r['duplicate_of']=representative if representative!=r['file'] else None
 r['scoring_candidate']=r['file']==representative and any(known[f]['membership']=='candidate' for f in group)
 r['unscored_reason']=None if r['scoring_candidate'] else 'duplicate; score first file by name once' if r['duplicate_of'] else 'supporting evidence only'
 r['artifact_role']='current-quality' if r['scoring_candidate'] else 'current-supporting'
candidates=[r for r in rows if r['scoring_candidate']]
assert len(candidates)==48 and len(pairs)==601
allrows=[]
for r in before['files']:
 v=image_row(r); v.update(artifact_role='historical-baseline',scoring_candidate=False,unscored_reason='historical round1; excluded from current round2 mean'); allrows.append(v)
for r in after['files']:
 if r['observed_surface']=='appearance':
  v=image_row(r); v.update(artifact_role='superseded-capture-diagnostic',scoring_candidate=False,unscored_reason='Capture-driver nav focus probe left unmatched focus outline; use 02-confirmation Appearance'); allrows.append(v)
allrows+=rows
for p in sorted(D.glob('brand-reference-*.png')):
 raw=p.read_bytes()
 with Image.open(p) as im: w,h=im.size
 allrows.append({'file':p.name,'sha256':sha(raw),'bytes':len(raw),'width':w,'height':h,'observed_product':'KyIdentity','observed_surface':'Appearance','artifact_role':'brand-reference','scoring_candidate':False,'unscored_reason':'Ky brand context only; not quality membership'})
assert len(allrows)==106 and len({r['file'] for r in allrows})==106
counts={'all_images':50,'matrix_images':48,'scored_candidates':48,'pairwise_comparisons':601,'all_preserved_images':106,'historical_baseline':50,'superseded_diagnostic':4,'brand_reference':2,'current_originals':50}
artifact={'schema':'kycalendar-artifacts/2','stage':'round2-current','protocol':protocol,'files':sorted(allrows,key=lambda r:r['file']),'counts':counts,'duplicate_groups':[v for v in groups.values() if len(v)>1],'same_size_pairs_scope':'CURRENT selected 50 originals only, including supporting List originals; historical/diagnostic/brand excluded from duplicate membership','same_size_pairs':pairs}
write('artifacts-v2.json',artifact)
inv={'schema':'kycalendar-inventory/2','stage':'round2-current','protocol':protocol,'files':candidates,'unscored':[{'file':r['file'],'duplicate_of':r.get('duplicate_of'),'reason':r['unscored_reason'],'artifact_role':r['artifact_role']} for r in allrows if not r['scoring_candidate']],'counts':counts,'source_manifests':['manifest-01-after.json','manifest-02-confirmation.json'],'all_images':'artifacts-v2.json','supersession':'Four 01-after Appearance images are diagnostics only; four 02-confirmation Appearance images replace them. All originals retained.'}
write('inventory-v2.json',inv)
basekey={(r['observed_surface'],r['viewport'],r['theme']):r for r in before['files']}
matched=[]
for a in current:
 b=basekey[(a['observed_surface'],a['viewport'],a['theme'])]
 p=diff(b,a); p.update(surface=a['observed_surface'],viewport=a['viewport'],theme=a['theme'],baseline_role='historical-baseline',current_role='current-quality' if a['file'] in {r['file'] for r in candidates} else 'current-supporting',same_dimensions=True,match_conditions=True,clock_caveat='Live clock: first after 23:56–23:57 EDT Oct27, correction after midnight Oct28; display date and draft values remain Oct27. People sign-in times and any today/time lines are clock/state evidence, not improvement.')
 matched.append(p)
write('round2-matched-pair-audit.json',{'schema':'kycalendar-matched-pair-audit/1','pairs':matched,'exact_baseline_matches':[p for p in matched if p['exact']],'current_pair_scope':artifact['same_size_pairs_scope'],'current_same_size_pair_audit':'artifacts-v2.json#same_size_pairs','current_same_size_comparisons':len(pairs),'warning':'Unchanged-image rating movement is reviewer/rubric drift, never product improvement. Match includes original vs corrected capture role.'})
proof=read('round2-preservation-before.json'); protected=[]; other=[]
for f,h in proof.items():
 actual=sha((D/f).read_bytes()); entry={'file':f,'before_sha256':h,'after_sha256':actual,'identical':h==actual}
 # Parent can update review state concurrently; it is not a capture-owned protected original.
 if f=='review-state.json': other.append({**entry,'ownership':'parent-owned live state; not capture-worker output'})
 else: protected.append(entry)
assert all(r['identical'] for r in protected)
write('round2-preservation-proof.json',{'protected':protected,'parent_live_state':other,'protected_files':len(protected),'all_protected_identical':True,'baseline_images':50,'original_drivers':['capture.mjs','capture-fixture.mjs','capture-inventory.mjs','capture-checks.mjs'],'policy':'Original baseline PNGs, original manifest, checks, inventories and drivers must retain their exact original bytes.'})
checks=[]
def check(name,passed,evidence): checks.append({'name':name,'status':'pass' if passed else 'fail','evidence':evidence})
check('complete current matched matrix',len(current)==50 and len(candidates)==48,counts)
check('immutable baseline and original drivers',all(r['identical'] for r in protected),'round2-preservation-proof.json; all 50 baseline images plus original drivers/manifests/checks/inventories unchanged; parent live review-state explicitly separate')
check('source frozen at requested HEAD',all(m['source_frozen'] and m['source_before']['head']==HEAD and m['source_after']['head']==HEAD for m in [after,confirmation]),[m['source_before']['runtime_sha256'] for m in [after,confirmation]])
check('current runtime still matches captured file hashes',all(sha((ROOT/r['file']).read_bytes())==r['sha256'] for r in after['source_before']['files']),'Every recorded runtime source/dist file rehashed; no rebuild')
p=json.loads((PRIVATE/'process.json').read_text()); proc=Path('/proc')/str(p['pid']); actualbuild=subprocess.check_output(['go','version','-m',str(proc/'exe')],text=True)
check('owned live process and actual metadata',os.readlink(proc/'exe')==p['executable'] and proc.joinpath('stat').read_text().rsplit(')',1)[1].split()[19]==p['startticks'] and sha(proc.joinpath('exe').read_bytes())==p['sha256'] and 'vcs.revision='+HEAD in actualbuild and 'vcs.modified=false' in actualbuild,{'pid':p['pid'],'startticks':p['startticks'],'executable':p['executable'],'sha256':p['sha256'],'go_version_m':actualbuild})
check('seed reused unchanged',sha((PRIVATE/'seed.json').read_bytes())==before['fixture']['seed_sha256']==after['fixture']['seed_sha256']==confirmation['fixture']['seed_sha256'],'No init/reset/reseed, event edits or deletion; real sign-ins update login timestamps/session data')
check('one browser at a time and both closed',after['browser_closed'] and confirmation['browser_closed'] and after['finished_at']<confirmation['started_at'],'One initial browser, then ONE bounded correction after it closed; no cosmetic iteration loop')
check('dimensions and capture conditions',all(r['width']==(1280 if r['viewport']=='desktop' else 390) and r['height']==(900 if r['viewport']=='desktop' else 844) and r['conditions']['viewport']['dpr']==1 and r['conditions']['locale']=='en-US' and r['conditions']['timezone']=='America/New_York' and r['conditions']['saved_theme'] is None and r['conditions']['scroll']=={'x':0,'y':0} and r['conditions']['os_dark']==(r['theme']=='dark') for r in current),'Original PNG IHDR + per-image runtime probes; initial viewport, neutral pointer, no resizes/crops')
check('zero horizontal PAGE overflow',all(r['conditions']['overflow_px']==0 for r in current),'All 50 current originals have documentElement.scrollWidth−innerWidth=0; internal Week width is intentional')
check('one left region',all(r['conditions']['left_regions']['app_header_count']==1 and r['conditions']['left_regions']['primary_nav_count']==1 and r['conditions']['left_regions']['calendar_inside_header'] for r in current if r['role']!='signed-out'),'One app header/Primary nav, contained calendar list, phone top navigation; no adjacent content sidebar')
check('no page errors or CSP violations',all(not r['diagnostics']['page_errors'] and not r['diagnostics']['csp'] for r in current),'Original diagnostics retained in both manifests')
expectedpaths={'/api/admin/users','/api/calendars'}
check('no unexpected HTTP/network/console errors',all(not r['diagnostics']['request_failures'] and all(e['status']==403 and e['path'] in expectedpaths for e in r['diagnostics']['http_errors']) and all(e=='Failed to load resource: the server responded with a status of 403 (Forbidden)' for e in r['diagnostics']['console_errors']) for r in current),'Eight deliberate role-denial GETs return 403 and browser emits corresponding console errors. These remain raw; not a claim of zero raw errors. No other HTTP/network/console failures.')
check('security headers and service workers retained',all("script-src 'self'" in r['diagnostics']['document_csp'] and r['conditions']['service_worker_controlled'] for r in current),'Production CSP + real serviceWorkers:allow; no interception/mocks')
check('no filled credential captures',all(not r['conditions']['credential_inputs_filled'] for r in after['files']+confirmation['files']),'No filled sign-in/setup screenshot, traces, storage states or token/challenge bodies exported')
check('font readiness',all(r['conditions']['font_probes']['fonts_status']=='loaded' and r['conditions']['font_probes']['space_grotesk_15'] for r in current),'Real document.fonts.ready plus two animation frames; used stacks and loaded faces retained')
check('original served assets match embedded dist',all(r['matches_disk'] for r in after['served_assets']+confirmation['served_assets']),'Original response SHA-256; first driver includes .ttf; supplemental paths below also bind active worker')
assets=[]
paths={'/sw.js'}
for r in after['files']+confirmation['files']:
 for path in r['diagnostics']['request_counts']:
  if path=='/' or path.endswith(('.js','.css','.ttf','.woff','.woff2','.png','.svg','.webmanifest')): paths.add(path)
for path in sorted(paths):
 with urllib.request.urlopen(after['fixture']['origin']+path,timeout=5) as res: raw=res.read(); status=res.status
 file='web/dist/index.html' if path=='/' else 'web/dist'+path
 assets.append({'path':path,'file':file,'status':status,'bytes':len(raw),'sha256':sha(raw),'disk_sha256':sha((ROOT/file).read_bytes()),'matches':sha(raw)==sha((ROOT/file).read_bytes()),'scope':'post-capture loopback byte binding, no browser reopened'})
check('supplemental fonts/worker/static bindings',all(r['matches'] for r in assets),assets)
keyboard=after['keyboard_probes']
check('Enter/Escape focus returns to replacement anchor 4/4',len(keyboard)==4 and all(r['list_enter_opens_real_event'] and r['escape_closes'] and r['focus_returned_to_trigger'] and r['original_disconnected'] and r['active']['tag']=='A' and r['active']['tabindex']=='0' and r['active']['id']==r['logical_event_id'] for r in keyboard),keyboard)
week=[r for r in after['read_only_probes'] if r['probe']=='phone week keyboard horizontal movement']
check('phone Week horizontal keyboard movement 2/2',len(week)==2 and all(r['pass'] and r['after']['left']==40 and r['after']['width']==358 and r['after']['total']==768 for r in week),week)
weekimages=[r for r in current if r['observed_surface']=='week' and r['viewport']=='mobile']
check('phone Week hint and internal grid',all(r['conditions']['round2_geometry']['week_hint']['visible'] and r['conditions']['round2_geometry']['week_grid']['width']>=768 and r['conditions']['round2_geometry']['week']['width']==358 for r in weekimages),'Visible instruction, 48rem grid inside 358px keyboard region; toolbar/page stay within viewport; full originals inspected')
month=[r for r in current if r['observed_surface']=='month' and r['viewport']=='mobile']
check('phone Month visible title boxes',all(e['title'] and e['title']['width']>0 and e['title']['height']>0 for r in month for e in r['conditions']['round2_geometry']['event_labels']),'Both themes: 13px titles in real positive-width/height boxes, nowrap + ellipsis; times 1×1 visually hidden. Narrow cells show short prefixes, not complete titles.')
access=[r for r in confirmation['supplemental_probes'] if r['probe']=='Month actual accessible tree']
check('Month full accessible time/title content',len(access)==4 and all(r['team_checkin_button_count']==1 and 'button "9a Team check-in"' in r['snapshot'] and 'button "1p Planning the community workshop and preparing the shared materials"' in r['snapshot'] for r in access),'Actual accessibility snapshots in manifest-02-confirmation.json. Original geometry accessible_label:null means no explicit aria-label; names derive from full descendant content. No blanket accessibility gate.')
roles=[r for r in after['read_only_probes'] if r['probe'].endswith('role API boundary')]
check('real role API isolation 8/8',len(roles)==8 and all(r['status']==403 for r in roles),roles)
powprobes=[r for r in after['read_only_probes'] if r['probe']=='login challenge count while typing and clearing']
check('login challenge request counts stable 4/4',len(powprobes)==4 and all(r['stable'] and r['beforeTyping']==r['afterTyping']==r['afterClear']==1 for r in powprobes),powprobes)
nav=[r for r in confirmation['supplemental_probes'] if 'navigation' in r['probe']]
check('navigation controls focused and reachable',len(nav)==8 and all(i['focused'] and i['x']>=0 and i['right']<=i['viewport'] for r in nav for i in r['items']),'Supplemental post-image programmatic focus reachability across everyday/admin navigation in both sizes/themes; real page clicks reached matrix pages. This is not every keyboard-only task path.')
check('phone Calendars disclosure open/Escape/focus',all(r['disclosure']['opened'] and r['disclosure']['closed'] and r['disclosure']['focus_summary'] for r in nav if r['probe']=='post-capture everyday navigation and signout' and r['size']=='mobile'),'Both themes; read-only opening and Escape; no calendar mutation')
signout=[r for r in nav if r['probe']=='post-capture everyday navigation and signout']
check('phone signout right alignment',all(r['signout']['right']==374 and r['signout']['width']==40 for r in signout if r['size']=='mobile'),'Confirmation uses real accessible-name locator; box x334/right374 in 390px viewport. Original after CSS selector assumed aria-label and returned null; kept raw without overriding.')
private_modes=[{'path':str(f.relative_to(PRIVATE)), 'mode':oct(f.stat().st_mode&0o777)} for f in [PRIVATE,*PRIVATE.rglob('*')]]
check('private fixture permissions',all(r['mode'] in ['0o700','0o600'] for r in private_modes),'Private directory and nested paths 0700/0600; credentials/database/keys never exported')
check('original image bytes and pinned duplicate rule',len(pairs)==601 and len(candidates)==48 and len(allrows)==106,counts)
# Every current and superseded after original was opened individually using view_image(detail=original).
def observation(r):
 s=r['observed_surface']; mobile=r['viewport']=='mobile'
 if s=='month': return 'Weekday/date grid and semantic calendar colours remain. '+('13px event prefixes are visible with ellipsis (e.g. Tea…, Des…, Lun…, Pla…); 1px time box is visually hidden, full time/title is in accessible tree. Tiny 7-column cells still truncate heavily.' if mobile else 'Short titles and event times readable; long workshop title ellipsizes in cell; spanning Release preparation remains blue.')
 if s=='week': return 'One-line titles ellipsize instead of breaking mid-line; semantic blue/green/purple event fills remain. '+('Visible scroll instruction; first Sunday–Tuesday columns shown in 358px region, 768px grid. Other days need internal scroll; ArrowRight moved 40px.' if mobile else 'All seven days visible; Design review and short Team check-in readable; long workshop and appointment names ellipsize.')
 if s=='day': return 'Library day, Design review and Lunch break readable; orange selected Day and neutral today column remain. '+('Long workshop name ellipsizes on one line; lower time rows continue below initial viewport.' if mobile else 'Long workshop title fits on one line; time grid opens at 07:00.')
 if 'list' in s: return 'Readable time/title rows and day headers; semantic colour dots retained. '+('Long workshop name wraps across three lines; later days continue below the fold.' if mobile else 'All seeded week rows fit; long workshop title fits on one line.')
 if s=='new-event': return 'Empty title; unsaved Oct27 09:00–10:00; End field focused; labelled inputs, Cancel and Save visible. '+('Single-column dialog within 390px viewport.' if mobile else 'Centered dialog with Start/End in one row.')
 if s=='existing-event': return 'Editable Design review, Meeting room and full handover description; Oct27 10:00–11:00; Title focused; Delete/Cancel/Save visible. Escape/refetch returns focus to replacement anchor.'
 if s=='appearance': return 'Corrected original-sequence capture: no nav focus-ring contamination. Match system selected; labelled preview cards and browser-local explanation readable. '+('Two columns; remaining cards below fold; nav row horizontally scrolled to Appearance.' if mobile else 'Five-column card picker with all rows visible.')
 if s=='phones-and-apps': return 'Calendar app selected, AI agent option visible, empty name, Create and No app passwords yet guidance readable. '+('Setup continues below fold; no generated token or secret.' if mobile else 'Setup instructions visible; synthetic loopback URL only.')
 if s=='admin-overview': return 'Administrator separation text visible; six management destinations. '+('Cards stack; first People and Groups fully visible, Group calendars begins below.' if mobile else 'Six cards in 3×2 layout; no calendar content.')
 if s=='admin-people': return 'Synthetic everyday/admin rows, role labels, active status and management actions readable; live last-sign-in times differ from baseline. '+('Stacked labelled cards; second row continues below fold.' if mobile else 'Two-row table; actions fit without page overflow.')
 if s=='admin-group-calendars': return 'Shared planning remains ungranted. Plain access hint replaces nested empty box; disabled Group says No groups available; Role/ Add group visible. All-granted alternative not exercised because fixture has zero groups.'
 if s=='login': return 'Empty username/password; Browser check passed; Sign in and Match system readable; no filled secrets or SSO button.'
 raise AssertionError(s)
inspected=[]
for r in after['files']+confirmation['files']:
 inspected.append({'file':r['file'],'sha256':r['sha256'],'inspected':'individual full-resolution original via view_image detail=original','observed_surface':r['observed_surface'],'viewport':r['viewport'],'theme':r['theme'],'filename_matches_content':True,'current':r['file'] in {v['file'] for v in current},'observations':('Superseded capture: extra focused Appearance navigation outline caused by driver probe; retained diagnostic. ' if r['observed_surface']=='appearance' and r['stage']=='01-after' else '')+observation(r),'signout':'Phone top-right 40px control, glyph near x350; desktop lower-left account region.' if r['role']!='signed-out' else 'signed out; no Sign out control'})
limitations=[
 'No full suites or frontend/Go builds run by this worker; hosted CI owns required full checks after host hardlocks. Parent-reported unit/browser results are context only, not fresh executions here.',
 'Full keyboard, visible focus, all-theme computed contrast, state and complete responsive gates remain untested. Targeted checks do not constitute screenshot-only accessibility acceptance.',
 'No seed event edited/deleted. Parent already tested deletion fallback on a separate disposable fixture; deletion fallback was not rerun here.',
 'No groups fixture means all-granted selector text/state is not exercised. Disabled No groups available is captured; Group role selector/Add group remain visible. No grants were added.',
 'Original signout geometry selector incorrectly assumed aria-label and returned null. Its raw manifest remains unchanged; supplemental actual-name locator verifies alignment. Original event accessible_label null denotes absent explicit aria-label, not an empty computed name; actual accessibility tree recorded in confirmation.',
 'One permitted correction recaptured four Appearance views after driver focus probe contaminated nav focus modality. Four raw originals, original round2 driver/log and manifest retained and explicitly unscored.',
 'Real clock live. Initial after Oct27 23:56–23:57 EDT, correction Oct28 after midnight. Calendar display date fixed Oct27; today may roll to Oct28 in supplemental probes. Sign-in times, Today enabled state and time indicator are clock/state changes, not UI improvement.',
 '403 responses and associated console messages are expected role-denial probes and preserved raw. Zero raw-console-error claim is not made.',
 'DOM rectangle visibility helper may count descendants of closed phone disclosure; containment and native disclosure probes establish one navigation region, not that raw descendant visibility count.',
 'One local standalone origin, production CSP and workers, synthetic fixture. SSO/external feeds/mail/recovery and production integrations not exercised.',
 'MySlop handoff could not be read/posted: documented curl credential returned unknown or revoked token. A working machine credential is needed for parent to post.',
 'DOX pass completed for owned capture paths; AGENTS unchanged by explicit instruction and because existing capture ownership/contracts remain valid. Parent owns review inputs, authority, reviews and progression decision.'
]
behavior={k:{'status':'untested','reason':v} for k,v in {
 'keyboard':'Targeted Enter/Escape, replacement event anchor 4/4, Week ArrowRight 2/2, disclosure Escape and nav focus reachability pass; not every task keyboard-only.',
 'focus':'Event anchor return passes; all controls/themes visible-focus measurement not done.',
 'contrast':'No complete computed text/border/focus contrast measurement across all shipped themes.',
 'states':'Populated/sparse/empty matrix captured; full loading/error state matrix not exercised.',
 'responsive':'Zero page overflow and targeted nav/Week geometry pass; full target usability at both sizes not exhaustively exercised.'}.items()}
result={'schema':'kycalendar-round2-capture-checks/2','checked_at':datetime.now(timezone.utc).isoformat(),'head':HEAD,'checks':checks,'summary':{**counts,'checks_passed':sum(c['status']=='pass' for c in checks),'checks_failed':sum(c['status']=='fail' for c in checks),'baseline_exact_matches':sum(p['exact'] for p in matched),'all_after_originals_individually_inspected':54,'corrections_used':1,'runtime_edits':False,'commits_or_pushes':False,'browser_closed':True},'behavior_gates':behavior,'per_file_inspection':inspected,'raw_failures':{'after':after['failures'],'confirmation':confirmation['failures']},'raw_diagnostics_by_file':[{'file':r['file'],'diagnostics':r['diagnostics']} for r in after['files']+confirmation['files']],'keyboard_probes':keyboard,'read_only_probes':after['read_only_probes'],'supplemental_probes':confirmation['supplemental_probes'],'supplemental_assets':assets,'unchanged_baseline_matches':[p for p in matched if p['exact']],'current_membership':[r['file'] for r in candidates],'unscored':inv['unscored'],'limitations':limitations,'private_process':{k:p[k] for k in ['pid','startticks','executable','sha256']},'driver_bindings':[{'file':f,'sha256':sha((D/f).read_bytes())} for f in ['capture.mjs','capture-fixture.mjs','capture-checks.mjs','capture-inventory.mjs','capture-round2.mjs','capture-round2-confirmation.mjs',Path(__file__).name]]}
write('capture-round2-checks.json',result)
md='# Matched KyCalendar round2 capture checks\n\n'
md+=f"48 current unique quality candidates from 50 selected originals. All 54 after/correction originals individually inspected at full resolution; 50 baseline + 54 new + 2 brand originals retained (106 total). One four-Appearance correction; no runtime edits/builds, commits or pushes.\n\n{result['summary']['checks_passed']} targeted checks pass; {result['summary']['checks_failed']} fail. Complete behavior gates remain untested. Focus return passes 4/4; phone Week ArrowRight 2/2; login challenge count stays 1 in all four typing probes.\n\n"
md+=f"Source `{HEAD}`; private process PID {p['pid']}, startticks {p['startticks']}, executable `{p['executable']}`, SHA-256 `{p['sha256']}`. Actual running executable metadata: vcs.modified=false, matching revision; Go1.27.3. Fixture `/tmp/kycalendar-eight-Fqv5Ll` retained at `http://127.0.0.1:36827`, never reseeded. Stop only with owned private stop.sh, after verifying record and port ownership.\n\n"
md+='## Current membership and correction\n\nUse inventory-v2.json files for quality. 01-after Appearance four files are diagnostic only; 02-confirmation Appearance replaces them. Two near-duplicate explicit phone List images remain unscored; first-filename default-phone-list representatives enter quality. Historical baseline and KyIdentity brand references never enter the current mean. Stage-specific inventory-01-after/artifacts-01-after outputs are preserved and describe that original stage, not canonical current membership.\n\n'
md+=f"All 601 same-sized CURRENT selected-original pairs are in artifacts-v2.json. Matched 50 before/current pixel/hash pairs are in round2-matched-pair-audit.json. {sum(p['exact'] for p in matched)} current images exactly match baseline by SHA-256; rating changes on these are reviewer/rubric drift, not product improvement.\n\n"
md+='## Targeted checks\n\n'+'\n'.join(f"- **{c['status'].upper()} — {c['name']}**" for c in checks)+'\n\n'
md+='## Behavior gate limits\n\n'+'\n'.join(f"- **{k}: {v['status']}** — {v['reason']}" for k,v in behavior.items())+'\n\n'
md+='## Per-image full-resolution inspection\n\n'+'\n'.join(f"- `{r['file']}` — {'CURRENT' if r['current'] else 'DIAGNOSTIC'}: {r['observations']}" for r in inspected)+'\n\n'
md+='## Preserved raw diagnostics\n\nNo page errors, CSP violations or network failures. Deliberate role-denial GETs generated eight 403 responses and matching console messages, retained in raw cumulative file diagnostics. Original signout-selector null and absent explicit event aria-label remain raw; bounded confirmation records real accessible-name signout geometry and Month accessibility tree. No baseline-specific failure/prose reused as fresh acceptance.\n\n'
md+='## Limitations and handoff\n\n'+'\n'.join('- '+s for s in limitations)+'\n'
(D/'capture-round2-checks.md').write_text(md)
print(json.dumps(result['summary']))
assert all(c['status']=='pass' for c in checks), 'targeted audit failures: inspect raw results'
