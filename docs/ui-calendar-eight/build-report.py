"""Validate terminal review records and export lossless Council evidence."""
import argparse
import csv
import hashlib
import json
import zipfile
from collections import defaultdict
from decimal import Decimal, ROUND_HALF_UP
from fractions import Fraction
from pathlib import Path
from xml.etree import ElementTree as ET
from xml.sax.saxutils import escape

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--current-round', type=int, required=True)
parser.add_argument('--pr', default='Not opened yet')
args = parser.parse_args()
root = Path(__file__).resolve().parent
load = lambda path: json.loads(path.read_text())
sha = lambda data: hashlib.sha256(data).hexdigest()
weights = dict(zip(['hierarchy', 'typography', 'density_layout', 'color', 'visible_affordances', 'coherence'], [20, 15, 20, 15, 15, 15]))
brand_keys = ['palette_tokens', 'type_geometry', 'shell', 'accent_discipline', 'suite_cohesion']


def rounded(value):
    value = Fraction(value)
    return str((Decimal(value.numerator) / value.denominator).quantize(Decimal('.1'), rounding=ROUND_HALF_UP))


book = {
    'Rounds': [['Round', 'Reviewer', 'Status', 'n', 'Exact mean', 'Half-up', 'Opus progression', 'Independent satisfaction', 'Raw SHA256']],
    'Cohorts': [['Round', 'Reviewer', 'Cohort', 'n', 'Exact mean', 'Half-up', 'Scope']],
    'Quality evidence': [['Round', 'Reviewer', 'File', 'Surface', 'Cohort', 'Theme', 'Viewport', 'State', *weights, 'Unrounded', 'Total', *[key + ' observation' for key in weights], 'SHA256', 'Caveat']],
    'Brand evidence': [['Round', 'Reviewer', 'File', *brand_keys, 'Separate exact mean', *[key + ' observation' for key in brand_keys]]],
    'Inputs': [['File', 'Role', 'SHA256', 'Bytes', 'Width', 'Height']],
    'Checks': [['Item', 'Evidence']],
}

# Only explicit completed rounds are read; a running later round's partial files stay unread.
for turn in range(1, args.current_round + 1):
    inputs = load(root / f'review-inputs-v{turn}.json')
    inventory = {row['file']: row for row in inputs['quality_candidates']}
    for who in ['opus', 'sol', 'gemini']:
        path = root / f'{who}-round{turn}.json'
        if who == 'gemini' or not path.exists():
            status = 'Unused: known exhausted quota' if who == 'gemini' else 'Pending'
            book['Rounds'].append([turn, who, status] + [''] * 6)
            continue
        report = load(path)
        rows = report['files']
        frozen = json.dumps(rows, ensure_ascii=False, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
        assert sha(frozen) == report['freeze']['sha256']
        assert len(frozen) == report['freeze']['bytes']
        assert len(rows) == len(inventory) == len({row['file'] for row in rows})
        assert {row['file'] for row in rows} == set(inventory)
        totals, cohorts = [], defaultdict(list)
        for row in rows:
            source = inventory[row['file']]
            data = (root / row['file']).read_bytes()
            assert sha(data) == row['sha256'] == source['sha256']
            assert len(data) == row['bytes'] == source['bytes']
            assert all(type(row['scores'][key]) is int and 0 <= row['scores'][key] <= 10 for key in weights)
            exact = sum(Fraction(row['scores'][key] * weight, 100) for key, weight in weights.items())
            assert exact == Fraction(str(row.get('total_unrounded', row.get('weighted_total_unrounded'))))
            assert rounded(exact) == str(Decimal(str(row['total'])).quantize(Decimal('.1')))
            total = Fraction(str(row['total']))
            totals.append(total)
            cohorts[row['cohort']].append(total)
            viewport = row['viewport']['kind'] if isinstance(row['viewport'], dict) else row['viewport']
            book['Quality evidence'].append([turn, who, row['file'], source['observed_surface'], row['cohort'], row['theme'], viewport, row['state'], *[row['scores'][key] for key in weights], str(exact), row['total'], *[row['observations'][key] for key in weights], row['sha256'], row['caveat']])
            brand = row['ky_fit']
            scores = brand['scores'] if isinstance(brand, dict) else row['ky_fit_scores']
            reasons = brand.get('observations', brand.get('reasons', {})) if isinstance(brand, dict) else row['ky_fit_observations']
            values = [Fraction(str(value)) for value in scores.values() if isinstance(value, (int, float))]
            book['Brand evidence'].append([turn, who, row['file'], *[scores.get(key, 'N/A') for key in brand_keys], str(sum(values) / len(values)), *[reasons.get(key, 'N/A') for key in brand_keys]])
        mean = sum(totals) / len(totals)
        satisfaction = report.get('satisfied', report.get('independent_satisfied', report.get('independent_satisfaction')))
        if isinstance(satisfaction, dict):
            satisfaction = satisfaction['satisfied']
        gate = ('Accepted' if Decimal(rounded(mean)) >= 8 else 'Below 8.0') if who == 'opus' else 'Does not control progression'
        book['Rounds'].append([turn, who, 'Reviewed', len(rows), str(mean), rounded(mean), gate, satisfaction, sha(path.read_bytes())])
        for cohort, values in sorted(cohorts.items()):
            mean = sum(values) / len(values)
            book['Cohorts'].append([turn, who, cohort, len(values), str(mean), rounded(mean), 'Ky coverage only; not a competitive ranking'])
        book['Checks'].append([f'{who} round {turn} whole-array freeze', json.dumps({key: report['freeze'][key] for key in ['sha256', 'bytes']})])
    artifacts = load(root / inputs.get('all_images', f'artifacts-v{turn}.json'))
    for row in artifacts['files'] + inputs.get('brand_only', []):
        book['Inputs'].append([row['file'], row.get('unscored_reason') or row.get('role', 'Current original'), row['sha256'], row['bytes'], row['width'], row['height']])

for label, evidence in [
    ('Progression', 'Current-round Opus overall, half-up to one decimal >=8.0; no peer/history/brand pooling or cohort veto.'),
    ('Pull request', args.pr),
    ('Scope', 'Recorded captures only; missing flows are unscored. Full behavior/accessibility/integration and production acceptance remain separate.'),
    ('Baseline keyboard probe', 'Failed event-dialog focus return in all four conditions. Later fixes require fresh execution.'),
    ('Full required checks', 'Hosted CI only under owner hard-lock resource restriction; verify current PR head separately.'),
]:
    book['Checks'].append([label, evidence])

# Stdlib XLSX writer shared in shape with the Drive/DNS exports: preserve fractions as text.
ns = 'http://schemas.openxmlformats.org/spreadsheetml/2006/main'
rels = 'http://schemas.openxmlformats.org/package/2006/relationships'
office = 'http://schemas.openxmlformats.org/officeDocument/2006/relationships'
parts = {
    '[Content_Types].xml': '<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>' + ''.join(f'<Override PartName="/xl/worksheets/sheet{i}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>' for i in range(1, len(book) + 1)) + '</Types>',
    '_rels/.rels': f'<Relationships xmlns="{rels}"><Relationship Id="rId1" Type="{office}/officeDocument" Target="xl/workbook.xml"/></Relationships>',
    'xl/workbook.xml': f'<workbook xmlns="{ns}" xmlns:r="{office}"><sheets>' + ''.join(f'<sheet name="{name}" sheetId="{i}" r:id="rId{i}"/>' for i, name in enumerate(book, 1)) + '</sheets></workbook>',
    'xl/_rels/workbook.xml.rels': f'<Relationships xmlns="{rels}">' + ''.join(f'<Relationship Id="rId{i}" Type="{office}/worksheet" Target="worksheets/sheet{i}.xml"/>' for i in range(1, len(book) + 1)) + '</Relationships>',
}
for i, rows in enumerate(book.values(), 1):
    parts[f'xl/worksheets/sheet{i}.xml'] = f'<worksheet xmlns="{ns}"><sheetData>' + ''.join(f'<row r="{j}">' + ''.join('<c t="inlineStr"><is><t xml:space="preserve">' + escape(str(value)) + '</t></is></c>' for value in row) + '</row>' for j, row in enumerate(rows, 1)) + '</sheetData></worksheet>'
path = root / 'KyCalendar-Council.xlsx'
with zipfile.ZipFile(path, 'w', zipfile.ZIP_DEFLATED) as archive:
    for name, xml in parts.items():
        ET.fromstring(xml)
        info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
        info.compress_type = zipfile.ZIP_DEFLATED
        archive.writestr(info, xml.encode())
with zipfile.ZipFile(path) as archive:
    assert archive.testzip() is None
    for i, rows in enumerate(book.values(), 1):
        tree = ET.fromstring(archive.read(f'xl/worksheets/sheet{i}.xml'))
        actual = [[cell.find(f'{{{ns}}}is/{{{ns}}}t').text or '' for cell in row] for row in tree.findall(f'{{{ns}}}sheetData/{{{ns}}}row')]
        assert actual == [[str(value) for value in row] for row in rows]
with (root / 'ratings.csv').open('w', newline='') as output:
    csv.writer(output, lineterminator='\n').writerows(book['Quality evidence'])
lines = ['# KyCalendar Council', '', 'Quality and brand stay separate. Current-round Opus alone controls progression; historical rounds are not pooled.', '', '| Round | Reviewer | Status | n | Exact quality | Half-up | Progression | Satisfied |', '|---|---|---|---:|---|---:|---|---|']
for row in book['Rounds'][1:]:
    lines.append('| ' + ' | '.join(str(value) if value != '' else '—' for value in row[:8]) + ' |')
lines += ['', f'PR: {args.pr}', '', '[Workbook](KyCalendar-Council.xlsx) · [Per-file CSV](ratings.csv). Raw reviews and source images remain unchanged.', '', 'Coverage summaries are not rankings or full behavior/accessibility/production acceptance. Gemini is unused because its quota is exhausted.', '']
(root / 'COUNCIL.md').write_text('\n'.join(lines))
print(f'Validated {args.current_round} round(s); exported workbook, CSV and score history; XLSX CRC/XML/data round-trip passed.')
