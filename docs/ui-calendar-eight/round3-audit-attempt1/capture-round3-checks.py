"""Round3 evidence audit only; no browsers, builds, suites or runtime writes.
Run: OPENBLAS_NUM_THREADS=1 OMP_NUM_THREADS=1 python3 docs/ui-calendar-eight/capture-round3-checks.py
Requires capture-inventory.mjs 03-after. Refuses overwriting published output.
"""
import hashlib,json,os,subprocess,urllib.request
from pathlib import Path
from datetime import datetime,timezone
from collections import Counter
from PIL import Image
import numpy as np
D=Path(__file__).resolve().parent;ROOT=D.parent.parent;PRIVATE=Path('/tmp/kycalendar-eight-Fqv5Ll')
HEAD='27690a367bf1808224aba7ef4b39ab0c8937bcf7'
def sha(b):return hashlib.sha256(b).hexdigest()
def read(n):return json.loads((D/n).read_text())
def write(n,v):
 with (D/n).open('x') as f:json.dump(v,f,indent=2);f.write('\n')
def git(*a):return subprocess.check_output(['git',*a],cwd=ROOT,text=True).strip()
M=read('manifest-03-after.json');A1=read('manifest-03-after-attempt1.json');ST=read('artifacts-03-after.json');OLD=read('artifacts-v2.json');BASE=read('manifest-00-before.json')
checks=[]
def check(name,passed,evidence):checks.append({'name':name,'status':'pass' if passed else 'fail','evidence':evidence})
protected=[]
for r in read('round3-preservation-before.json')['files']:
 actual=sha((D/r['file']).read_bytes());protected.append({**r,'after_sha256':actual,'identical':actual==r['sha256']})
write('round3-preservation-proof.json',{'protected':protected,'protected_files':len(protected),'protected_original_images':106,'all_identical':all(r['identical'] for r in protected)})
check('all protected bytes preserved',all(r['identical'] for r in protected),'round3-preservation-proof.json: all prior images, manifests, inventories, audits, raw reviews and existing drivers')
check('50 originals and exact original role/surface/order',len(M['files'])==50 and [(r['observed_surface'],r['viewport'],r['theme'],r['role']) for r in M['files']]==[(r['observed_surface'],r['viewport'],r['theme'],r['role']) for r in BASE['files']],{'files':len(M['files']),'matrix':sum(r['membership']=='candidate' for r in M['files'])})
check('source HEAD and runtime frozen',git('rev-parse','HEAD')==HEAD and M['source_before']['head']==M['source_after']['head']==HEAD and M['source_frozen'] and all(sha((ROOT/r['file']).read_bytes())==r['sha256'] for r in M['source_before']['files']) and git('diff','HEAD','--','cmd','internal','third_party','web/src','web/public','web/dist','go.mod','go.sum')=='',{'head':HEAD,'runtime_sha256':M['source_before']['runtime_sha256']})
p=read('manifest-03-after.json')['binary'];proc=Path('/proc')/str(p['pid']);actual={'pid':p['pid'],'startticks':proc.joinpath('stat').read_text().rsplit(')',1)[1].split()[19],'executable':os.readlink(proc/'exe'),'sha256':sha(proc.joinpath('exe').read_bytes())}
buildinfo=subprocess.check_output(['go','version','-m',str(proc/'exe')],text=True)
check('owned live fixture identity and Go metadata',actual['pid']==629822 and actual['startticks']=='2918321' and actual['sha256']=='9d15c2a64705c2de4029b2a11e34f5cee65e57e56f72289d895581928de85477' and all(actual[k]==p[k] for k in actual) and 'vcs.revision='+HEAD in buildinfo and 'vcs.modified=false' in buildinfo,actual)
check('unchanged seed and display contract',sha((PRIVATE/'seed.json').read_bytes())==BASE['fixture']['seed_sha256']==M['fixture']['seed_sha256'] and M['fixture']['display_date']=='2026-10-27' and M['fixture']['origin']=='http://127.0.0.1:36827','No seed/calendar/account writes; sign-ins update sessions and last-sign-in timestamps.')
check('one bounded completion; no originals replaced',len(A1['files'])==21 and M['bounded_completion']['preserved_originals']==21 and all(next(x for x in M['files'] if x['file']==r['file'])['sha256']==r['sha256'] for r in A1['files']) and A1['browser_closed'] and M['browser_closed'] and A1['finished_at']<M['bounded_completion']['started_at'],{'attempt1':21,'new_files_in_completion':29,'raw_failures_retained':M['failures'],'browser':'One Chromium at a time, renderer limit2, sequential contexts; both passes closed; no further capture passes.'})
rows=ST['files'];oldrows=[]
for r in OLD['files']:
 v={**r,'scoring_candidate':False,'unscored_reason':'Historical round1/round2 evidence; excluded from round3 current quality.'}
 if v.get('artifact_role') in ['current-quality','current-supporting']:v['artifact_role']='historical-round2'
 if v.get('artifact_role')=='brand-reference':v['unscored_reason']='KyIdentity Appearance brand reference only; excluded from quality.'
 oldrows.append(v)
for r in rows:r['artifact_role']='current-quality' if r['scoring_candidate'] else 'current-supporting'
allrows=sorted(oldrows+rows,key=lambda r:r['file'])
for r in allrows:
 b=(D/r['file']).read_bytes()
 with Image.open(D/r['file']) as im:w,h=im.size
 assert sha(b)==r['sha256'] and len(b)==r['bytes'] and (w,h)==(r['width'],r['height']),r['file']
check('156 unique originals with complete identities',len(allrows)==156 and len({r['file'] for r in allrows})==156 and len(list(D.rglob('*.png')))==156 and all(r.get('observed_product') and r.get('observed_surface') and r.get('artifact_role') for r in allrows),'106 preserved +50 round3; two brand refs stay separately unscored; every PNG rehashed and decoded.')
counts={**ST['counts'],'all_images':156,'current_images':50,'historical_images':104,'brand_reference':2,'current_supporting':sum(not r['scoring_candidate'] for r in rows),'all_preserved_images':106}
artifact={**ST,'schema':'kycalendar-artifacts/3','stage':'round3-current','files':allrows,'counts':counts,'same_size_pairs_scope':'Current 50 original captures only; 601 same-sized pairs. Historical/brand pairs not exhaustively compared.'}
inv={'schema':'kycalendar-inventory/3','stage':'round3-current','protocol':ST['protocol'],'files':[r for r in rows if r['scoring_candidate']],'unscored':[{'file':r['file'],'duplicate_of':r.get('duplicate_of'),'reason':r['unscored_reason'],'artifact_role':r['artifact_role']} for r in allrows if not r['scoring_candidate']],'counts':counts,'source_manifests':['manifest-03-after.json'],'all_images':'artifacts-v3.json','supersession':'All50 round3 originals current. First filename in exact/near duplicate group scores once; default-phone-list representatives promoted; history and brand excluded.'}
write('artifacts-v3.json',artifact);write('inventory-v3.json',inv)
check('current membership and pinned duplicate protocol',len(inv['files'])==48 and len(ST['same_size_pairs'])==601 and len(ST['duplicate_groups'])==2 and all(any('default-phone-list' in f for f in g) and len(g)==2 for g in ST['duplicate_groups']),{'counts':counts,'duplicate_groups':ST['duplicate_groups']})
for r in M['files']:
 c=r['conditions'];assert (r['width'],r['height'])==((1280,900) if r['viewport']=='desktop' else (390,844))
check('matched capture conditions',all(r['conditions']['viewport']['dpr']==1 and r['conditions']['locale']=='en-US' and r['conditions']['timezone']=='America/New_York' and r['conditions']['saved_theme'] is None and r['conditions']['os_dark']==(r['theme']=='dark') and r['conditions']['scroll']=={'x':0,'y':0} and r['conditions']['overflow_px']==0 and not r['conditions']['credential_inputs_filled'] and r['conditions']['font_probes']['fonts_status']=='loaded' for r in M['files']),'DPR1;1280x900/390x844; OS light/dark;no saved theme;en-US;America/New_York;initial viewport;zero horizontal PAGE overflow;real fonts;uncropped originals.')
DIA=[r['diagnostics'] for r in M['files']]+[r['diagnostics'] for r in M['read_only_probes'] if 'diagnostics' in r]
check('browser HTTP CSP console and network diagnostics',all(not d[k] for d in DIA for k in ['page_errors','console_errors','csp','request_failures','http_errors','external_requests']),'All raw diagnostics preserved. No role-denial requests added in round3; no external requests, CSP relaxation, routes or auth mocks.')
check('production CSP and real service workers',all("script-src 'self'" in r['diagnostics']['document_csp'] and r['conditions']['service_worker_controlled'] for r in M['files']),'ServiceWorkers allow; no worker or CSP substitution.')
class NoRedirect(urllib.request.HTTPRedirectHandler):
 def redirect_request(self,*a,**kw):raise RuntimeError('redirect refused')
opener=urllib.request.build_opener(NoRedirect());assets=[]
paths={a['path'] for a in M['served_assets']}|{a['path'] for a in A1['served_assets']}|{'/sw.js'}
for path in sorted(paths):
 assert path.startswith('/') and not path.startswith('//') and '?' not in path and '#' not in path and '..' not in path
 with opener.open('http://127.0.0.1:36827'+path,timeout=10) as response:
  assert response.status==200;b=response.read()
 file='web/dist/index.html' if path=='/' else 'web/dist'+path;disk=sha((ROOT/file).read_bytes());assets.append({'path':path,'file':file,'sha256':sha(b),'disk_sha256':disk,'matches':disk==sha(b),'bytes':len(b)})
check('original and supplemental served-asset bindings',all(a['matches_disk'] for a in M['served_assets']+A1['served_assets']) and all(a['matches'] for a in assets),'sourceprovenance-round3.json;actual body hashes vs frozen disk;local GET only;redirects refused;includes worker/fonts.')
write('sourceprovenance-round3.json',{'source':M['source_before'],'source_after':M['source_after'],'private_process':actual,'actual_go_version_m':buildinfo,'parent_record':p,'assets':assets,'driver_bindings':[{'file':n,'sha256':sha((D/n).read_bytes())} for n in ['capture.mjs','capture-round3-attempt1.mjs','capture-round3.mjs','capture-inventory.mjs','capture-round3-checks.py']],'rebuild_by_capture_worker':False,'fixture_retained':True,'private_directory':str(PRIVATE)})
K=M['keyboard_probes'];keys={(x['size'],x['scheme']) for x in K}
check('Enter Escape replacement event focus in four conditions',len(keys)==4 and all(x['list_enter_opens_real_event'] and x['escape_closes'] and x['original_disconnected'] and x['focus_returned_to_trigger'] and x['active']['tag']=='A' and x['active']['tabindex']=='0' and x['active']['id']==x['logical_event_id'] for x in K),{'conditions':sorted(keys),'observations':len(K),'note':'Mobile light repeated only during bounded missing-file completion interaction; no duplicate screenshot.'})
P=[p for p in M['read_only_probes'] if p['probe'].startswith('login challenge')]
check('stable challenge typing clearing four conditions',len(P)==4 and all(p['stable'] and p['beforeTyping']==p['afterTyping']==p['afterClear']==1 and not p['submitted'] for p in P),P)
W=[p for p in M['read_only_probes'] if p['probe'].startswith('Week keyboard')]
check('phone Week keyboard movement and original offset restoration',len(W)==2 and all(p['moved'] and p['initial']['week']['scrollLeft']==p['restored']['week']['scrollLeft'] and p['direction']==('ArrowLeft' if p['initial']['week']['scrollLeft']>0 else 'ArrowRight') for p in W),[{k:p[k] for k in ['size','scheme','direction','moved']}|{'offsets':[p[k]['week']['scrollLeft'] for k in ['initial','after','restored']]} for p in W])
G=[p for p in M['read_only_probes'] if p['probe']=='post-shot geometry'];weekshots=[p for p in G if p['surface']=='week' and p['size']=='mobile']
def fully(b,w):return b and b['x']>=w['x']-1 and b['right']<=w['right']+1
check('initial phone Week today header and column within internal viewport',len(weekshots)==2 and all(fully(p['geometry']['week']['today_header'],p['geometry']['week']['box']) and fully(p['geometry']['week']['today_column'],p['geometry']['week']['box']) for p in weekshots),[{'file':p['file'],'scrollLeft':p['geometry']['week']['scrollLeft'],'today_header':p['geometry']['week']['today_header']} for p in weekshots])
check('phone Week time gutter actually painted before and after movement',all(any(a['painted_hit_within_axis'] for a in p[k].get('week_axis_paint',[]) if a['text']=='7am') for p in W for k in ['initial','after','restored']),'FAIL: bounding boxes stay x17..68, but originals show no labels; hit test at7am returns fc-timegrid-now-indicator-container. Counter-scrolled table cell clipping/stacking suspected. Bounding box visibility alone is insufficient.')
REG=[p for p in M['read_only_probes'] if p['probe'].startswith('Week region attributes') and p['size']=='mobile']
check('phone Week accessible region and tabindex after Day to Week/remount',len(REG)==2 and all(p['attributes']=={'role':'region','label':'Week calendar','tabindex':'0'} for p in REG),REG)
N=[p for p in M['read_only_probes'] if p['probe'].startswith('settled navigation')]
check('settled focus probes show full phone navigation controls',all(i['fully_visible'] for p in N for i in p['items']),[{'size':p['size'],'scheme':p['scheme'],'role':p['role'],'items':[i for i in p['items'] if not i['fully_visible']]} for p in N if any(not i['fully_visible'] for i in p['items'])])
GROUP=[p for p in G if p['surface']=='admin-group-calendars']
check('Add group disabled with no selection in zero-group fixture',len(GROUP)==4 and all(p['geometry']['add_group'] and all(x['disabled'] for x in p['geometry']['add_group']) and any(x['disabled'] and x['value']=='' and x['text']=='No groups available' for x in p['geometry']['group_fields']) for p in GROUP),'4/4 real read-only DOM probes; no blank submission; no group/account fixture mutation.')
check('phone grant fields use full card width',all(p['geometry']['group_fields'][1]['box']['width']>=323 and p['geometry']['group_fields'][2]['box']['width']>=323 for p in GROUP if p['size']=='mobile'),'FAIL both themes: Group288px, Role160px, available content324px. ui.css phone flex-basis rule is weaker than later .inline-form.card-footer field/field-narrow flex shorthands.')
# Derive meaningful rows from recorded Range rectangles. Raw loose intersection count includes
# one-pixel line-box overlap on the clipped third line; do not call that a third visible text line.
textrows=[]
for p in G:
 if p['surface'] not in ['month','week','day']:continue
 titles=[('month',t,None) for t in p['geometry']['month_titles']]+[('timed',t['title'],t['event']) for t in p['geometry']['timed_titles'] if t['title']]
 for kind,t,event in titles:
  b=t['box'];lines=sorted({round(r['y'],2) for r in t['rects'] if b['y']<=(r['y']+r['bottom'])/2<b['bottom'] and r['height']>0});textrows.append({'file':p['file'],'kind':kind,'text':t['text'],'title_box':b,'event_box':event,'raw_range_rows':t['line_count'],'raw_intersection_rows':t['visible_line_count'],'visible_text_rows_midpoint_inside_clip':len(lines),'visible_row_y':lines,'method':'Range line-box midpoints inside title clip, corroborated by individual full-resolution original. Loose one-pixel intersections do not count as visible text rows; no glyph OCR claim.'})
write('round3-title-geometry.json',{'rows':textrows,'caveat':'Raw manifest visible_line_count uses loose rectangle intersection; may count a1px clipped third row. Use midpoint-derived rows and full-resolution visual inspection.'})
month=[r for r in textrows if r['kind']=='month' and '-mobile-' in r['file']]
check('phone Month two visible title rows without page overflow',len(month)==22 and all(r['visible_text_rows_midpoint_inside_clip']==2 and r['title_box']['lineClamp']=='2' for r in month),'22 title boxes across2 originals; many titles remain ellipsized, full text retained in DOM. Actual visible prefixes documented by inspection; complete accessibility gate untested.')
tall=[r for r in textrows if r['kind']=='timed' and r['event_box']['height']>=60 and r['text'].startswith('Planning')]
check('tall timed titles permit two lines',len(tall)==8 and all(r['title_box']['lineClamp']=='2' for r in tall) and all(r['visible_text_rows_midpoint_inside_clip']==2 for r in tall if '-week-' in r['file'] or '-mobile-' in r['file']),'8 long-title blocks across Week/Day and4 conditions; desktop Day needs only1 line; phone Day shows full title in2; Week remains ellipsized after2.')
# Same-size baseline/prior-round matched pair differences; no all-history exhaustive comparison.
previous={ (r['observed_surface'],r['viewport'],r['theme']):r for r in OLD['files'] if r['artifact_role'] in ['current-quality','current-supporting']}
baseline={(r['observed_surface'],r['viewport'],r['theme']):r for r in BASE['files']};matched=[]
for current in M['files']:
 with Image.open(D/current['file']) as im:a=np.array(im.convert('RGBA'),dtype=np.uint8)
 for label,prior in [('baseline',baseline),('round2',previous)]:
  old=prior[(current['observed_surface'],current['viewport'],current['theme'])]
  with Image.open(D/old['file']) as im:b=np.array(im.convert('RGBA'),dtype=np.uint8)
  assert a.shape==b.shape
  delta=np.abs(a.astype(np.int16)-b.astype(np.int16));changed=int(np.count_nonzero(np.any(delta,axis=2)))
  matched.append({'prior_round':label,'a':old['file'],'b':current['file'],'changed_pixels':changed,'fraction_changed':changed/(current['width']*current['height']),'max_channel_delta':int(delta.max()),'exact':old['sha256']==current['sha256'],'same_dimensions':True,'clock_caveat':'Real Oct28;today/current-time/last-sign-in differences are clock state, not product improvement. Displaydate and draft remain2026-10-27.'})
write('round3-matched-pair-audit.json',{'pairs':matched,'current_same_size_pairs':601,'scope':'100 same-size matched baseline/R2 comparisons plus601 current-set pairs; not all historical pairs. No scores or artifact-driven rerating.'})
limitations=['Complete behavior/accessibility/integration and physical-device gates remain untested. No full tests/builds rerun: user capture-only scope; previous parent test results are not this worker fresh evidence.','Real Oct28 clock retained. Today moves toWednesday28; time indicator and People last-sign-in changes are clock state. Captures span initial attempt and bounded missing-file completion; use per-image timestamps.','Week opens today/current anchor by selected refinement. Previous Monday-empty/Tuesday-clipped premise was disproved; Tuesday already fit. Raw R1/R2 opinions remain unchanged; no false repair or rerating claimed.','Native date End segment selection remains original driver artifact; no focus cleared or product focus changed.','No extra capture attempt after the one bounded completion. Role-locator timeout is retained and maps to a possible product Day/Week DOM lifecycle defect, not merely driver error.','MySlop unavailable: user reports known revoked401; no retry or external requests.','DOX pass: root/web/browser/evidence AGENTS intentionally unchanged; this task adds evidence under existing ownership/contracts and is expressly scoped away from runtime/instruction edits.']
result={'schema':'kycalendar-capture-checks/3','checked_at':datetime.now(timezone.utc).isoformat(),'head':HEAD,'checks':checks,'summary':dict(Counter(c['status'] for c in checks)),'raw_failures':M['failures'],'inspection_tool_failure':{'raw_error':'unable to locate image .../kycalendar-03-after-login-desktop-light.png: No such file or directory (os error2)','reason':'Inspection attempted before screenshot had been written; original successfully inspected later; no capture replacement.'},'behavior_gates':'untested; targeted observations only; failed product checks do not invalidate the truthful retained originals','private_process':actual,'current_membership':'inventory-v3.json','all_artifacts':'artifacts-v3.json','per_file_inspection':'round3-per-file-inspection.json','title_geometry':'round3-title-geometry.json','supplemental_assets':assets,'clock_states':[{'file':r['file'],'real_browser_time':r['conditions']['real_browser_time'],'today_dates':r['conditions']['round2_geometry']['today_dates']} for r in M['files']],'limitations':limitations}
write('capture-round3-checks.json',result)
md=['Round3 matched capture evidence at `'+HEAD+'`. All50 originals retained; Chromium closed; fixture retained. No scores.','',f"Checks: {result['summary']}. Failures are explicit product observations, not a visual gate pass.",'','Product findings:','- Phone Week time labels fail actual painting despite in-viewport cell boxes; both original Week images retain the defect. CSS counter-scroll cell transforms plus clipping/stacking need parent diagnosis.','- Phone Day→Week after returning from Phones & apps has no role/aria-label/tabindex. Parent hypothesis: FullCalendar reuses timegrid DOM, so viewDidMount does not rerun; datesSet may need to reapply attributes. No runtime edit here.','- Phone Group and Role grant fields are288px and160px instead of324px; later footer flex shorthand likely wins. Add group disabled works4/4.','- Settled phone nav focus clips Phones & apps, and admin Sign-in/Backups/Two-step verification in both schemes; complete navigation visibility gate remains open.','','Outputs: `inventory-v3.json` (48 unique candidates), `artifacts-v3.json` (156 originals), `manifest-03-after.json`, `sourceprovenance-round3.json`, `round3-preservation-proof.json`, `round3-title-geometry.json`, `round3-matched-pair-audit.json`, `round3-per-file-inspection.json`.','','Capture correction: attempt1 stopped after21 originals on missing Week region role. ONE bounded completion wrote29 missing originals; first21 hashes unchanged. Raw driver/manifest/log and failure retained; no cosmetic recapture.','','Checks:']
md += [f"- {c['status'].upper()}: {c['name']}" for c in checks]
md += ['','Limitations:']+['- '+x for x in limitations]
with (D/'capture-round3-checks.md').open('x') as f:f.write('\n'.join(md)+'\n')
print(json.dumps({'summary':result['summary'],'counts':counts,'failed_checks':[c['name'] for c in checks if c['status']=='fail']}))
