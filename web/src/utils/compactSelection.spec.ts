// SPDX-License-Identifier: AGPL-3.0-only
import {
  COMPACT_CHIP_LIMIT,
  compactSelectionSlot,
  orderSelectedFirst,
  selectedValues,
} from './compactSelection';

// Owner, 2026-10-02: "show the chip when they are max 2 items, over 2 just make
// it say 'N items selected' (no chips), and when multiple items are selected,
// the selected ones need to appear top in the options list."

describe('compactSelectionSlot — chips up to 2, then one summary', () => {
  it('the limit is 2', () => {
    expect(COMPACT_CHIP_LIMIT).toBe(2);
  });

  it('one or two selected → every one is a chip', () => {
    expect(compactSelectionSlot(1, 0)).toBe('chip');
    expect(compactSelectionSlot(2, 0)).toBe('chip');
    expect(compactSelectionSlot(2, 1)).toBe('chip');
  });

  it('three or more → the summary once (index 0), nothing for the rest, never a chip', () => {
    for (const count of [3, 4, 25]) {
      expect(compactSelectionSlot(count, 0)).toBe('summary');
      for (let i = 1; i < count; i += 1) expect(compactSelectionSlot(count, i)).toBe('none');
    }
  });

  it('nothing selected, or an index outside the selection → nothing', () => {
    expect(compactSelectionSlot(0, 0)).toBe('none');
    expect(compactSelectionSlot(2, 2)).toBe('none');
    expect(compactSelectionSlot(3, -1)).toBe('none');
  });

  it('honours a different limit', () => {
    expect(compactSelectionSlot(3, 2, 3)).toBe('chip');
    expect(compactSelectionSlot(4, 0, 3)).toBe('summary');
    expect(compactSelectionSlot(1, 0, 0)).toBe('summary');
  });
});

describe('orderSelectedFirst — the selected options at the top', () => {
  const items = [
    { value: 'a', title: 'Anna' },
    { value: 'b', title: 'Bruno' },
    { value: 'c', title: 'Carla' },
    { value: 'd', title: 'Dario' },
    { value: 'e', title: 'Elena' },
  ];
  const valueOf = (i: { value: string }) => i.value;

  it('selected first, both groups in their original order', () => {
    const out = orderSelectedFirst(items, ['d', 'b'], valueOf).map((i) => i.value);
    expect(out).toEqual(['b', 'd', 'a', 'c', 'e']);
  });

  it('nothing selected → the original order, as a new array', () => {
    const out = orderSelectedFirst(items, [], valueOf);
    expect(out.map((i) => i.value)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(out).not.toBe(items);
  });

  it('a selected value no longer among the options is simply absent', () => {
    expect(orderSelectedFirst(items, ['zz', 'c'], valueOf).map((i) => i.value)).toEqual(['c', 'a', 'b', 'd', 'e']);
  });

  it('numbers and strings match (an id may come back as either)', () => {
    const numeric = [{ value: 1 }, { value: 2 }, { value: 3 }];
    expect(orderSelectedFirst(numeric, ['3'], (i) => i.value).map((i) => i.value)).toEqual([3, 1, 2]);
  });

  it('never drops or duplicates an option', () => {
    const out = orderSelectedFirst(items, ['e', 'a', 'c'], valueOf);
    expect(out).toHaveLength(items.length);
    expect(new Set(out.map((i) => i.value)).size).toBe(items.length);
  });
});

describe('selectedValues', () => {
  it('reads a multi-select model; anything else is no selection', () => {
    expect(selectedValues(['a', 2])).toEqual(['a', 2]);
    expect(selectedValues([])).toEqual([]);
    expect(selectedValues(null)).toEqual([]);
    expect(selectedValues('all')).toEqual([]);
    expect(selectedValues(['a', null, undefined, { x: 1 }])).toEqual(['a']);
  });
});
