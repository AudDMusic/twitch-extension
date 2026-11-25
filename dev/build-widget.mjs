// Builds the hosted widget (widget.audd.tech) as one file: panel.html with
// its styles, its scripts and the logo inside, and without Twitch's helper
// script, which the widget doesn't use.
//
//   node dev/build-widget.mjs [output file]     default: dist/index.html
//
// The file has no "<?" in it, so it can also be saved as index.php.
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const frontend = resolve(root, 'frontend');
const out = resolve(process.argv[2] || resolve(root, 'dist/index.html'));
const read = (name) => readFileSync(resolve(frontend, name), 'utf8');

let html = read('panel.html');

function replaceOnce(pattern, replacement, what) {
  let n = 0;
  html = html.replace(pattern, (...m) => { n += 1; return replacement(...m); });
  if (n === 0) throw new Error(`build-widget: ${what} not found in panel.html`);
}

// The widget runs outside Twitch.
replaceOnce(/[ \t]*<script src="https:\/\/extension-files\.twitch\.tv\/[^"]*"><\/script>\n/g, () => '', "Twitch's helper script");

replaceOnce(/<link rel="stylesheet" href="([^"]+)">/g, (_, href) => `<style>\n${read(href).trim()}\n</style>`, 'a stylesheet');

const logo = 'data:image/png;base64,' + readFileSync(resolve(frontend, 'images/audd-logo.png')).toString('base64');
replaceOnce(/<script src="([^"]+)" defer><\/script>/g, (_, src) => {
  let js = read(src).trim();
  if (src === 'js/audd.js') {
    if (!js.includes("'images/audd-logo.png'")) throw new Error('build-widget: the logo address is not in js/audd.js');
    js = js.replaceAll("'images/audd-logo.png'", `'${logo}'`);
  }
  // A closing script tag inside a script would end it early.
  if (/<\/script/i.test(js)) throw new Error(`build-widget: ${src} contains a closing script tag`);
  return `<script>\n${js}\n</script>`;
}, 'a script');

// Scripts with src and defer ran after the page was parsed; inline scripts run
// where they stand, so they go to the end of the body.
const scripts = [];
html = html.replace(/[ \t]*<script>\n[\s\S]*?\n<\/script>\n/g, (m) => { scripts.push(m.trim()); return ''; });
html = html.replace('</body>', scripts.join('\n') + '\n</body>');

for (const leftover of [/\b(?:src|href)="(?!https?:|data:|#)[^"]*"/, /<\?/]) {
  const m = html.match(leftover);
  if (m) throw new Error(`build-widget: the page still has ${JSON.stringify(m[0])}`);
}

mkdirSync(dirname(out), { recursive: true });
writeFileSync(out, html);
console.log(`${out}  ${Buffer.byteLength(html)} bytes`);
