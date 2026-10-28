"""Build a self-contained before/after gallery from verified Council originals."""
import argparse
import base64
import hashlib
import json
import struct
from decimal import Decimal, ROUND_HALF_UP
from fractions import Fraction
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--round', type=int, required=True)
parser.add_argument('--scored-through', type=int, required=True, help='Only terminal rounds may be read.')
parser.add_argument('--output', type=Path, required=True)
args = parser.parse_args()
assert 1 <= args.scored_through <= args.round
root = Path(__file__).resolve().parent
load = lambda name: json.loads((root / name).read_text())
sha = lambda data: hashlib.sha256(data).hexdigest()
rounded = lambda value: str((Decimal(value.numerator) / value.denominator).quantize(Decimal('.1'), rounding=ROUND_HALF_UP))


def key(row):
    surface = row['observed_surface'].replace('default-phone-list', 'list')
    return surface, row['viewport'], row['theme']


def original(row):
    data = (root / row['file']).read_bytes()
    assert sha(data) == row['sha256'] and len(data) == row['bytes']
    assert struct.unpack('>II', data[16:24]) == (row['width'], row['height'])
    return dict(file=row['file'], hash=row['sha256'], width=row['width'], height=row['height'],
                image='data:image/png;base64,' + base64.b64encode(data).decode())


baseline = load('review-inputs-v1.json')
current = load(f'review-inputs-v{args.round}.json')
before = {key(row): row for row in baseline['quality_candidates']}
pairs = []
for row in sorted(current['quality_candidates'], key=key):
    surface, viewport, theme = key(row)
    prior = before[key(row)]
    pairs.append(dict(label=f'{surface.replace("-", " ").title()} · {viewport.title()} · {theme.title()}',
                      before=original(prior), after=original(row), unchanged=row['sha256'] == prior['sha256']))
assert len(pairs) == len(before) == 48
scores = []
for turn in range(1, args.scored_through + 1):
    values = []
    for who in ['opus', 'sol']:
        report = load(f'{who}-round{turn}.json')
        frozen = json.dumps(report['files'], ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
        assert sha(frozen) == report['freeze']['sha256'] and len(frozen) == report['freeze']['bytes']
        mean = sum(Fraction(str(row['total'])) for row in report['files']) / len(report['files'])
        values.append(f'{rounded(mean)} ({mean})')
    scores.append([turn, *values])
for turn in range(args.scored_through + 1, args.round + 1):
    scores.append([turn, 'Pending', 'Pending'])
payload = json.dumps(dict(pairs=pairs, scores=scores, round=args.round,
                         source=current['captured_source']), ensure_ascii=False).replace('<', '\\u003c')
html = '''<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>KyCalendar changes</title>
<style>
:root{color-scheme:light dark;--bg:#f8f6ef;--ink:#202a2d;--muted:#475557;--panel:#efede5;--line:#687376}
@media(prefers-color-scheme:dark){:root{--bg:#202323;--ink:#f5f1e7;--muted:#c4c9c7;--panel:#2b3030;--line:#b0b8b5}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--ink);font:1rem/1.5 system-ui,sans-serif}
main{max-width:100rem;margin:auto;padding:clamp(1rem,3vw,2.5rem)}h1{font-size:clamp(1.5rem,3vw,2.25rem);margin:0 0 .5rem}
p{max-width:80ch;margin:.5rem 0 1rem}.muted,figcaption{color:var(--muted)}
table{border-collapse:collapse;margin:1rem 0;width:100%;max-width:42rem;table-layout:fixed}th,td{padding:.5rem .8rem;border-bottom:1px solid var(--line);text-align:left;overflow-wrap:anywhere}
.controls{display:flex;align-items:end;flex-wrap:wrap;gap:.75rem;margin:1.5rem 0}label{display:grid;gap:.35rem;flex:1 1 22rem;min-width:0}
select,button{font:inherit;min-height:2.75rem;background:var(--panel);color:var(--ink);border:1px solid var(--line);border-radius:.35rem;padding:.5rem .75rem;max-width:100%}
button{cursor:pointer}:focus-visible{outline:3px solid var(--ink);outline-offset:3px}
.pair{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:1.5rem}figure{margin:0;min-width:0}h2{font-size:1.2rem;margin:0 0 .5rem}
img{display:block;max-width:100%;height:auto;border:1px solid var(--line)}figcaption{font-size:.85rem;overflow-wrap:anywhere;margin-top:.5rem}
details{margin-top:1.5rem}summary{cursor:pointer}code{overflow-wrap:anywhere}@media(max-width:42rem){.pair{grid-template-columns:1fr}}
</style><main>
<h1>KyCalendar changes</h1>
<p>Browse all 48 matched views. Original screenshots are embedded at their recorded resolution; the display scales them to fit. Each image is verified against its capture hash.</p>
<table><caption>Quality history · exact mean in parentheses</caption><thead><tr><th scope="col">Round</th><th scope="col">Opus 5.5</th><th scope="col">Sol</th></tr></thead><tbody id="scores"></tbody></table>
<p class="muted">Only current-round Opus quality, rounded half-up to one decimal, controls progression at 8.0. Brand and historical rounds stay separate. Gemini is unused because its quota is exhausted. Scores describe screenshots; complete behavior and accessibility acceptance remain separate.</p>
<div class="controls"><label>View<select id="view"></select></label><button id="previous" type="button">Previous</button><button id="next" type="button">Next</button></div>
<p id="status" role="status"></p>
<div class="pair"><figure><h2>Before · fresh baseline</h2><img id="before" alt=""><figcaption id="before-caption"></figcaption></figure>
<figure><h2 id="after-title">After</h2><img id="after" alt=""><figcaption id="after-caption"></figcaption></figure></div>
<details><summary>Capture conditions and limits</summary><p>1280×900 desktop or 390×844 phone, DPR1, en-US, America/New_York, OS light/dark, no saved theme. Same isolated synthetic accounts, calendars and events. Display date October 27, 2026; the real clock is not mocked. Today markers, time indicators and relative sign-in ages can change. Phone List near-duplicates, superseded diagnostics and KyIdentity brand references are retained in the evidence archive and excluded here.</p><p>Identical image bytes indicate calibration rather than product improvement when scores change. These initial-viewport images do not prove unseen content, physical touch behavior, full contrast or keyboard coverage.</p><p>Current captured source: <code id="source"></code>.</p></details>
</main><script>
const data=__DATA__, view=document.getElementById('view');
data.pairs.forEach((p,i)=>view.add(new Option(p.label,String(i))));
view.selectedIndex=Math.max(0,data.pairs.findIndex(p=>p.label==='Month · Mobile · Light'));
for(const values of data.scores){const tr=document.createElement('tr');for(const value of values){const td=document.createElement('td');td.textContent=String(value);tr.append(td)}document.getElementById('scores').append(tr)}
document.getElementById('source').textContent=data.source;
document.getElementById('after-title').textContent='After · round '+data.round;
function show(){const p=data.pairs[view.selectedIndex];for(const side of ['before','after']){const img=document.getElementById(side),item=p[side];img.src=item.image;img.width=item.width;img.height=item.height;img.alt=p.label+' — '+side;document.getElementById(side+'-caption').textContent=item.file+' · SHA-256 '+item.hash}document.getElementById('status').textContent=p.unchanged?'Identical original bytes: any changed score is calibration.':'Different originals: assess layout changes separately from live-clock effects.'}
view.addEventListener('change',show);for(const [id,step] of [['previous',-1],['next',1]])document.getElementById(id).addEventListener('click',()=>{view.selectedIndex=(view.selectedIndex+step+data.pairs.length)%data.pairs.length;show()});show();
</script></html>'''.replace('__DATA__', payload)
args.output.parent.mkdir(parents=True, exist_ok=True)
args.output.write_text(html)
print(f'{args.output}: {len(pairs)} verified pairs; {len(html.encode())} bytes; scored through round {args.scored_through}.')
