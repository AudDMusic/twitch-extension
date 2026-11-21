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

  var DEVICE_GRANT = 'urn:ietf:params:oauth:grant-type:device_code';
  var PRICE = '$45';

  // What went wrong, why, and what to do. Keys are the error codes of
  // twitch.php, the AudD sign-in service, and /lastSong/auddApiToken/.
  var MESSAGES = {
    network: 'Can’t reach AudD. Check your connection, then try again.',
    internal_error: 'Something went wrong on AudD’s side. Try again in a minute; if it keeps happening, email hello@audd.io.',
    no_jwt: 'Twitch didn’t pass your channel details to AudD. Reload this page and try again; if it keeps happening, email hello@audd.io.',
    wrong_jwt: 'AudD couldn’t confirm your Twitch session. Reload this page and try again; if it keeps happening, email hello@audd.io.',
    not_broadcaster: 'Only the channel’s broadcaster can connect an AudD account here.',
    no_token: 'Paste your API token first.',
    wrong_token: 'No AudD account has this API token. Copy it again from dashboard.audd.io; if it still doesn’t work, email api@audd.io.',
    signin_unavailable: 'Signing in with AudD isn’t available right now. Paste your API token instead: it’s on dashboard.audd.io.',
    rate_limited: 'Too many sign-in attempts from your network. Wait a few minutes, then try again.',
    access_denied: 'The sign-in was declined on the AudD page. Start again when you’re ready.',
    expired_token: 'The code expired before it was approved. Start again to get a new code.',
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
    signin: null,            // device flow, see startSignIn
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
    previewCaption: document.getElementById('preview-caption')
  };

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
      s.key = 'example-' + i;
      return s;
    });
  })();

  var preview = AudD.createView(nodes.preview, 'panel', { example: true });
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
        on: { click: function () { model.account = 'none'; model.connect = 'choose'; model.error = null; render(); } }
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
    box.appendChild(el('div', { class: 'actions' }, [
      el('button', { class: 'button button--primary button--signin', type: 'button', disabled: busy, on: { click: startSignIn } }, 'Sign in with AudD')
    ]));
    if (model.error && model.error.where === 'account') box.appendChild(alertBox(model.error.code));
    box.appendChild(el('ul', { class: 'alt-paths' }, [
      el('li', {}, ['New to AudD? ', model.busy === 'signup'
        ? el('span', { class: 'status-line status-line--inline' }, [AudD.spinner(), 'Creating your account…'])
        : el('button', { class: 'link-button', type: 'button', disabled: busy, on: { click: signUp } }, 'Create an AudD account for this channel')]),
      el('li', {}, ['Have an API token? ', el('button', {
        class: 'link-button', type: 'button', disabled: busy,
        on: { click: function () { model.connect = 'token'; model.error = null; render(); focusLater('#api-token'); } }
      }, 'Paste it instead')])
    ]));
  }

  function tokenForm() {
    var error = model.error && model.error.where === 'token' ? model.error.code : null;
    var input = el('input', {
      class: 'input', id: 'api-token', name: 'api-token', type: 'text', autocomplete: 'off', spellcheck: 'false',
      autocapitalize: 'off', 'aria-describedby': 'api-token-hint' + (error ? ' api-token-error' : ''),
      'aria-invalid': error ? 'true' : null
    });
    if (model.tokenDraft) input.value = model.tokenDraft;
    input.addEventListener('input', function () { model.tokenDraft = input.value; });
    var form = el('form', { class: 'token-form', novalidate: true }, [
      el('label', { for: 'api-token', text: 'API token' }),
      input,
      error ? el('p', { class: 'field-error', id: 'api-token-error', role: 'alert', text: message(error) }) : null,
      el('p', { class: 'hint', id: 'api-token-hint' }, ['It’s on the main page of ', el('a', { href: E.dashboard, target: '_blank', rel: 'noopener noreferrer' }, 'dashboard.audd.io'), '.']),
      el('div', { class: 'actions' }, [
        model.busy === 'token'
          ? el('span', { class: 'status-line' }, [AudD.spinner(), 'Connecting…'])
          : el('button', { class: 'button button--primary', type: 'submit' }, 'Connect'),
        el('button', {
          class: 'button', type: 'button', disabled: model.busy === 'token',
          on: { click: function () { model.connect = 'choose'; model.error = null; render(); } }
        }, [icon('back'), 'Back'])
      ])
    ]);
    if (model.error && model.error.where === 'account') form.appendChild(alertBox(model.error.code));
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      saveToken(input.value.trim(), 'token');
    });
    return form;
  }

  function signinPanel() {
    var s = model.signin;
    var panel = el('div', { class: 'signin' });
    if (s.phase === 'starting') {
      panel.appendChild(el('p', { class: 'status-line' }, [AudD.spinner(), 'Getting a sign-in code…']));
      return panel;
    }
    if (s.phase === 'linking') {
      panel.appendChild(el('p', { class: 'status-line' }, [AudD.spinner(), 'Approved. Linking your AudD account to this channel…']));
      return panel;
    }
    var open = s.uriComplete || s.uri;
    var openLabel = 'Open the AudD sign-in page';
    panel.appendChild(el('ol', { class: 'signin__steps' }, [
      el('li', {}, [
        'Sign in to AudD in a new tab.',
        el('div', {}, [el('a', { class: 'button button--primary', href: open, target: '_blank', rel: 'noopener noreferrer' }, [openLabel, icon('open')])])
      ]),
      el('li', {}, [
        s.uriComplete ? 'Check that the page shows this code, then approve:' : 'Enter this code there, then approve:',
        el('div', { class: 'code' }, [
          el('span', { class: 'code__value', 'aria-label': 'Code: ' + s.userCode.split('').join(' '), text: s.userCode }),
          copyButton(function () { return s.userCode; }, 'Copy code')
        ])
      ])
    ]));
    var expires = el('span', { class: 'signin__expiry' });
    updateExpiry(expires);
    panel.appendChild(el('div', { class: 'signin__wait' }, [
      el('span', { class: 'status-line', role: 'status' }, [AudD.spinner(), el('span', {}, ['Waiting for you to approve. ', expires])]),
      el('button', { class: 'button button--small', type: 'button', on: { click: cancelSignIn } }, 'Cancel')
    ]));
    return panel;
  }

  function updateExpiry(node) {
    if (!model.signin || !model.signin.expiresAt) return;
    var min = Math.max(1, Math.ceil((model.signin.expiresAt - Date.now()) / 60000));
    node.textContent = 'The code works for ' + min + (min === 1 ? ' more minute.' : ' more minutes.');
  }
  setInterval(function () {
    var node = nodes.account.querySelector('.signin__expiry');
    if (node) updateExpiry(node);
  }, 15000);

  function renderRecognition() {
    var box = nodes.recognition;
    box.textContent = '';
    if (model.account !== 'connected') {
      setStep(nodes.stepRecognition, 'todo');
      box.appendChild(el('p', { text: 'It costs ' + PRICE + ' per month for your stream. You can turn it on once your account is connected.' }));
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
      box.appendChild(el('p', { class: 'ok-line' }, [icon('check'),
        a.tillText ? 'Music recognition is on until ' + a.tillText + '.' : 'Music recognition is on.']));
      if (model.justPaid) box.appendChild(el('p', { text: 'Payment received. Thank you!' }));
      if (a.suggestRenewal) {
        box.appendChild(el('p', { text: 'It ends soon. Renew to keep showing songs without a gap: ' + PRICE + ' for another month.' }));
        box.appendChild(el('div', { class: 'actions' }, [payButton('Renew for ' + PRICE)]));
      }
    } else {
      setStep(nodes.stepRecognition, 'current');
      if (model.recognition === 'expired') {
        box.appendChild(el('p', {}, [
          'Music recognition ended' + (a.tillText ? ' on ' : ''), a.tillText ? el('strong', { text: a.tillText }) : null,
          '. Viewers still see the songs from before, but new ones aren’t recognized. Renew for one month: ' + PRICE + '.'
        ]));
        box.appendChild(el('div', { class: 'actions' }, [payButton('Renew for ' + PRICE)]));
      } else {
        box.appendChild(el('p', { text: 'AudD listens to your stream and recognizes the music while you’re live: ' + PRICE + ' for one month.' }));
        box.appendChild(el('div', { class: 'actions' }, [payButton('Turn on for ' + PRICE)]));
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
      el('li', {}, [el('strong', { text: 'Component' }), 'Over your video: the song playing now. Viewers can hide it.'])
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

  function copyButton(getText, label) {
    var button = el('button', { class: 'button button--small', type: 'button' });
    setCopyLabel(button, label, false);
    button.addEventListener('click', function () {
      copyText(getText()).then(function () {
        setCopyLabel(button, 'Copied', true);
        setTimeout(function () { setCopyLabel(button, label, false); }, 2000);
      }).catch(function () {
        setCopyLabel(button, 'Select and copy it', false);
      });
    });
    return button;
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
    var wasActive = model.recognition === 'active';
    model.account = 'connected';
    model.recognition = s.state;
    model.info = s.account;
    model.connect = 'choose';
    model.tokenDraft = '';
    model.error = null;
    if (s.state === 'active' && model.awaitingPayment && !wasActive) {
      model.justPaid = true;
      model.awaitingPayment = false;
    }
    showLivePreview();
  }

  function loadAccount() {
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
  function checkAccount(showErrors) {
    if (checking || !auth) return;
    checking = true;
    AudD.twitchPhp('get_user', { jwt: auth.token }).then(function (res) {
      applyAccount(res, showErrors ? 'recognition' : 'quiet');
      render();
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
      if (model.account !== 'connected' && source === 'signin') {
        model.connect = 'choose';
        model.signin = null;
      }
      render();
    }).catch(function () {
      model.busy = null;
      model.error = { code: 'network', where: source === 'token' ? 'token' : 'account' };
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
    var started = Date.now();
    clearInterval(paymentTimer);
    paymentTimer = setInterval(function () {
      if (!model.awaitingPayment || Date.now() - started > 30 * 60000) { clearInterval(paymentTimer); return; }
      checkAccount(false);
    }, 15000);
    setTimeout(render, 0);
  }
  window.addEventListener('focus', function () { if (model.awaitingPayment) checkAccount(false); });
  document.addEventListener('visibilitychange', function () {
    if (document.visibilityState === 'visible' && model.awaitingPayment) checkAccount(false);
  });

  // ---------------------------------------------------- Sign in with AudD
  // OAuth 2.0 device authorization grant (RFC 8628) against AudD's
  // authorization server, which answers this page's origin. The access token
  // only goes to the extension backend, which reads the API token from the
  // AudD MCP server; both tokens are revoked once the account is linked.

  var signinRun = 0;

  function oauthCode(r) {
    var code = r.data && typeof r.data.error === 'string' ? r.data.error : '';
    if (r.status === 429) return 'rate_limited';
    if (code === 'invalid_client' || code === 'unauthorized_client' || code === 'invalid_scope' || r.status === 404) return 'signin_unavailable';
    return code || 'internal_error';
  }

  function startSignIn() {
    var run = ++signinRun;
    model.connect = 'signin';
    model.error = null;
    model.signin = { phase: 'starting' };
    render();
    AudD.request(E.oauthDevice, {
      form: { client_id: E.oauthClientId, scope: E.oauthScope, resource: E.oauthResource }
    }).then(function (r) {
      if (run !== signinRun) return;
      var d = r.data || {};
      var uri = AudD.httpUrl(d.verification_uri);
      var uriComplete = AudD.httpUrl(d.verification_uri_complete);
      if (r.status !== 200 || !d.device_code || !d.user_code || !(uri || uriComplete)) {
        signInFailed(r.status === 200 ? 'internal_error' : oauthCode(r));
        return;
      }
      model.signin = {
        phase: 'waiting',
        deviceCode: String(d.device_code),
        userCode: AudD.str(d.user_code),
        uri: uri,
        uriComplete: uriComplete,
        expiresAt: Date.now() + (AudD.num(d.expires_in) || 900) * 1000,
        interval: Math.max(AudD.num(d.interval) || 5, 2)
      };
      render();
      pollLater(run);
    }).catch(function () {
      if (run === signinRun) signInFailed('network');
    });
  }

  function pollLater(run) {
    setTimeout(function () { pollToken(run); }, model.signin.interval * 1000);
  }

  function pollToken(run) {
    var s = model.signin;
    if (run !== signinRun || !s || s.phase !== 'waiting') return;
    if (Date.now() > s.expiresAt + 60000) { signInFailed('expired_token'); return; }
    AudD.request(E.oauthToken, {
      form: { grant_type: DEVICE_GRANT, device_code: s.deviceCode, client_id: E.oauthClientId }
    }).then(function (r) {
      if (run !== signinRun) return;
      var d = r.data || {};
      if (r.status === 200 && d.access_token) {
        link(run, String(d.access_token), d.refresh_token ? String(d.refresh_token) : null, typeof d.scope === 'string' ? d.scope : null);
        return;
      }
      var code = oauthCode(r);
      if (code === 'authorization_pending') { pollLater(run); return; }
      if (code === 'slow_down') { s.interval += 5; pollLater(run); return; }
      signInFailed(code);
    }).catch(function () {
      // The network or the server failed: keep trying until the code expires.
      if (run === signinRun) pollLater(run);
    });
  }

  function link(run, accessToken, refreshToken, scope) {
    model.signin.phase = 'linking';
    render();
    var done = function () { revoke(refreshToken, 'refresh_token'); revoke(accessToken, 'access_token'); };
    if (scope && scope.split(/\s+/).indexOf(E.oauthScope) < 0) {
      done();
      signInFailed('token_permission_missing');
      return;
    }
    AudD.request(E.lastSong + 'auddApiToken/', { form: { jwt: auth.token, access_token: accessToken }, timeout: 30000 }).then(function (r) {
      if (run !== signinRun) return;
      var d = r.data || {};
      if (r.status === 200 && typeof d.api_token === 'string' && d.api_token) {
        return saveToken(d.api_token, 'signin');
      }
      // A backend without this endpoint answers 404.
      signInFailed(r.status === 404 ? 'signin_unavailable' : (typeof d.error === 'string' && d.error) || 'internal_error');
    }).catch(function () {
      if (run === signinRun) signInFailed('network');
    }).finally(done);
  }

  // Ends the extension's access to the AudD account; failures don't matter.
  function revoke(token, hint) {
    if (!token) return;
    AudD.request(E.oauthRevoke, { form: { token: token, token_type_hint: hint, client_id: E.oauthClientId } }).catch(function () {});
  }

  function signInFailed(code) {
    signinRun++;
    model.signin = null;
    model.connect = code === 'signin_unavailable' ? 'token' : 'choose';
    model.error = { code: code, where: 'account' };
    render();
  }

  function cancelSignIn() {
    signinRun++;
    model.signin = null;
    model.connect = 'choose';
    model.error = null;
    render();
  }

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
