// Builds the app into dist/ (embedded into the Go binary).
// Plain esbuild plus the Svelte compiler; no other tooling.
import * as esbuild from 'esbuild';
import { compile } from 'svelte/compiler';
import { readFile, writeFile, mkdir, rm, cp, readdir } from 'node:fs/promises';
import path from 'node:path';
import { createHash } from 'node:crypto';

const dev = process.argv.includes('--dev');
const css = new Map();

const sveltePlugin = {
  name: 'svelte',
  setup(build) {
    build.onLoad({ filter: /\.svelte$/ }, async (args) => {
      const source = await readFile(args.path, 'utf8');
      try {
        const r = compile(source, { filename: args.path, generate: 'client', css: 'external', dev });
        let contents = r.js.code;
        if (r.css?.code) {
          const id = args.path + '.css';
          css.set(id, r.css.code);
          contents += `\nimport ${JSON.stringify(id)};`;
        }
        const warnings = r.warnings
          .filter((w) => !String(w.code).startsWith('a11y_') || !dev)
          .map((w) => ({ text: `${w.code}: ${w.message}`, location: w.start && { file: args.path, line: w.start.line, column: w.start.column } }));
        return { contents, loader: 'js', resolveDir: path.dirname(args.path), warnings };
      } catch (e) {
        return { errors: [{ text: e.message, location: e.start && { file: args.path, line: e.start.line, column: e.start.column } }] };
      }
    });
    build.onResolve({ filter: /\.svelte\.css$/ }, (args) => ({ path: args.path, namespace: 'svelte-css' }));
    build.onLoad({ filter: /.*/, namespace: 'svelte-css' }, (args) => ({ contents: css.get(args.path), loader: 'css' }));
  },
};

const out = 'dist';
await rm(path.join(out, 'assets'), { recursive: true, force: true });
await mkdir(path.join(out, 'assets'), { recursive: true });

const result = await esbuild.build({
  entryPoints: ['src/main.js'],
  bundle: true,
  format: 'esm',
  outdir: path.join(out, 'assets'),
  entryNames: '[name]-[hash]',
  assetNames: '[name]-[hash]',
  minify: !dev,
  sourcemap: dev,
  target: ['es2022', 'safari16'],
  conditions: ['svelte', 'browser', dev ? 'development' : 'production'],
  mainFields: ['svelte', 'browser', 'module', 'main'],
  metafile: true,
  plugins: [sveltePlugin],
  logLevel: 'warning',
});

const outputs = Object.keys(result.metafile.outputs).map((p) => path.relative(out, p).split(path.sep).join('/'));
const js = outputs.find((p) => p.endsWith('.js'));
const style = outputs.find((p) => p.endsWith('.css'));

// static files
for (const f of await readdir('public')) await cp(path.join('public', f), path.join(out, f), { recursive: true });

const version = createHash('sha256').update(outputs.join()).digest('hex').slice(0, 10);
let html = await readFile('index.html', 'utf8');
html = html.replace('%STYLE%', `/${style}`).replace('%SCRIPT%', `/${js}`);
await writeFile(path.join(out, 'index.html'), html);

// service worker: cache the app shell, never the documents
const sw = (await readFile('src/sw.js', 'utf8'))
  .replace('%VERSION%', version)
  .replace('%ASSETS%', JSON.stringify(['/', `/${js}`, `/${style}`, '/manifest.webmanifest', '/icon.svg', '/icon-192.png']));
await writeFile(path.join(out, 'sw.js'), sw);
console.log(`built ${js}, ${style}`);
