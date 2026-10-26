// A tiny stand-in for the Paperless-ngx REST API, enough for the importer.
import http from 'node:http';
import fs from 'node:fs';

export function startPaperless(pdfPath, port = 18000) {
  const pdf = fs.readFileSync(pdfPath);
  const page = (results) => ({ count: results.length, next: null, previous: null, results });
  const tags = [
    { id: 1, name: 'Strom', color: '#a6cee3', match: 'strom stromrechnung', matching_algorithm: 1, is_insensitive: true },
    { id: 2, name: 'Steuer', color: '#b2df8a', match: '', matching_algorithm: 0, is_insensitive: true },
  ];
  const correspondents = [
    { id: 1, name: 'Stadtwerke Musterstadt', match: 'Stadtwerke Musterstadt', matching_algorithm: 3, is_insensitive: true },
    { id: 2, name: 'Finanzamt', match: '', matching_algorithm: 6, is_insensitive: true },
  ];
  const types = [{ id: 1, name: 'Rechnung', match: '^Rechnung', matching_algorithm: 4, is_insensitive: true }];
  const fields = [{ id: 1, name: 'Vertragsnummer', data_type: 'string' }];
  const docs = [
    { id: 101, title: 'Stromrechnung Juli', content: 'Stadtwerke Musterstadt Stromrechnung Juli Gesamtbetrag 81,10 EUR', correspondent: 1,
      document_type: 1, tags: [1], created: '2026-07-24', archive_serial_number: 17, notes: [{ id: 1, note: 'bezahlt am 01.08.' }],
      custom_fields: [{ field: 1, value: 'V-4711' }], original_file_name: 'scan1.pdf' },
    { id: 102, title: 'Einkommensteuerbescheid 2025', content: 'Finanzamt Bescheid für 2025', correspondent: 2,
      document_type: null, tags: [2], created: '2026-05-02T00:00:00+02:00', archive_serial_number: null, notes: [],
      custom_fields: [], original_file_name: 'bescheid.pdf' },
    { id: 103, title: 'Stromrechnung August', content: 'Stadtwerke Musterstadt Stromrechnung August', correspondent: 1,
      document_type: 1, tags: [1], created: '2026-08-24', archive_serial_number: null, notes: [], custom_fields: [], original_file_name: 'scan3.pdf' },
  ];
  const srv = http.createServer((req, res) => {
    if (req.headers.authorization !== 'Token test-token') {
      res.writeHead(401, { 'Content-Type': 'application/json' });
      return res.end('{"detail":"Invalid token."}');
    }
    const url = new URL(req.url, 'http://x');
    const json = (o) => { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify(o)); };
    const dl = url.pathname.match(/^\/api\/documents\/(\d+)\/download\/$/);
    if (dl) {
      // each document a different file (a comment after %%EOF changes the bytes)
      res.writeHead(200, { 'Content-Type': 'application/pdf' });
      return res.end(Buffer.concat([pdf, Buffer.from(`\n% paperless ${dl[1]}\n`)]));
    }
    switch (url.pathname) {
      case '/api/tags/': return json(page(tags));
      case '/api/correspondents/': return json(page(correspondents));
      case '/api/document_types/': return json(page(types));
      case '/api/custom_fields/': return json(page(fields));
      case '/api/documents/': return json(page(docs));
    }
    res.writeHead(404);
    res.end();
  });
  return new Promise((r) => srv.listen(port, '127.0.0.1', () => r(srv)));
}
