import test from 'node:test';
import assert from 'node:assert/strict';
import { parseAmount, previewPath, snippetParts } from '../src/lib/format.js';

test('amounts as people type them', () => {
  assert.equal(parseAmount('84,20'), 8420);
  assert.equal(parseAmount('84.20'), 8420);
  assert.equal(parseAmount('1.234,56 €'), 123456);
  assert.equal(parseAmount('1,234.56'), 123456);
  assert.equal(parseAmount('12'), 1200);
  assert.equal(parseAmount(''), null);
});

test('file name preview matches the server', () => {
  const d = { id: 7, title: 'Stromrechnung Oktober', sender: 'Stadtwerke Köln', date: '2026-10-24', tags: ['Strom'] };
  assert.equal(previewPath('{year}/{month}/{date}_{sender}_{title}', d), '2026/10/2026-10-24_Stadtwerke-Köln_Stromrechnung-Oktober.pdf');
  assert.equal(previewPath('{year}/{month}/{date}_{sender}_{title}', { ...d, sender: '' }), '2026/10/2026-10-24_Stromrechnung-Oktober.pdf');
  assert.equal(previewPath('../{year}/{title}', d), '2026/Stromrechnung-Oktober.pdf');
});

test('search snippets', () => {
  assert.deepEqual(snippetParts('Ihre \x02Strom\x03rechnung'), [{ text: 'Ihre ' }, { text: 'Strom', hit: true }, { text: 'rechnung' }]);
});
