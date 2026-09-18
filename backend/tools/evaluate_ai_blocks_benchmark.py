"""Aggregate sanitized AI-block benchmark results without reading model content.

The corpus owns prompts and expected coverage. A result file deliberately carries
only case IDs and bounded outcome counters so it can be retained with release
telemetry without storing student requests or generated programs.
"""

from __future__ import annotations

import argparse
import json
import math
from collections import Counter, defaultdict
from pathlib import Path
from typing import Any


CORPUS_SCHEMA_VERSION = 2
RESULT_SCHEMA_VERSION = 2
VERIFICATION_KINDS = ('executable', 'structural', 'manual', 'unsupported')
PASSING_FIELD_BY_VERIFICATION = {
    'executable': 'passed',
    'structural': 'passed',
    'manual': 'approved',
    'unsupported': 'accepted',
}


def read_json(path: Path) -> dict[str, Any]:
    try:
        value = json.loads(path.read_text(encoding='utf-8'))
    except (OSError, json.JSONDecodeError) as exc:
        raise ValueError(f'cannot read JSON: {path}') from exc
    if not isinstance(value, dict):
        raise ValueError(f'JSON root must be an object: {path}')
    return value


def as_bool(value: Any, field: str) -> bool:
    if not isinstance(value, bool):
        raise ValueError(f'{field} must be boolean')
    return value


def as_nonnegative_number(value: Any, field: str) -> float:
    if not isinstance(value, (int, float)) or isinstance(value, bool) or not math.isfinite(value) or value < 0:
        raise ValueError(f'{field} must be a non-negative finite number')
    return float(value)


def as_nonnegative_int(value: Any, field: str) -> int:
    if not isinstance(value, int) or isinstance(value, bool) or value < 0:
        raise ValueError(f'{field} must be a non-negative integer')
    return value


def percentile(values: list[float], fraction: float) -> float | None:
    if not values:
        return None
    ordered = sorted(values)
    return ordered[min(len(ordered) - 1, int((len(ordered) - 1) * fraction))]


def rate(numerator: int, denominator: int) -> float | None:
    return numerator / denominator if denominator else None


def criterion_expectations(case: dict[str, Any], case_id: str) -> dict[str, Counter[str]]:
    expect = case.get('expect')
    if not isinstance(expect, dict):
        raise ValueError(f'corpus case {case_id} must contain an expect object')
    raw_criteria = expect.get('success_criteria')
    if not isinstance(raw_criteria, list) or not raw_criteria:
        raise ValueError(f'corpus case {case_id} must declare one or more success_criteria')
    criteria_by_id: set[str] = set()
    counts = {kind: Counter() for kind in VERIFICATION_KINDS}
    for index, criterion in enumerate(raw_criteria):
        path = f'corpus case {case_id}.expect.success_criteria[{index}]'
        if not isinstance(criterion, dict) or set(criterion) != {'id', 'priority', 'verification'}:
            raise ValueError(f'{path} must contain only id, priority and verification')
        criterion_id = criterion.get('id')
        priority = criterion.get('priority')
        verification = criterion.get('verification')
        if not isinstance(criterion_id, str) or not criterion_id or criterion_id in criteria_by_id:
            raise ValueError(f'{path}.id must be a unique non-empty string')
        if priority not in {'must', 'should'}:
            raise ValueError(f'{path}.priority must be must or should')
        if verification not in VERIFICATION_KINDS:
            raise ValueError(f'{path}.verification is invalid')
        criteria_by_id.add(criterion_id)
        counts[verification]['total'] += 1
        counts[verification]['must_total'] += int(priority == 'must')
    return counts


def criterion_outcomes(raw: Any, expected: dict[str, Counter[str]], path: str) -> dict[str, Counter[str]]:
    if not isinstance(raw, dict) or set(raw) != set(VERIFICATION_KINDS):
        raise ValueError(f'{path} must contain exactly executable, structural, manual and unsupported')
    outcomes: dict[str, Counter[str]] = {}
    for kind in VERIFICATION_KINDS:
        entry = raw.get(kind)
        entry_path = f'{path}.{kind}'
        if not isinstance(entry, dict):
            raise ValueError(f'{entry_path} must be an object')
        passing_field = PASSING_FIELD_BY_VERIFICATION[kind]
        expected_fields = {'total', 'must_total', passing_field, 'must_' + passing_field}
        if kind == 'manual':
            expected_fields.update({'reviewed', 'must_reviewed'})
        if set(entry) != expected_fields:
            raise ValueError(f'{entry_path} has invalid fields')
        values = Counter({field: as_nonnegative_int(entry.get(field), f'{entry_path}.{field}') for field in expected_fields})
        if values['total'] != expected[kind]['total'] or values['must_total'] != expected[kind]['must_total']:
            raise ValueError(f'{entry_path} totals must match the corpus success_criteria')
        if values[passing_field] > values['total'] or values['must_' + passing_field] > values['must_total']:
            raise ValueError(f'{entry_path} completion count exceeds its total')
        if values['must_' + passing_field] > values[passing_field]:
            raise ValueError(f'{entry_path} must completion count exceeds total completion count')
        if kind == 'manual':
            if values['reviewed'] > values['total'] or values['must_reviewed'] > values['must_total']:
                raise ValueError(f'{entry_path} review count exceeds its total')
            if values['approved'] > values['reviewed'] or values['must_approved'] > values['must_reviewed']:
                raise ValueError(f'{entry_path} approval requires a completed review')
        outcomes[kind] = values
    return outcomes


def must_criteria_completed(outcomes: dict[str, Counter[str]]) -> bool:
    return all(
        values['must_' + PASSING_FIELD_BY_VERIFICATION[kind]] == values['must_total']
        for kind, values in outcomes.items()
    )


def add_criterion_counts(destination: Counter[str], outcomes: dict[str, Counter[str]]) -> None:
    for kind, values in outcomes.items():
        passing_field = PASSING_FIELD_BY_VERIFICATION[kind]
        prefix = f'criterion_{kind}_'
        destination[prefix + 'total'] += values['total']
        destination[prefix + 'must_total'] += values['must_total']
        destination[prefix + passing_field] += values[passing_field]
        destination[prefix + 'must_' + passing_field] += values['must_' + passing_field]
        if kind == 'manual':
            destination[prefix + 'reviewed'] += values['reviewed']
            destination[prefix + 'must_reviewed'] += values['must_reviewed']


def criterion_summary(counts: Counter[str]) -> dict[str, dict[str, float | int | None]]:
    summary: dict[str, dict[str, float | int | None]] = {}
    for kind in VERIFICATION_KINDS:
        passing_field = PASSING_FIELD_BY_VERIFICATION[kind]
        prefix = f'criterion_{kind}_'
        entry: dict[str, float | int | None] = {
            'total': counts[prefix + 'total'],
            'must_total': counts[prefix + 'must_total'],
            passing_field: counts[prefix + passing_field],
            'must_' + passing_field: counts[prefix + 'must_' + passing_field],
            'completion_rate': rate(counts[prefix + passing_field], counts[prefix + 'total']),
            'must_completion_rate': rate(counts[prefix + 'must_' + passing_field], counts[prefix + 'must_total']),
        }
        if kind == 'manual':
            entry['reviewed'] = counts[prefix + 'reviewed']
            entry['must_reviewed'] = counts[prefix + 'must_reviewed']
            entry['review_rate'] = rate(counts[prefix + 'reviewed'], counts[prefix + 'total'])
            entry['must_review_rate'] = rate(counts[prefix + 'must_reviewed'], counts[prefix + 'must_total'])
        summary[kind] = entry
    return summary


def all_must_completion_rate(counts: Counter[str]) -> float | None:
    total = sum(counts[f'criterion_{kind}_must_total'] for kind in VERIFICATION_KINDS)
    completed = sum(
        counts[f'criterion_{kind}_must_{PASSING_FIELD_BY_VERIFICATION[kind]}']
        for kind in VERIFICATION_KINDS
    )
    return rate(completed, total)


def aggregate(corpus: dict[str, Any], results: dict[str, Any]) -> dict[str, Any]:
    cases = corpus.get('cases')
    if corpus.get('schema_version') != CORPUS_SCHEMA_VERSION or not isinstance(cases, list):
        raise ValueError('unsupported benchmark corpus')
    corpus_by_id: dict[str, dict[str, Any]] = {}
    for index, item in enumerate(cases):
        if not isinstance(item, dict) or not isinstance(item.get('id'), str) or not isinstance(item.get('category'), str):
            raise ValueError(f'corpus cases[{index}] must contain a unique string id and category')
        case_id = item['id']
        if case_id in corpus_by_id:
            raise ValueError(f'corpus cases[{index}].id is duplicated: {case_id}')
        corpus_by_id[case_id] = item
    if len(corpus_by_id) != len(cases):
        raise ValueError('benchmark corpus has invalid or duplicate case IDs')
    expected_criteria_by_case = {
        case_id: criterion_expectations(case, case_id)
        for case_id, case in corpus_by_id.items()
    }
    if results.get('schema_version') != RESULT_SCHEMA_VERSION or not isinstance(results.get('runs'), list):
        raise ValueError('result schema_version=2 and runs array are required')

    seen: set[str] = set()
    by_category: dict[str, Counter[str]] = defaultdict(Counter)
    totals: Counter[str] = Counter()
    latencies: list[float] = []
    repair_counts: list[float] = []
    token_counts: list[float] = []
    costs: list[float] = []

    for index, raw in enumerate(results['runs']):
        if not isinstance(raw, dict):
            raise ValueError(f'runs[{index}] must be an object')
        case_id = raw.get('case_id')
        if not isinstance(case_id, str) or case_id not in corpus_by_id or case_id in seen:
            raise ValueError(f'runs[{index}].case_id must identify one unique corpus case')
        seen.add(case_id)
        case = corpus_by_id[case_id]
        category = str(case['category'])
        expected_criteria = expected_criteria_by_case[case_id]
        contract_valid = as_bool(raw.get('contract_valid'), f'runs[{index}].contract_valid')
        program_valid = as_bool(raw.get('program_valid'), f'runs[{index}].program_valid')
        program_first_pass = as_bool(raw.get('program_first_pass'), f'runs[{index}].program_first_pass')
        end_to_end_completed = as_bool(raw.get('end_to_end_completed'), f'runs[{index}].end_to_end_completed')
        if program_first_pass and not program_valid:
            raise ValueError(f'runs[{index}].program_first_pass requires program_valid')
        task_field_total = as_nonnegative_int(raw.get('task_field_total'), f'runs[{index}].task_field_total')
        task_field_matched = as_nonnegative_int(raw.get('task_field_matched'), f'runs[{index}].task_field_matched')
        if task_field_matched > task_field_total:
            raise ValueError(f'runs[{index}].task_field_matched exceeds total')
        outcomes = criterion_outcomes(raw.get('criterion_outcomes'), expected_criteria, f'runs[{index}].criterion_outcomes')
        if end_to_end_completed and not must_criteria_completed(outcomes):
            raise ValueError(f'runs[{index}].end_to_end_completed requires every must criterion to pass, be approved or be accepted')
        asset_match = raw.get('asset_match')
        if asset_match not in {'not_applicable', 'top1', 'candidate_recall', 'review_required', 'missing'}:
            raise ValueError(f'runs[{index}].asset_match is invalid')
        totals.update({
            'runs': 1,
            'contract_valid': int(contract_valid),
            'program_valid': int(program_valid),
            'program_first_pass': int(program_first_pass),
            'end_to_end_completed': int(end_to_end_completed),
            'task_field_total': task_field_total,
            'task_field_matched': task_field_matched,
            'asset_top1': int(asset_match == 'top1'),
            'asset_candidate_recall': int(asset_match in {'top1', 'candidate_recall'}),
            'asset_review_required': int(asset_match == 'review_required'),
            'asset_missing': int(asset_match == 'missing'),
        })
        add_criterion_counts(totals, outcomes)
        by_category[category].update({
            'runs': 1,
            'contract_valid': int(contract_valid),
            'program_valid': int(program_valid),
            'program_first_pass': int(program_first_pass),
            'end_to_end_completed': int(end_to_end_completed),
            'task_field_total': task_field_total,
            'task_field_matched': task_field_matched,
        })
        add_criterion_counts(by_category[category], outcomes)
        latencies.append(as_nonnegative_number(raw.get('latency_ms'), f'runs[{index}].latency_ms'))
        repair_counts.append(float(as_nonnegative_int(raw.get('repair_count'), f'runs[{index}].repair_count')))
        token_counts.append(as_nonnegative_number(raw.get('token_count'), f'runs[{index}].token_count'))
        costs.append(as_nonnegative_number(raw.get('cost_usd'), f'runs[{index}].cost_usd'))

    category_summary = {
        category: {
            'run_count': counts['runs'],
            'contract_valid_rate': rate(counts['contract_valid'], counts['runs']),
            'program_valid_rate': rate(counts['program_valid'], counts['runs']),
            'program_first_pass_rate': rate(counts['program_first_pass'], counts['runs']),
            'end_to_end_completion_rate': rate(counts['end_to_end_completed'], counts['runs']),
            'task_field_recall': rate(counts['task_field_matched'], counts['task_field_total']),
            'criterion_verification': criterion_summary(counts),
            'all_must_criterion_completion_rate': all_must_completion_rate(counts),
        }
        for category, counts in sorted(by_category.items())
    }
    return {
        'schema_version': RESULT_SCHEMA_VERSION,
        'corpus_case_count': len(corpus_by_id),
        'evaluated_case_count': totals['runs'],
        'missing_case_ids': sorted(set(corpus_by_id) - seen),
        'contract_valid_rate': rate(totals['contract_valid'], totals['runs']),
        'program_valid_rate': rate(totals['program_valid'], totals['runs']),
        'program_first_pass_rate': rate(totals['program_first_pass'], totals['runs']),
        'end_to_end_completion_rate': rate(totals['end_to_end_completed'], totals['runs']),
        'task_field_recall': rate(totals['task_field_matched'], totals['task_field_total']),
        'criterion_verification': criterion_summary(totals),
        'all_must_criterion_completion_rate': all_must_completion_rate(totals),
        'asset_matching': {
            'top1_count': totals['asset_top1'],
            'candidate_recall_count': totals['asset_candidate_recall'],
            'review_required_count': totals['asset_review_required'],
            'missing_count': totals['asset_missing'],
        },
        'repair': {'total': sum(repair_counts), 'p95': percentile(repair_counts, 0.95)},
        'latency_ms': {'p50': percentile(latencies, 0.5), 'p95': percentile(latencies, 0.95)},
        'token_count': {'total': sum(token_counts), 'p95': percentile(token_counts, 0.95)},
        'cost_usd': {'total': sum(costs), 'p95': percentile(costs, 0.95)},
        'by_category': category_summary,
        'meets_phase0_90_percent_target': (
            len(seen) == len(corpus_by_id)
            and rate(totals['end_to_end_completed'], totals['runs']) is not None
            and all_must_completion_rate(totals) is not None
            and rate(totals['end_to_end_completed'], totals['runs']) >= 0.9
            and all_must_completion_rate(totals) >= 0.9
        ),
    }


def main() -> int:
    parser = argparse.ArgumentParser(description='Aggregate sanitized AI-block benchmark result summaries.')
    parser.add_argument('--corpus', type=Path, required=True)
    parser.add_argument('--results', type=Path, required=True)
    parser.add_argument('--output', type=Path)
    args = parser.parse_args()
    report = aggregate(read_json(args.corpus), read_json(args.results))
    rendered = json.dumps(report, ensure_ascii=False, indent=2) + '\n'
    if args.output:
        args.output.write_text(rendered, encoding='utf-8')
    else:
        print(rendered, end='')
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
