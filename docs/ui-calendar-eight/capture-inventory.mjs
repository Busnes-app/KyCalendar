// Recompute original-image inventory and every same-size pixel pair, without changing PNGs.
// Usage: node capture-inventory.mjs [00-before]
// Reuses installed Pillow/numpy; one sequential process, no installs or suites.
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';
const dir = fileURLToPath(new URL('.', import.meta.url));
const stage = process.argv[2] ?? '00-before';
console.log(execFileSync('python3', ['-c', String.raw`
import sys,json,hashlib,itertools
from pathlib import Path
from PIL import Image
import numpy as np
d=Path(sys.argv[1]); stage=sys.argv[2]
suffix='v1' if stage=='00-before' else stage
m=json.loads((d/f'manifest-{stage}.json').read_text())
known={r['file']:r for r in m['files']}
rows=[]; arrays={}; pairs=[]; duplicate_edges=[]
for path in sorted((d/'screenshots').glob(f'kycalendar-{stage}-*.png')):
 rel=path.relative_to(d).as_posix(); raw=path.read_bytes()
 with Image.open(path) as im:
  w,h=im.size
  arrays[rel]=np.array(im.convert('RGBA'),dtype=np.uint8)
 old=known.get(rel,{})
 r={k:v for k,v in old.items() if k not in ['conditions','diagnostics']}
 r.update(file=rel,sha256=hashlib.sha256(raw).hexdigest(),bytes=len(raw),width=w,height=h,
  duplicate_of=None,scoring_candidate=old.get('membership')=='candidate',
  unscored_reason=None if old.get('membership')=='candidate' else old.get('membership','preserved raw artifact; no matrix membership'))
 if old:
  assert r['sha256']==old['sha256'] and w==old['width'] and h==old['height'],rel
 rows.append(r)
for a,b in itertools.combinations(rows,2):
 if (a['width'],a['height'])!=(b['width'],b['height']): continue
 delta=np.abs(arrays[a['file']].astype(np.int16)-arrays[b['file']])
 changed=int(np.count_nonzero(np.any(delta!=0,axis=2)))
 fraction=changed/(a['width']*a['height']); maximum=int(delta.max())
 exact=a['sha256']==b['sha256']; near=fraction<0.001 and maximum<=8
 p=dict(a=a['file'],b=b['file'],changed_pixels=changed,pixel_count=a['width']*a['height'],fraction_changed=fraction,max_channel_delta=maximum,exact=exact,near_duplicate=near)
 pairs.append(p)
 if exact or near: duplicate_edges.append((a['file'],b['file']))
# ponytail: 50 originals; a tiny union-find keeps transitive duplicate groups explicit.
parent={r['file']:r['file'] for r in rows}
def find(x):
 while parent[x]!=x: x=parent[x]
 return x
for a,b in duplicate_edges:
 x,y=find(a),find(b)
 if x!=y: parent[max(x,y)]=min(x,y)
groups={}
for r in rows: groups.setdefault(find(r['file']),[]).append(r['file'])
for r in rows:
 group=groups[find(r['file'])]
 if len(group)>1 and r['file']!=min(group):
  r['duplicate_of']=min(group); r['scoring_candidate']=False; r['unscored_reason']='duplicate; score first file by name once'
 # An explicit supporting image can be first by filename. Promote its representative
 # only when the duplicate group contains an actual requested matrix candidate.
 if r['file']==min(group) and any(known.get(x,{}).get('membership')=='candidate' for x in group):
  r['scoring_candidate']=True; r['unscored_reason']=None
candidate=[r for r in rows if r['scoring_candidate']]
protocol={'exact':'same SHA-256','near':'same dimensions; fraction of pixels with any nonzero RGBA channel change <0.001 AND max absolute channel delta <=8',
 'representative':'first filename in each connected duplicate group','pixels':'original decoded RGBA, no crops/resizes, sequential Pillow/numpy'}
art={'schema':'kycalendar-artifacts/1','stage':stage,'protocol':protocol,'files':rows,'duplicate_groups':[v for v in groups.values() if len(v)>1],
 'same_size_pairs':pairs,'counts':{'all_images':len(rows),'matrix_images':sum(r.get('membership')=='candidate' for r in rows),'scored_candidates':len(candidate),'pairwise_comparisons':len(pairs)}}
inv={'schema':'kycalendar-inventory/1','stage':stage,'protocol':protocol,'files':candidate,
 'unscored':[{'file':r['file'],'duplicate_of':r['duplicate_of'],'reason':r['unscored_reason']} for r in rows if not r['scoring_candidate']],
 'counts':art['counts'],'source_manifest':f'manifest-{stage}.json','all_images':f'artifacts-{suffix}.json'}
assert len({r['file'] for r in rows})==len(rows)
assert all(r['width'] in [1280,390] and r['height']==(900 if r['width']==1280 else 844) for r in rows)
assert len(candidate)+len(inv['unscored'])==len(rows)
for name,data in [(f'artifacts-{suffix}.json',art),(f'inventory-{suffix}.json',inv)]:
 (d/name).write_text(json.dumps(data,indent=2)+'\n')
print(json.dumps(art['counts']))
`, dir, stage], { encoding: 'utf8', maxBuffer: 1024 * 1024, env: { ...process.env, OPENBLAS_NUM_THREADS: '1', OMP_NUM_THREADS: '1' } }));
