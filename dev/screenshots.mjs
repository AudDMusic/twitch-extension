// Screenshots of every view and state from the local preview, plus a check
// that pages make no request outside the preview server and hit no CSP or
// script errors.
//
//   cd dev && go run ./preview -before <old frontend folder> &
//   node screenshots.mjs [outDir] [filter]
//
// Needs playwright-core and a Chromium: set PLAYWRIGHT_CORE to the
// playwright-core folder and CHROMIUM to the browser binary if they aren't
// found on their own. PREVIEW_URL defaults to http://127.0.0.1:8787.
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';

const require = createRequire(import.meta.url);
const pw = require(process.env.PLAYWRIGHT_CORE || 'playwright-core');
const BASE = process.env.PREVIEW_URL || 'http://127.0.0.1:8787';
const OUT = process.argv[2] || 'screenshots';
// Optional: only the states whose name contains one of these comma-separated words.
const FILTER = process.argv[3] ? process.argv[3].split(',').filter(Boolean) : null;
fs.mkdirSync(OUT, { recursive: true });

const panel = (q) => `/ext/panel.html?anchor=panel&platform=web&${q}`;
const mobile = (q) => `/ext/panel.html?anchor=panel&platform=mobile&${q}`;
const component = (q, bg) => `/dev/static/stage.html?bg=${bg}&src=${encodeURIComponent(`/ext/video_component.html?anchor=component&platform=web&${q}`)}`;
const widget = (q, bg, w, h) => `/dev/static/stage.html?bg=${bg}&w=${w}&h=${h}&pos=left&src=${encodeURIComponent(`/ext/panel.html?ch=${q}`)}`;
const config = (q) => `/ext/config.html?mode=config&${q}`;

const P = { w: 318, h: 300 };
const STAGE = { w: 1138, h: 640 };
const CFG = { w: 960, h: 1040 };

// [name, url, size, options]
const shots = [
  // Before (0.0.4 from the repository)
  ['before_panel_dark', '/before/panel.html?anchor=panel&platform=web&channel=typical', P],
  ['before_panel_long', '/before/panel.html?anchor=panel&platform=web&channel=long', P],
  ['before_panel_empty', '/before/panel.html?anchor=panel&platform=web&channel=empty-prod', P],
  ['before_component_bright', `/dev/static/stage.html?bg=bright&src=${encodeURIComponent('/before/video_component.html?anchor=component&channel=typical')}`, STAGE],
  ['before_config_not-linked', '/before/config.html?mode=config&channel=cfg-before&account=none', { w: 960, h: 700 }],
  ['before_config_linked', '/before/config.html?mode=config&channel=cfg-before-active&account=active', { w: 960, h: 900 }],
  ['before_mobile_dark', '/before/panel.html?anchor=panel&platform=mobile&channel=typical', { w: 375, h: 640 }],

  // Panel
  ...['dark', 'light'].flatMap((t) => [
    [`after_panel_${t}_playing`, panel(`theme=${t}&channel=typical`), P],
    [`after_panel_${t}_just-played`, panel(`theme=${t}&channel=just`), P],
    [`after_panel_${t}_stale`, panel(`theme=${t}&channel=stale`), P],
    [`after_panel_${t}_one-song`, panel(`theme=${t}&channel=one`), P],
    [`after_panel_${t}_full-40`, panel(`theme=${t}&channel=full`), P],
    [`after_panel_${t}_long-strings`, panel(`theme=${t}&channel=long`), P],
    [`after_panel_${t}_no-cover`, panel(`theme=${t}&channel=nocover`), P],
    [`after_panel_${t}_empty`, panel(`theme=${t}&channel=empty`), P],
    [`after_panel_${t}_loading`, panel(`theme=${t}&channel=slow`), P, { wait: 600 }],
    [`after_panel_${t}_backend-down`, panel(`theme=${t}&channel=down`), P, { wait: 2500 }],
  ]),
  ['after_panel_dark_youtube', panel('theme=dark&channel=youtube'), P],
  ['after_panel_dark_legacy-backend', panel('theme=dark&channel=legacy'), P],
  ['after_panel_dark_empty-production-shape', panel('theme=dark&channel=empty-prod'), P],
  ['after_panel_dark_full-40-scrolled', panel('theme=dark&channel=full'), P, { scroll: '.history__scroll' }],
  ['after_panel_dark_new-song-arrives', panel('theme=dark&channel=push-demo&every=3'), P, { wait: 4200 }],
  ['after_panel_dark_broadcaster-not-set-up', panel('theme=dark&role=broadcaster&account=none&channel=empty'), P],
  ['after_panel_dark_broadcaster-expired', panel('theme=dark&role=broadcaster&account=expired&channel=stale'), P],
  ['after_panel_dark_broadcaster-expiring', panel('theme=dark&role=broadcaster&account=expiring&channel=typical'), P],
  ['after_panel_light_broadcaster-unpaid', panel('theme=light&role=broadcaster&account=unpaid&channel=stale'), P],
  ['after_panel_dark_short-height-120', panel('theme=dark&channel=typical'), { w: 318, h: 120 }],
  ['after_panel_dark_tall-500', panel('theme=dark&channel=full'), { w: 318, h: 500 }],
  ['after_panel_dark_hover-row-focus', panel('theme=dark&channel=typical'), P, { focus: 3 }],

  // Mobile
  ['after_mobile_dark_playing', mobile('theme=dark&channel=typical'), { w: 375, h: 640 }],
  ['after_mobile_light_playing', mobile('theme=light&channel=typical'), { w: 375, h: 640 }],
  ['after_mobile_dark_empty', mobile('theme=dark&channel=empty'), { w: 375, h: 640 }],

  // Video component over video
  ['after_component_bright_playing', component('theme=light&channel=typical', 'bright'), STAGE],
  ['after_component_busy_playing', component('theme=dark&channel=typical', 'busy'), STAGE],
  ['after_component_dark_playing', component('theme=dark&channel=typical', 'dark'), STAGE],
  ['after_component_bright_just-played', component('theme=dark&channel=just', 'bright'), STAGE],
  ['after_component_busy_played-20-min-ago-compact', component('theme=dark&channel=recent', 'busy'), STAGE],
  ['after_component_bright_played-3-hours-ago-hidden', component('theme=dark&channel=stale', 'bright'), STAGE],
  ['after_component_bright_long-strings', component('theme=dark&channel=long', 'bright'), STAGE],
  ['after_component_busy_no-cover', component('theme=dark&channel=nocover', 'busy'), STAGE],
  ['after_component_dark_empty-hidden', component('theme=dark&channel=empty', 'dark'), STAGE],
  ['after_component_bright_backend-down-hidden', component('theme=dark&channel=down', 'bright'), STAGE, { wait: 2500 }],
  ['after_component_closeup_playing', `/ext/video_component.html?anchor=component&platform=web&theme=dark&channel=typical`, { w: 320, h: 160 }, { scale: 3, bodyBg: '#3b4a5c' }],

  // Hosted widget / OBS
  ['after_widget_busy_default', widget('typical', 'busy', 600, 160), STAGE],
  ['after_widget_dark_shadow', widget('typical&shadow', 'dark', 600, 160), STAGE],
  ['after_widget_busy_background', widget('typical&background', 'busy', 600, 160), STAGE],
  ['after_widget_busy_history-background', widget('typical&history&background', 'busy', 600, 420), STAGE],
  ['after_widget_dark_history', widget('typical&history&shadow', 'dark', 600, 420), STAGE],
  ['after_widget_bright_black-font', widget('typical&black-font', 'bright', 600, 160), STAGE],
  ['after_widget_bright_background-black-font-history', widget('typical&background&black-font&history', 'bright', 600, 420), STAGE],
  ['after_widget_dark_tall-400', widget('long&background', 'dark', 400, 400), STAGE],
  ['after_widget_dark_empty', widget('empty', 'dark', 600, 160), STAGE],
  ['after_widget_busy_wide-1000x120', widget('typical&background', 'busy', 1000, 120), STAGE],

  // Configuration page
  ['after_config_loading', config('theme=dark&channel=cfg-slow&account=slow'), CFG, { wait: 800 }],
  ['after_config_not-linked', config('theme=dark&channel=cfg-none&account=none'), CFG],
  ['after_config_light_not-linked', config('theme=light&channel=cfg-none-l&account=none'), CFG],
  // Sign in with AudD: the flows click the link and act in the AudD tab it opens
  ['after_config_sign-in-waiting', config('theme=dark&channel=cfg-signin&account=none'), CFG, { flow: 'signin-wait' }],
  ['after_config_light_sign-in-waiting', config('theme=light&channel=cfg-signin-l&account=none'), CFG, { flow: 'signin-wait' }],
  ['after_config_narrow_sign-in-waiting', config('theme=dark&channel=cfg-signin-n&account=none'), { w: 420, h: 1100 }, { flow: 'signin-wait' }],
  ['after_config_sign-in-tab-audd-stand-in', config('theme=dark&channel=cfg-signin-tab&account=none'), CFG, { flow: 'signin-tab-audd' }],
  ['after_config_sign-in-tab-link-account-stand-in', config('theme=dark&channel=cfg-signin-tab2&account=none'), CFG, { flow: 'signin-tab-link' }],
  ['after_config_sign-in-linking', config('theme=dark&channel=cfg-signin-slow&account=slowlink'), CFG, { flow: 'signin-approve', after: 3000 }],
  ['after_config_signed-in-linked', config('theme=dark&channel=cfg-signin2&account=none'), CFG, { flow: 'signin-approve' }],
  ['after_config_sign-in-declined', config('theme=dark&channel=cfg-signin3&account=none'), CFG, { flow: 'signin-decline' }],
  ['after_config_light_sign-in-declined', config('theme=light&channel=cfg-signin3-l&account=none'), CFG, { flow: 'signin-decline' }],
  ['after_config_sign-in-failed-expired', config('theme=dark&channel=cfg-signin4&account=none'), CFG, { flow: 'signin-sign_in_expired' }],
  ['after_config_sign-in-failed-permission-missing', config('theme=dark&channel=cfg-signin5&account=none'), CFG, { flow: 'signin-token_permission_missing' }],
  ['after_config_sign-in-failed-account-service', config('theme=dark&channel=cfg-signin6&account=none'), CFG, { flow: 'signin-account_service_unavailable' }],
  ['after_config_sign-in-timed-out', config('theme=dark&channel=cfg-signin7&account=none'), CFG, { flow: 'signin-timeout', clock: true }],
  ['after_config_sign-in-unavailable', config('theme=dark&channel=cfg-nobackend&account=none&signin=nobackend'), CFG],
  ['after_config_sign-in-unavailable-backend-down', config('theme=dark&channel=cfg-down-signin&account=none&signin=down'), CFG],
  ['after_config_sign-in-unavailable-too-many', config('theme=dark&channel=cfg-busy&account=none&signin=busy'), CFG],
  ['after_config_sign-in-unavailable-after-click', config('theme=dark&channel=cfg-noresult&account=none&signin=noresult'), CFG, { flow: 'signin-close', after: 8000 }],
  ['after_config_sign-in-not-broadcaster', config('theme=dark&channel=cfg-moderator&account=none&role=moderator'), CFG],
  ['after_config_paste-token', config('theme=dark&channel=cfg-paste&account=none'), CFG, { flow: 'paste-open' }],
  ['after_config_paste-token-wrong', config('theme=dark&channel=cfg-paste2&account=none'), CFG, { flow: 'paste-wrong' }],
  ['after_config_create-account-unpaid', config('theme=dark&channel=cfg-signup&account=none'), CFG, { flow: 'signup' }],
  ['after_config_linked-active', config('theme=dark&channel=cfg-active&account=active'), CFG],
  ['after_config_light_linked-active', config('theme=light&channel=cfg-active-l&account=active'), CFG],
  ['after_config_linked-expiring', config('theme=dark&channel=cfg-expiring&account=expiring'), CFG],
  ['after_config_linked-expired-renew', config('theme=dark&channel=cfg-expired&account=expired'), CFG],
  ['after_config_linked-no-subscription', config('theme=dark&channel=cfg-unpaid&account=unpaid'), CFG],
  ['after_config_awaiting-payment', config('theme=dark&channel=cfg-pay&account=unpaid'), CFG, { flow: 'pay-click' }],
  ['after_config_paid', config('theme=dark&channel=cfg-pay2&account=unpaid'), CFG, { flow: 'pay-complete' }],
  ['after_config_error-internal', config('theme=dark&channel=cfg-err&account=error-internal_error'), CFG],
  ['after_config_dashboard-unreachable', config('theme=dark&channel=cfg-down&account=down'), CFG],
  ['after_config_narrow', config('theme=dark&channel=cfg-narrow&account=active'), { w: 420, h: 1500 }],
];

const browser = await pw.chromium.launch({
  executablePath: process.env.CHROMIUM || undefined,
  args: ['--force-color-profile=srgb', '--font-render-hinting=none'],
});

const problems = [];
let count = 0;
for (const [name, url, size, opt = {}] of shots) {
  if (FILTER && !FILTER.some((word) => name.includes(word))) continue;
  await fetch(BASE + '/dev/reset').catch(() => {});
  const context = await browser.newContext({
    viewport: { width: size.w, height: size.h },
    deviceScaleFactor: opt.scale || 2,
    locale: 'en-US',
    timezoneId: 'Europe/Berlin',
    colorScheme: 'dark',
  });
  const page = await context.newPage();
  if (opt.clock) await page.clock.install();
  const log = [];
  page.on('request', (req) => {
    const u = new URL(req.url());
    if (!['127.0.0.1:8787', 'localhost:8787'].includes(u.host) && !u.protocol.startsWith('data') && !u.protocol.startsWith('blob')) {
      // The old frontend loads Roboto from Google Fonts (allowed by Twitch).
      if (!(name.startsWith('before_') && /fonts\.(googleapis|gstatic)\.com$/.test(u.host))) log.push('outside request: ' + req.url());
    }
  });
  page.on('console', (m) => {
    // Failed fetches and images are part of the mocked states; failed
    // scripts and styles are caught below.
    if (/^Failed to load resource/.test(m.text())) return;
    if (m.type() === 'error' || /Content Security Policy|Refused/.test(m.text())) log.push(`console ${m.type()}: ${m.text()}`);
  });
  page.on('response', (r) => {
    const type = r.request().resourceType();
    if (r.status() >= 400 && ['document', 'script', 'stylesheet', 'font'].includes(type)) log.push(`HTTP ${r.status()} for ${type} ${r.url()}`);
  });
  page.on('pageerror', (e) => log.push('page error: ' + e.message));
  await page.goto(BASE + url, { waitUntil: 'load' });
  if (opt.bodyBg) await page.addStyleTag({ content: `html,body{background:${opt.bodyBg} !important}` });
  await page.waitForTimeout(opt.wait || 1400);

  // A flow may hand back another page to take instead (the AudD tab).
  const shot = (opt.flow && await runFlow(context, page, opt)) || page;
  if (opt.scroll) {
    await page.$eval(opt.scroll, (el) => { el.scrollTop = el.scrollHeight; });
    await page.waitForTimeout(200);
  }
  if (opt.focus) {
    for (let i = 0; i < opt.focus; i++) await page.keyboard.press('Tab');
    await page.waitForTimeout(200);
  }
  await shot.screenshot({ path: path.join(OUT, name + '.png') });
  count++;
  // The old frontend has its own known errors; report only the new one's.
  if (log.length && !name.startsWith('before_')) problems.push(`${name}:\n  ${log.join('\n  ')}`);
  await context.close();
}

async function runFlow(context, page, opt) {
  const flow = opt.flow;
  if (flow.startsWith('signin-')) {
    // signin-<wait|close|timeout|approve|decline|error code|tab-audd|tab-link>
    const step = flow.slice('signin-'.length);
    const [tab] = await Promise.all([
      context.waitForEvent('page'),
      page.getByRole('link', { name: 'Sign in with AudD' }).click(),
    ]);
    await tab.waitForLoadState();
    if (step === 'tab-audd') return tab;
    if (step === 'approve' || step === 'tab-link') {
      await tab.getByRole('button', { name: 'Approve' }).click();
      await tab.waitForLoadState();
      if (step === 'tab-link') return tab;
      await tab.getByRole('button', { name: 'Link account' }).click();
    } else if (step === 'decline') {
      await tab.getByRole('button', { name: 'Decline' }).click();
    } else if (['sign_in_expired', 'token_permission_missing', 'account_service_unavailable'].includes(step)) {
      await tab.getByRole('button', { name: step }).click();
    }
    await tab.waitForLoadState();
    await tab.close();
    await page.bringToFront();
    // The page gives up 10 minutes after the click.
    if (step === 'timeout') await page.clock.fastForward('10:30');
    await page.waitForTimeout(opt.after || 3500);
  }
  if (flow === 'paste-open' || flow === 'paste-wrong') {
    await page.getByRole('button', { name: 'Paste it instead' }).click();
    if (flow === 'paste-wrong') {
      await page.getByLabel('API token').fill('not-a-real-token');
      await page.getByRole('button', { name: 'Connect' }).click();
      await page.waitForTimeout(900);
    }
  }
  if (flow === 'signup') {
    await page.getByRole('button', { name: /Create an AudD account/ }).click();
    await page.waitForTimeout(1200);
  }
  if (flow === 'pay-click' || flow === 'pay-complete') {
    const [payPage] = await Promise.all([
      context.waitForEvent('page'),
      page.getByRole('link', { name: /Turn on: \$45/ }).click(),
    ]);
    await payPage.waitForLoadState();
    if (flow === 'pay-complete') {
      await payPage.getByRole('button', { name: /Pay \$45/ }).click();
      await payPage.close();
      await page.bringToFront();
      await page.getByRole('button', { name: 'Check now' }).click();
      await page.waitForTimeout(1200);
    } else {
      await payPage.close();
      await page.waitForTimeout(400);
    }
  }
}

await browser.close();
console.log(`${count} screenshots in ${OUT}`);
if (problems.length) {
  console.log('Problems:\n' + problems.join('\n'));
  process.exitCode = 1;
} else {
  console.log('No requests outside the preview server, no CSP violations, no script errors.');
}
