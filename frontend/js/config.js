// The configuration page: connect an AudD account (Sign in with AudD, a new
// account, or a pasted API token), turn on recognition, add the extension to
// the channel, and an optional OBS widget.
(function () {
  'use strict';
  var AudD = window.AudD;
  var E = AudD.endpoints;
  var el = AudD.el;
  var icon = AudD.icon;
  var ext = window.Twitch && window.Twitch.ext;

  var PRICE = '$45';

  // What went wrong, why, and what to do. Keys are the error codes of
  // twitch.php, of the backend's sign-in (/lastSong/signIn/), and this page's.
  var MESSAGES = {
    network: 'Can’t reach AudD. Check your connection, then try again.',
    internal_error: 'Something went wrong on AudD’s side. Try again in a minute; if it keeps happening, email hello@audd.io.',
    no_jwt: 'Twitch didn’t pass your channel details to AudD. Reload this page and try again; if it keeps happening, email hello@audd.io.',
    wrong_jwt: 'AudD couldn’t confirm your Twitch session. Reload this page and try again; if it keeps happening, email hello@audd.io.',
    not_broadcaster: 'Only the channel’s broadcaster can connect an AudD account here.',
    no_token: 'Paste your API token first.',
    wrong_token: 'No AudD account has this API token. Copy it again from dashboard.audd.io; if it still doesn’t work, email hello@audd.io.',
    signin_unavailable: 'Signing in with AudD isn’t available right now. Paste your API token instead: it’s on dashboard.audd.io.',
    signin_declined: 'The sign-in was declined on the AudD page. Sign in again when you’re ready.',
    signin_timeout: 'The sign-in wasn’t finished within 10 minutes, so it stopped. Sign in again when you’re ready.',
    token_permission_missing: 'AudD didn’t share your API token because that permission was unticked. Sign in again and leave it ticked.',
    sign_in_expired: 'The AudD sign-in expired before your account was linked. Sign in again.',
    account_service_unavailable: 'AudD’s account service didn’t respond. Try again in a minute, or paste your API token instead.'
  };

  function message(code) {
    return MESSAGES[code] || ('AudD couldn’t link your account (error “' + code + '”). Try again, or email hello@audd.io.');
  }

  var auth = null;
  var model = {
    account: 'loading',      // loading | none | connected | error
    recognition: null,       // active | expired | unpaid
    info: null,              // account fields from twitch.php
    connect: 'choose',       // choose | token | signin
    busy: null,              // signup | token
    error: null,             // {code, where: 'account' | 'token' | 'recognition'}
    session: { ready: null, starting: false, failed: null, failedAt: 0 }, // the next sign-in, see prepareSession
    signin: null,            // a sign-in in progress, see signInOpened
    awaitingPayment: false,
    justPaid: false,
    widget: { history: false, background: false, dark: false }
  };

  var nodes = {
    account: document.getElementById('account'),
    recognition: document.getElementById('recognition'),
    channel: document.getElementById('channel'),
    stepAccount: document.getElementById('step-account'),
    stepRecognition: document.getElementById('step-recognition'),
    stepChannel: document.getElementById('step-channel'),
    obs: document.getElementById('obs'),
    widgetUrl: document.getElementById('widget-url'),
    widgetCopy: document.getElementById('widget-copy'),
    preview: document.getElementById('preview'),
    previewCaption: document.getElementById('preview-caption'),
    announcer: document.getElementById('announce')
  };

  // One polite live region for the changes that matter; the steps
  // themselves are redrawn and would be read again in full.
  function announce(text) {
    nodes.announcer.textContent = '';
    setTimeout(function () { nodes.announcer.textContent = text; }, 50);
  }

  AudD.setTheme('dark');

  // ------------------------------------------------------------ preview

  var EXAMPLE_SONGS = (function () {
    var now = Date.now();
    var min = 60000;
    return [
      { title: 'Paper Planes at Midnight', artist: 'Kaia Moreno', cover: 'images/example-1.svg', playedAt: now - 1 * min },
      { title: 'Tidal (Extended Mix)', artist: 'Neon Harbor', cover: 'images/example-2.svg', playedAt: now - 5 * min },
      { title: 'Orbit', artist: 'The Lanterns', cover: 'images/example-3.svg', playedAt: now - 9 * min },
      { title: 'Slow Static', artist: 'Juno Vale', cover: null, playedAt: now - 13 * min },
      { title: 'Afterglow', artist: 'Mira & The Echo', cover: null, playedAt: now - 17 * min }
    ].map(function (s, i) {
      s.link = null;
      s.album = '';
      s.playLength = 0;
      s.knowsMode = true;
      s.key = 'example-' + i;
      return s;
    });
  })();

  var preview = AudD.createView(nodes.preview, 'panel', { example: true, announce: false, headingLevel: 3 });
  preview.update({ phase: 'ready', songs: EXAMPLE_SONGS });
  var previewFeed = null;

  function showLivePreview() {
    if (previewFeed || !auth) return;
    preview.setExample(false);
    previewFeed = AudD.createFeed({ channel: auth.channelId, onChange: preview.update });
    preview.update(previewFeed.state);
    previewFeed.start();
    nodes.previewCaption.textContent = 'Your panel, live: songs appear here as AudD recognizes them on your stream.';
  }
  nodes.previewCaption.textContent = 'What viewers see in the panel below your video.';

  // ------------------------------------------------------------- render

  function setStep(node, state) { node.setAttribute('data-state', state); }

  function alertBox(code) {
    return el('div', { class: 'alert', role: 'alert' }, [icon('alert'), el('p', { text: message(code) })]);
  }

  function render() {
    renderAccount();
    renderRecognition();
    renderChannel();
    renderWidget();
  }

  function renderAccount() {
    var box = nodes.account;
    box.textContent = '';
    var connected = model.account === 'connected';
    setStep(nodes.stepAccount, connected ? 'done' : 'current');

    if (model.account === 'loading') {
      box.appendChild(el('p', { class: 'status-line' }, [AudD.spinner(), 'Checking your AudD account…']));
      return;
    }
    if (model.account === 'error') {
      box.appendChild(alertBox(model.error ? model.error.code : 'network'));
      box.appendChild(el('div', { class: 'actions' }, [
        el('button', { class: 'button', type: 'button', on: { click: loadAccount } }, [icon('retry'), 'Try again'])
      ]));
      return;
    }
    if (connected) {
      box.appendChild(el('p', { class: 'ok-line' }, [icon('check'), 'Your AudD account is connected to this channel.']));
      box.appendChild(el('p', {}, [el('button', {
        class: 'link-button', type: 'button',
        on: { click: function () { stopAwaitingPayment(); model.account = 'none'; model.switching = true; model.connect = 'choose'; model.error = null; retrySession(); render(); } }
      }, 'Connect a different AudD account')]));
      return;
    }

    if (model.connect === 'signin' && model.signin) {
      box.appendChild(signinPanel());
      return;
    }
    if (model.connect === 'token') {
      box.appendChild(tokenForm());
      return;
    }

    box.appendChild(el('p', { text: 'AudD recognizes the songs. Sign in to link your AudD account to this channel.' }));
    var busy = !!model.busy;
    var signInLink = el('a', {
      class: 'button button--primary button--signin', id: 'signin-link', target: '_blank', rel: 'noopener',
      on: { click: signInOpened, auxclick: signInOpened }
    }, ['Sign in with AudD', icon('open')]);
    updateSignInLink(signInLink);
    box.appendChild(el('div', { class: 'actions' }, [signInLink]));
    var errorCode = model.error && model.error.where === 'account' ? model.error.code : null;
    if (errorCode) box.appendChild(alertBox(errorCode));
    if (model.session.failed && model.session.failed !== errorCode) box.appendChild(alertBox(model.session.failed));
    if (model.switching) {
      box.appendChild(el('p', {}, [el('button', {
        class: 'link-button', type: 'button', disabled: busy,
        on: { click: function () { model.switching = false; loadAccount(); } }
      }, 'Keep the account that\u2019s connected now')]));
    }
    box.appendChild(el('ul', { class: 'alt-paths' }, [
      el('li', {}, ['New to AudD? ', model.busy === 'signup'
        ? el('span', { class: 'status-line status-line--inline' }, [AudD.spinner(), 'Creating your account…'])
        : el('button', { class: 'link-button', type: 'button', disabled: busy, on: { click: signUp } }, 'Create an AudD account')]),
      el('li', {}, ['Have an API token? ', el('button', {
        class: 'link-button', type: 'button', disabled: busy,
        on: { click: function () { model.connect = 'token'; model.error = null; render(); focusLater('#api-token'); } }
      }, 'Paste it instead')])
    ]));
    // The link needs a started session: start one now if there is none.
    prepareSession();
  }

  function tokenForm() {
    var error = model.error && model.error.where === 'token' ? model.error.code : null;
    // Hidden like a password: streamers often set up on camera.
    var input = el('input', {
      class: 'input', id: 'api-token', name: 'api-token', type: 'password', autocomplete: 'off', spellcheck: 'false',
      autocapitalize: 'off', 'aria-describedby': 'api-token-hint' + (error ? ' api-token-error' : ''),
      'aria-invalid': error ? 'true' : null
    });
    if (model.tokenDraft) input.value = model.tokenDraft;
    input.addEventListener('input', function () { model.tokenDraft = input.value; });
    var reveal = el('button', { class: 'button button--small', type: 'button', 'aria-controls': 'api-token', 'aria-pressed': 'false' }, 'Show');
    reveal.addEventListener('click', function () {
      var show = input.type === 'password';
      input.type = show ? 'text' : 'password';
      reveal.textContent = show ? 'Hide' : 'Show';
      reveal.setAttribute('aria-pressed', show ? 'true' : 'false');
    });
    var form = el('form', { class: 'token-form', novalidate: true }, [
      model.error && model.error.where === 'account' ? alertBox(model.error.code) : null,
      el('label', { for: 'api-token', text: 'API token' }),
      el('div', { class: 'token-field' }, [input, reveal]),
      error ? el('p', { class: 'field-error', id: 'api-token-error', role: 'alert', text: message(error) }) : null,
      el('p', { class: 'hint', id: 'api-token-hint' }, ['It’s on the main page of ', el('a', { href: E.dashboard, target: '_blank', rel: 'noopener noreferrer' }, 'dashboard.audd.io'), '.']),
      el('div', { class: 'actions' }, [
        model.busy === 'token'
          ? el('span', { class: 'status-line' }, [AudD.spinner(), 'Connecting…'])
          : el('button', { class: 'button button--primary', type: 'submit' }, 'Connect'),
        el('button', {
          class: 'button', type: 'button', disabled: model.busy === 'token',
          on: { click: function () { model.connect = 'choose'; model.error = null; retrySession(); render(); } }
        }, [icon('back'), 'Back'])
      ])
    ]);
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      saveToken(input.value.trim(), 'token');
    });
    return form;
  }

  function signinPanel() {
    var s = model.signin;
    var panel = el('div', { class: 'signin' });
    if (s.phase === 'linking') {
      panel.appendChild(el('p', { class: 'status-line' }, [AudD.spinner(), 'Approved. Linking your AudD account to this channel…']));
      return panel;
    }
    panel.appendChild(el('p', { class: 'signin__lead', id: 'signin-lead', tabindex: '-1', text: 'Finish in the AudD tab that opened:' }));
    panel.appendChild(el('ol', { class: 'signin__steps' }, [
      el('li', { text: 'Sign in to AudD if it asks, then approve.' }),
      el('li', {}, ['Check that the channel is yours, then click ', el('strong', { text: 'Link account' }), '.'])
    ]));
    panel.appendChild(el('p', { class: 'signin__text', text: 'This page continues on its own after that.' }));
    panel.appendChild(el('p', { class: 'signin__text' }, [
      'Don’t see the AudD tab? ',
      el('a', { href: signInUrl(s.session), target: '_blank', rel: 'noopener' }, ['Open the AudD page again', icon('open')])
    ]));
    panel.appendChild(el('div', { class: 'signin__wait' }, [
      el('span', { class: 'status-line', role: 'status' }, [AudD.spinner(), 'Waiting for you to link your account…']),
      el('button', { class: 'button button--small', type: 'button', on: { click: cancelSignIn } }, 'Cancel')
    ]));
    return panel;
  }

  function renderRecognition() {
    var box = nodes.recognition;
    box.textContent = '';
    if (model.account !== 'connected') {
      setStep(nodes.stepRecognition, 'todo');
      box.appendChild(el('p', { text: 'Music recognition for your stream costs ' + PRICE + ' per month. You can turn it on once your account is connected.' }));
      return;
    }
    var a = model.info || {};
    var link = AudD.paymentLink(a);
    var payButton = function (label) {
      if (!link) return el('p', { text: 'Email hello@audd.io to turn it on.' });
      return el('a', {
        class: 'button button--primary', href: link, target: '_blank', rel: 'noopener noreferrer',
        on: { click: startAwaitingPayment }
      }, [label, icon('open')]);
    };

    if (model.recognition === 'active') {
      setStep(nodes.stepRecognition, a.suggestRenewal ? 'attention' : 'done');
      box.appendChild(el('p', { class: 'ok-line' }, [a.suggestRenewal ? null : icon('check'),
        a.tillText ? 'Music recognition is on until ' + a.tillText + '.' : 'Music recognition is on.']));
      if (model.justPaid) box.appendChild(el('p', { text: 'Payment received. Thank you!' }));
      if (a.suggestRenewal) {
        box.appendChild(el('p', { text: 'It ends soon. Renew to keep showing songs without a gap.' }));
        box.appendChild(el('div', { class: 'actions' }, [payButton('Renew: ' + PRICE + ' per month')]));
      }
    } else {
      setStep(nodes.stepRecognition, 'current');
      if (model.recognition === 'expired') {
        box.appendChild(el('p', {}, [
          'Music recognition ended' + (a.tillText ? ' on ' : ''), a.tillText ? el('strong', { text: a.tillText }) : null,
          '. Viewers still see the songs from before, but new ones aren’t recognized.'
        ]));
        box.appendChild(el('div', { class: 'actions' }, [payButton('Renew: ' + PRICE + ' per month')]));
      } else {
        box.appendChild(el('p', { text: 'AudD listens to your stream and recognizes the music while you’re live. It costs ' + PRICE + ' per month for your stream.' }));
        box.appendChild(el('div', { class: 'actions' }, [payButton('Turn on: ' + PRICE + ' per month')]));
        box.appendChild(el('p', { class: 'hint' }, ['Want to test it for free first? Email ', el('a', { href: 'mailto:hello@audd.io', target: '_blank', rel: 'noopener' }, 'hello@audd.io'), '.']));
      }
    }
    if (model.awaitingPayment && model.recognition !== 'active') {
      box.appendChild(el('div', { class: 'notice', role: 'status' }, [
        el('p', { class: 'status-line' }, [AudD.spinner(), 'Finish the payment in the new tab. This page checks for it on its own.']),
        el('div', { class: 'actions' }, [el('button', { class: 'button button--small', type: 'button', on: { click: function () { checkAccount(true); } } }, 'Check now')])
      ]));
    }
    if (model.error && model.error.where === 'recognition') box.appendChild(alertBox(model.error.code));
  }

  function renderChannel() {
    var box = nodes.channel;
    box.textContent = '';
    var ready = model.account === 'connected' && model.recognition === 'active';
    setStep(nodes.stepChannel, ready ? 'current' : 'todo');
    if (!ready) {
      box.appendChild(el('p', { text: 'Then activate the extension as a panel below your video or a component over it.' }));
      return;
    }
    box.appendChild(el('p', { text: 'Go back to My Extensions, click Activate on AudD Music Recognition, and choose where it goes:' }));
    box.appendChild(el('ul', { class: 'places' }, [
      el('li', {}, [el('strong', { text: 'Panel' }), 'Below your video: the song playing now and the songs before it.']),
      el('li', {}, [el('strong', { text: 'Component' }), 'Over your video: the song playing now.'])
    ]));
    box.appendChild(el('p', { text: 'Each recognized song is also posted to your chat.' }));
  }

  function widgetUrl() {
    var id = (model.info && model.info.twitchId) || (auth && auth.channelId) || '';
    var url = E.widget + '?ch=' + encodeURIComponent(id);
    if (model.widget.history) url += '&history';
    if (model.widget.background) url += '&background';
    if (model.widget.dark) url += '&black-font';
    return url;
  }

  function renderWidget() {
    var show = model.account === 'connected';
    nodes.obs.hidden = !show;
    if (show) nodes.widgetUrl.value = widgetUrl();
  }

  // Copy with the Clipboard API, falling back to selecting the text.
  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text).catch(function () { return legacyCopy(text); });
    }
    return legacyCopy(text);
  }

  function legacyCopy(text) {
    return new Promise(function (resolve, reject) {
      var area = el('textarea', { class: 'visually-hidden', readonly: true });
      area.value = text;
      document.body.appendChild(area);
      area.select();
      var ok = false;
      try { ok = document.execCommand('copy'); } catch (e) { ok = false; }
      document.body.removeChild(area);
      if (ok) resolve(); else reject(new Error('copy failed'));
    });
  }

  function setCopyLabel(button, label, done) {
    button.textContent = '';
    button.appendChild(icon(done ? 'check' : 'copy'));
    button.appendChild(document.createTextNode(label));
  }

  setCopyLabel(nodes.widgetCopy, 'Copy', false);
  nodes.widgetCopy.addEventListener('click', function () {
    copyText(nodes.widgetUrl.value).then(function () {
      setCopyLabel(nodes.widgetCopy, 'Copied', true);
      setTimeout(function () { setCopyLabel(nodes.widgetCopy, 'Copy', false); }, 2000);
    }).catch(function () {
      nodes.widgetUrl.focus();
      nodes.widgetUrl.select();
    });
  });
  nodes.widgetUrl.addEventListener('focus', function () { nodes.widgetUrl.select(); });
  [['opt-history', 'history'], ['opt-background', 'background'], ['opt-dark', 'dark']].forEach(function (pair) {
    document.getElementById(pair[0]).addEventListener('change', function (e) {
      model.widget[pair[1]] = e.target.checked;
      renderWidget();
    });
  });

  function focusLater(selector) {
    setTimeout(function () {
      var node = document.querySelector(selector);
      if (node) node.focus();
    }, 0);
  }

  // ------------------------------------------------------------ account

  // Applies a twitch.php answer (get_user, signup or save_user).
  function applyAccount(res, where) {
    var s = AudD.accountState(res);
    if (s.state === 'none') {
      model.account = 'none';
      return;
    }
    if (s.state === 'error') {
      model.error = { code: s.code, where: where };
      if (where === 'load') model.account = 'error';
      return;
    }
    if (model.account !== 'connected' && where !== 'load') announce('Your AudD account is connected to this channel.');
    model.account = 'connected';
    model.switching = false;
    model.recognition = s.state;
    model.info = s.account;
    model.connect = 'choose';
    model.tokenDraft = '';
    model.error = null;
    // Paid: recognition turned on, or its end date moved (a renewal).
    var from = model.paymentFrom || {};
    if (model.awaitingPayment && s.state === 'active' && (from.state !== 'active' || (s.account.tillText && s.account.tillText !== from.till))) {
      model.justPaid = true;
      stopAwaitingPayment();
      announce('Payment received. Music recognition is on.');
    }
    showLivePreview();
  }

  function loadAccount() {
    if (!auth) {
      // Twitch never authorized this page; only a reload can fix that.
      window.location.reload();
      return;
    }
    model.account = 'loading';
    model.error = null;
    render();
    AudD.twitchPhp('get_user', { jwt: auth.token }).then(function (res) {
      applyAccount(res, 'load');
      render();
    }).catch(function () {
      model.account = 'error';
      model.error = { code: 'network', where: 'load' };
      render();
    });
  }

  var checking = false;
  // Checks for a payment. It leaves the page alone while the streamer is
  // connecting an account, and redraws only when something changed.
  function checkAccount(showErrors) {
    if (checking || !auth || model.account !== 'connected' || model.switching) return;
    checking = true;
    AudD.twitchPhp('get_user', { jwt: auth.token }).then(function (res) {
      if (model.account !== 'connected' || model.switching) return;
      var before = JSON.stringify([model.account, model.recognition, model.info, model.awaitingPayment, model.error]);
      applyAccount(res, showErrors ? 'recognition' : 'quiet');
      if (showErrors || JSON.stringify([model.account, model.recognition, model.info, model.awaitingPayment, model.error]) !== before) render();
    }).catch(function () {
      if (showErrors) { model.error = { code: 'network', where: 'recognition' }; render(); }
    }).finally(function () { checking = false; });
  }

  function signUp() {
    model.busy = 'signup';
    model.error = null;
    render();
    AudD.twitchPhp('signup', { jwt: auth.token }).then(function (res) {
      model.busy = null;
      applyAccount(res, 'account');
      render();
    }).catch(function () {
      model.busy = null;
      model.error = { code: 'network', where: 'account' };
      render();
    });
  }

  // source: 'token' (pasted) or 'signin'
  function saveToken(token, source) {
    if (source === 'token' && !token) {
      model.error = { code: 'no_token', where: 'token' };
      render();
      focusLater('#api-token');
      return Promise.resolve();
    }
    if (source === 'token') { model.busy = 'token'; model.error = null; render(); }
    return AudD.twitchPhp('save_user', { jwt: auth.token, api_token: token }).then(function (res) {
      model.busy = null;
      if (!res.ok && (res.code === 'wrong_token' || res.code === 'no_token') && source === 'token') {
        model.error = { code: res.code, where: 'token' };
        render();
        focusLater('#api-token');
        return;
      }
      applyAccount(res, 'account');
      if (source === 'signin') {
        model.signin = null;
        if (model.account !== 'connected') model.connect = 'choose';
      }
      render();
    }).catch(function () {
      model.busy = null;
      model.error = { code: 'network', where: 'account' };
      if (source === 'signin') { model.connect = 'choose'; model.signin = null; }
      render();
    });
  }

  // While a payment tab is open, check for the payment when the streamer
  // comes back to this page, and every 15 seconds for up to 30 minutes.
  var paymentTimer = null;
  function startAwaitingPayment() {
    model.awaitingPayment = true;
    model.justPaid = false;
    model.paymentFrom = { till: model.info ? model.info.tillText : '', state: model.recognition };
    var started = Date.now();
    clearInterval(paymentTimer);
    paymentTimer = setInterval(function () {
      if (!model.awaitingPayment || Date.now() - started > 30 * 60000) {
        clearInterval(paymentTimer);
        if (model.awaitingPayment) { model.awaitingPayment = false; render(); }
        return;
      }
      checkAccount(false);
    }, 15000);
    setTimeout(render, 0);
  }

  function stopAwaitingPayment() {
    model.awaitingPayment = false;
    clearInterval(paymentTimer);
  }
  window.addEventListener('focus', function () { if (model.awaitingPayment) checkAccount(false); });
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible' && model.awaitingPayment) checkAccount(false);
  });

  // ---------------------------------------------------- Sign in with AudD
  // The extension backend runs the sign-in. The page makes a random secret
  // and registers its SHA-256, the session, with signIn/start/. "Sign in with
  // AudD" is then a plain link to signIn/open/?session=..., ready before the
  // click so that no popup blocker stops it. The backend sends that tab to
  // AudD's approval page, then asks the streamer to confirm the channel.
  // Meanwhile the page asks signIn/result/ with the secret, which only it
  // knows, and links the API token it gets the same way as a pasted one.
  // Each session is used once.

  var POLL_EVERY = 2000;
  var SIGNIN_LIMIT = 10 * 60000;   // how long a sign-in may take after the click
  var SESSION_TTL = 10 * 60000;    // how long a started session lasts
  var SESSION_RENEW = 9 * 60000;   // an unused session is replaced before it runs out
  var RETRY_AFTER = 30000;         // a failed start is tried again on return, no sooner than this
  var MAX_FAILURES = 3;            // the result unreachable this many times in a row: unavailable
  var sessionRun = 0;
  var signinRun = 0;
  var pollTimer = null;

  function hex(bytes) {
    var out = '';
    for (var i = 0; i < bytes.length; i++) out += (bytes[i] < 16 ? '0' : '') + bytes[i].toString(16);
    return out;
  }

  // A secret (32 random bytes as hex) and its SHA-256 (hex), the session.
  function makeSession() {
    return Promise.resolve().then(function () {
      var bytes = new Uint8Array(32);
      window.crypto.getRandomValues(bytes);
      var secret = hex(bytes);
      return window.crypto.subtle.digest('SHA-256', new TextEncoder().encode(secret)).then(function (hash) {
        return { secret: secret, session: hex(new Uint8Array(hash)) };
      });
    });
  }

  function signInUrl(session) {
    return E.lastSong + 'signIn/open/?session=' + session;
  }

  function showingSignInLink() {
    return !!document.getElementById('signin-link');
  }

  // Gives the "Sign in with AudD" link a started session with time left.
  // After a failed start it does nothing unless retry is set; the message
  // about the failure stays up until a retry succeeds.
  function prepareSession(retry) {
    var s = model.session;
    if (s.starting || !auth || (s.failed && !retry)) return;
    var age = s.ready ? Date.now() - s.ready.startedAt : Infinity;
    if (age < SESSION_RENEW) return;
    if (s.ready && age >= SESSION_TTL) {
      s.ready = null;
      updateSignInLink();
    }
    s.starting = true;
    var run = ++sessionRun;
    var startedAt = Date.now();
    var made = null;
    makeSession().then(function (m) {
      made = m;
      return AudD.request(E.lastSong + 'signIn/start/', { form: { jwt: auth.token, session: m.session } });
    }).then(function (r) {
      if (run !== sessionRun) return;
      s.starting = false;
      if (r.status === 200 && r.data && typeof r.data === 'object') { // {"status": "pending"}
        var wasFailed = !!s.failed;
        made.startedAt = startedAt;
        s.ready = made;
        s.failed = null;
        if (wasFailed && showingSignInLink()) renderAccount();
        else updateSignInLink();
        return;
      }
      var code = r.data && typeof r.data.error === 'string' ? r.data.error : '';
      // 404: a backend without the sign-in; 429: too many sign-ins for now.
      sessionFailed(r.status === 404 || r.status === 429 || r.status >= 500 || !code ? 'signin_unavailable' : code);
    }).catch(function () {
      if (run !== sessionRun) return;
      s.starting = false;
      sessionFailed('signin_unavailable');
    });
  }

  function sessionFailed(code) {
    var s = model.session;
    var changed = s.failed !== code;
    s.ready = null;
    s.failed = code;
    s.failedAt = Date.now();
    if (showingSignInLink()) {
      if (changed) renderAccount();
      else updateSignInLink();
    }
  }

  // The streamer chose to see the sign-in again (Back, another account).
  function retrySession() {
    if (model.session.failed) prepareSession(true);
  }

  // Points the link at the ready session, or makes it inert: it looks the
  // same while a session starts (a short wait), and dimmed while signing in
  // can't start.
  function updateSignInLink(a) {
    a = a || document.getElementById('signin-link');
    if (!a) return;
    var s = model.session;
    if (s.ready && !model.busy) {
      a.setAttribute('href', signInUrl(s.ready.session));
      a.removeAttribute('role');
      a.removeAttribute('aria-disabled');
      a.classList.remove('button--waiting');
    } else {
      a.removeAttribute('href');
      a.setAttribute('role', 'link');
      a.setAttribute('aria-disabled', 'true');
      a.classList.toggle('button--waiting', !model.busy && !s.failed);
    }
  }

  // The streamer opened the link: wait for the result.
  function signInOpened(e) {
    if (e.type === 'auxclick' && e.button !== 1) return;
    var ready = model.session.ready;
    if (!ready || model.busy || model.connect !== 'choose') return;
    model.session.ready = null; // used once
    var run = ++signinRun;
    model.connect = 'signin';
    model.error = null;
    model.signin = {
      phase: 'waiting', session: ready.session, secret: ready.secret,
      until: Date.now() + SIGNIN_LIMIT, failures: 0, polling: false
    };
    // Redraw once the browser has followed the link: a link taken out of the
    // page during its own click doesn't open.
    setTimeout(function () {
      if (run !== signinRun) return;
      render();
      focusLater('#signin-lead');
      announce('Finish in the AudD tab: approve the sign-in, then click Link account. This page continues on its own.');
    }, 0);
    pollLater(run);
  }

  function pollLater(run) {
    clearTimeout(pollTimer);
    pollTimer = setTimeout(function () { pollResult(run); }, POLL_EVERY);
  }

  function pollResult(run) {
    var s = model.signin;
    if (run !== signinRun || !s || s.phase !== 'waiting' || s.polling) return;
    if (Date.now() > s.until) {
      signInFailed('signin_timeout');
      return;
    }
    s.polling = true;
    AudD.request(E.lastSong + 'signIn/result/', { form: { jwt: auth.token, secret: s.secret } }).then(function (r) {
      s.polling = false;
      if (run !== signinRun) return;
      var d = r.data && typeof r.data === 'object' ? r.data : null;
      var error = d && typeof d.error === 'string' && d.error ? d.error : '';
      if (r.status === 200 && d) {
        if (d.status === 'done') {
          if (typeof d.api_token === 'string' && d.api_token) linkAccount(d.api_token);
          else signInFailed('internal_error');
        } else if (d.status === 'declined') {
          signInFailed('signin_declined');
        } else if (d.status === 'failed') {
          signInFailed(error || 'internal_error');
        } else {
          // "unknown" (the AudD tab hasn't reached the backend yet) or "pending"
          s.failures = 0;
          pollLater(run);
        }
        return;
      }
      // 404: a backend without the sign-in.
      if (r.status === 404) signInFailed('signin_unavailable');
      else if (r.status >= 500 || !error) pollFailed(run);
      else signInFailed(error); // bad_request, wrong_jwt, not_broadcaster
    }).catch(function () {
      s.polling = false;
      if (run === signinRun) pollFailed(run);
    });
  }

  // The backend didn't answer: a few times in a row means it can't now.
  function pollFailed(run) {
    if (++model.signin.failures >= MAX_FAILURES) signInFailed('signin_unavailable');
    else pollLater(run);
  }

  function linkAccount(apiToken) {
    signinRun++;
    clearTimeout(pollTimer);
    model.signin = { phase: 'linking' };
    render();
    saveToken(apiToken, 'signin');
  }

  function signInFailed(code) {
    signinRun++;
    clearTimeout(pollTimer);
    model.signin = null;
    model.connect = code === 'signin_unavailable' ? 'token' : 'choose';
    model.error = { code: code, where: 'account' };
    render();
  }

  function cancelSignIn() {
    signinRun++;
    clearTimeout(pollTimer);
    model.signin = null;
    model.connect = 'choose';
    model.error = null;
    render();
  }

  // Coming back from the AudD tab: ask for the result at once. On the first
  // step, renew a link that is running out and retry a failed start.
  function onReturn() {
    var s = model.signin;
    if (s && s.phase === 'waiting' && !s.polling) pollResult(signinRun);
    if (showingSignInLink()) prepareSession(!!model.session.failed && Date.now() - model.session.failedAt >= RETRY_AFTER);
  }
  window.addEventListener('focus', onReturn);
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible') onReturn();
  });
  setInterval(function () { if (showingSignInLink()) prepareSession(); }, 30000);

  // ------------------------------------------------------------- start

  render();

  if (!ext) {
    model.account = 'error';
    model.error = { code: 'no_jwt', where: 'load' };
    render();
    return;
  }

  ext.onContext(function (context) {
    if (context && context.theme) AudD.setTheme(context.theme);
  });

  var listening = false;
  ext.onAuthorized(function (a) {
    var first = !auth;
    auth = a;
    if (first) loadAccount();
    if (!listening) {
      listening = true;
      ext.listen('broadcast', function (target, contentType, msg) {
        if (previewFeed) previewFeed.pushMessage(msg);
      });
    }
  });
})();
