// AudD Music Recognition for Twitch: code shared by the panel, the video
// component, the hosted widget and the configuration page.
//
// Everything from the network is treated as untrusted: text goes into the
// page through textContent, links must be http(s), and missing or
// wrong-typed fields are skipped rather than trusted.
(function () {
  'use strict';

  var E = window.AUDD_ENDPOINTS || {};
  var AudD = (window.AudD = {});
  AudD.endpoints = E;
  AudD.version = '0.1.1';

  var params = new URLSearchParams(window.location.search);
  AudD.params = params;

  // ---------------------------------------------------------------- DOM

  function append(node, children) {
    if (children == null) return node;
    if (!Array.isArray(children)) children = [children];
    for (var i = 0; i < children.length; i++) {
      var c = children[i];
      if (c == null || c === false) continue;
      node.appendChild(typeof c === 'string' ? document.createTextNode(c) : c);
    }
    return node;
  }

  // el('a', {class: 'x', href: url, on: {click: fn}}, [children])
  function el(tag, props, children) {
    var node = document.createElement(tag);
    if (props) {
      Object.keys(props).forEach(function (k) {
        var v = props[k];
        if (v == null || v === false) return;
        if (k === 'class') node.className = v;
        else if (k === 'text') node.textContent = v;
        else if (k === 'on') Object.keys(v).forEach(function (ev) { node.addEventListener(ev, v[ev]); });
        else node.setAttribute(k, v === true ? '' : String(v));
      });
    }
    return append(node, children);
  }
  AudD.el = el;

  var SVG = 'http://www.w3.org/2000/svg';
  // Icons as element specs (no markup strings): 24 x 24, stroked.
  var ICONS = {
    note: [['path', { d: 'M9 18V5l11-2v13' }], ['circle', { cx: 6, cy: 18, r: 3 }], ['circle', { cx: 17, cy: 16, r: 3 }]],
    open: [['path', { d: 'M14 4h6v6' }], ['path', { d: 'M20 4l-9 9' }], ['path', { d: 'M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5' }]],
    check: [['path', { d: 'M5 12.5l4.5 4.5L19 7.5' }]],
    copy: [['rect', { x: 9, y: 9, width: 11, height: 11, rx: 2 }], ['path', { d: 'M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1' }]],
    alert: [['circle', { cx: 12, cy: 12, r: 9 }], ['path', { d: 'M12 7.5v5.5' }], ['path', { d: 'M12 16.5v.01' }]],
    retry: [['path', { d: 'M20 11a8 8 0 1 0-2.3 6.3' }], ['path', { d: 'M20 4v7h-7' }]],
    back: [['path', { d: 'M15 6l-6 6 6 6' }]],
    lock: [['rect', { x: 5, y: 11, width: 14, height: 9, rx: 2 }], ['path', { d: 'M8 11V8a4 4 0 0 1 8 0v3' }]]
  };

  function icon(name, cls) {
    var svg = document.createElementNS(SVG, 'svg');
    svg.setAttribute('viewBox', '0 0 24 24');
    svg.setAttribute('aria-hidden', 'true');
    svg.setAttribute('focusable', 'false');
    svg.setAttribute('class', 'icon' + (cls ? ' ' + cls : ''));
    (ICONS[name] || []).forEach(function (spec) {
      var part = document.createElementNS(SVG, spec[0]);
      Object.keys(spec[1]).forEach(function (k) { part.setAttribute(k, spec[1][k]); });
      svg.appendChild(part);
    });
    return svg;
  }
  AudD.icon = icon;

  // Three bars that move while a song is playing (still with reduced motion).
  function equalizer() {
    return el('span', { class: 'eq', 'aria-hidden': 'true' }, [el('span'), el('span'), el('span')]);
  }

  function spinner() {
    return el('span', { class: 'spinner', 'aria-hidden': 'true' });
  }
  AudD.spinner = spinner;

  // ---------------------------------------------------------- parsing

  function str(v) {
    if (typeof v === 'string') return v.replace(/\s+/g, ' ').trim();
    if (typeof v === 'number' && isFinite(v)) return String(v);
    return '';
  }
  AudD.str = str;

  function num(v) {
    if (typeof v === 'number' && isFinite(v)) return v;
    if (typeof v === 'string' && /^\s*-?\d+(\.\d+)?\s*$/.test(v)) return Number(v);
    return null;
  }
  AudD.num = num;

  // Returns an http(s) URL or null; anything else (javascript:, data:, junk)
  // is dropped.
  function httpUrl(v) {
    var s = str(v);
    if (!s) return null;
    try {
      var u = new URL(s);
      if (u.protocol === 'https:' || u.protocol === 'http:') return u.href;
    } catch (e) { /* not a URL */ }
    return null;
  }
  AudD.httpUrl = httpUrl;

  // "2026-11-21 20:15:07" in UTC (AudD stream callbacks) to milliseconds.
  function parseUtcText(s) {
    var m = /^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2})(?::(\d{2}))?/.exec(str(s));
    if (!m) return null;
    var t = Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5], +(m[6] || 0));
    return isFinite(t) ? t : null;
  }

  // The "no song page" link AudD uses when it has no page for a song.
  var NO_SONG_PAGE = 'VhpgG';

  function coverFor(link) {
    if (!link) return null;
    var base = E.songLinkBase || 'https://lis.tn/';
    if (link.indexOf(base) === 0) return link + (link.indexOf('?') < 0 ? '?thumb' : '&thumb');
    var yt = /^https?:\/\/(?:www\.)?youtu\.be\/([\w-]{6,20})/.exec(link) ||
      /^https?:\/\/(?:www\.|m\.)?youtube\.com\/watch\?(?:.*&)?v=([\w-]{6,20})/.exec(link);
    if (yt) return (E.youtubeThumbBase || 'https://i3.ytimg.com/vi/') + yt[1] + '/mqdefault.jpg';
    return null;
  }

  // One song from the backend (history element, PubSub or longpoll message).
  // Returns null for anything that has neither a title nor an artist.
  function parseSong(raw) {
    if (!raw || typeof raw !== 'object') return null;
    var title = str(raw.title);
    var artist = str(raw.artist);
    if (!title && !artist) return null;
    var link = httpUrl(raw.song_link);
    if (link && link === (E.songLinkBase || 'https://lis.tn/') + NO_SONG_PAGE) link = null;
    var playedAt = null;
    var p = num(raw.played_at);
    var modern = p != null && p > 0;
    if (modern) playedAt = p > 1e12 ? p : p * 1000;
    // Before played_at existed, the backend put the callback timestamp text
    // in song_length.
    if (playedAt == null) playedAt = parseUtcText(raw.song_length);
    // A time outside 2015..tomorrow is a broken value, not a song time.
    if (playedAt != null && (playedAt < 1420070400000 || playedAt > Date.now() + 86400000)) playedAt = null;
    var playLength = num(raw.play_length);
    return {
      title: title || artist,
      artist: title ? artist : '',
      album: str(raw.album),
      link: link,
      cover: coverFor(link),
      playedAt: playedAt,
      playLength: playLength != null && playLength > 0 && playLength < 86400 ? playLength : 0,
      // Only a backend that sends played_at also sends play_length when AudD
      // has it, so only then does a missing play_length mean the song had
      // just started.
      knowsMode: modern,
      key: [link || '', playedAt || '', title, artist].join('|')
    };
  }
  AudD.parseSong = parseSong;

  function byNewest(a, b) {
    var ta = a.song.playedAt;
    var tb = b.song.playedAt;
    if (ta && tb && ta !== tb) return tb - ta;
    return b.order - a.order;
  }

  // The History object of getChannelById ({OldestElement, Elements: [...]})
  // as a list of songs, newest first. Ordered by time, with the ring position
  // only breaking ties, so it doesn't depend on how a backend counts
  // OldestElement.
  function songsFromChannel(data) {
    var history = data && typeof data === 'object' ? data.History : null;
    var elements = history && Array.isArray(history.Elements) ? history.Elements : [];
    var n = elements.length;
    var oldest = num(history && history.OldestElement) || 0;
    var items = [];
    for (var i = 0; i < n; i++) {
      var song = parseSong(elements[i]);
      if (song) items.push({ song: song, order: (((i - oldest) % n) + n) % n });
    }
    items.sort(byNewest);
    return dedupe(items.map(function (x) { return x.song; }));
  }
  AudD.songsFromChannel = songsFromChannel;

  function dedupe(songs) {
    var seen = {};
    return songs.filter(function (s) {
      if (seen[s.key]) return false;
      seen[s.key] = true;
      return true;
    });
  }

  var HISTORY_CAP = 40;

  // Adds a song to a newest-first list; a song already in it is ignored.
  function mergeSong(songs, song) {
    if (!song) return songs;
    for (var i = 0; i < songs.length; i++) if (songs[i].key === song.key) return songs;
    var list = songs.concat([song]).map(function (s, i) { return { song: s, order: s === song ? Infinity : -i }; });
    list.sort(byNewest);
    return list.map(function (x) { return x.song; }).slice(0, HISTORY_CAP);
  }
  AudD.mergeSong = mergeSong;

  // ------------------------------------------------------------- time

  var LOCALE = params.get('locale') || navigator.language || 'en-US';

  function formatter(options) {
    try { return new Intl.DateTimeFormat(LOCALE, options); } catch (e) { return new Intl.DateTimeFormat('en-US', options); }
  }
  var clockFormat = formatter({ hour: 'numeric', minute: '2-digit' });
  var dayFormat = formatter({ month: 'short', day: 'numeric' });
  var fullFormat = formatter({ weekday: 'short', month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });

  function sameDay(a, b) {
    var x = new Date(a);
    var y = new Date(b);
    return x.getFullYear() === y.getFullYear() && x.getMonth() === y.getMonth() && x.getDate() === y.getDate();
  }

  var dayHeadingFormat = formatter({ weekday: 'short', month: 'short', day: 'numeric' });

  function dayKey(ms) {
    var d = new Date(ms);
    return d.getFullYear() + '-' + d.getMonth() + '-' + d.getDate();
  }

  // The heading above the songs of an earlier day.
  function dayHeading(ms, now) {
    var yesterday = new Date(now);
    yesterday.setDate(yesterday.getDate() - 1);
    if (sameDay(ms, now)) return 'Today';
    if (sameDay(ms, yesterday.getTime())) return 'Yesterday';
    return dayHeadingFormat.format(ms);
  }
  AudD.clock = function (ms) { return clockFormat.format(ms); };
  AudD.fullTime = function (ms) { return fullFormat.format(ms); };

  function ago(ms, now) {
    var d = Math.max(0, now - ms);
    var min = Math.floor(d / 60000);
    if (min < 1) return 'just now';
    if (min < 60) return min + ' min ago';
    var hours = Math.floor(min / 60);
    if (hours < 24 && sameDay(ms, now)) return hours + (hours === 1 ? ' hour ago' : ' hours ago');
    var yesterday = new Date(now);
    yesterday.setDate(yesterday.getDate() - 1);
    if (sameDay(ms, yesterday.getTime())) return 'yesterday at ' + clockFormat.format(ms);
    return 'on ' + dayFormat.format(ms);
  }
  AudD.ago = ago;

  // What the label above the latest song may truthfully say.
  //  - AudD sends a song either when it starts (callbacks=before) or after it
  //    ends, with play_length (the default). A song that came with a play
  //    length has ended, so it is "Just played", never "Now playing".
  //  - A song that came when it started counts as playing for 6 minutes,
  //    the length of nearly every song; after that it is "Played ... ago".
  var NOW_WINDOW = 6 * 60000;
  var JUST_WINDOW = 10 * 60000;

  //  - From a backend without played_at, it can't be told whether the song
  //    started or ended then, so a recent song is only the "Latest song".
  //  - A time more than 2 minutes ahead of this device's clock can't be
  //    placed either.
  function songStatus(song, now) {
    if (!song.playedAt || song.playedAt - now > 120000) return { kind: 'unknown', label: 'Latest song' };
    if (song.playLength > 0) {
      var ended = song.playedAt + song.playLength * 1000;
      if (now - ended < JUST_WINDOW) return { kind: 'just', label: 'Just played' };
      return { kind: 'past', label: 'Played ' + ago(ended, now) };
    }
    if (song.knowsMode && now - song.playedAt < NOW_WINDOW) return { kind: 'now', label: 'Now playing' };
    if (!song.knowsMode && now - song.playedAt < JUST_WINDOW) return { kind: 'unknown', label: 'Latest song' };
    return { kind: 'past', label: 'Played ' + ago(song.playedAt, now) };
  }
  AudD.songStatus = songStatus;

  // ---------------------------------------------------------- network

  // Resolves {status, data} (data is the parsed JSON or null); rejects only
  // when the request could not be made or timed out.
  function request(url, options) {
    options = options || {};
    var controller = typeof AbortController === 'function' ? new AbortController() : null;
    var timer = controller ? setTimeout(function () { controller.abort(); }, options.timeout || 15000) : null;
    var init = { method: options.method || 'GET', cache: 'no-store', credentials: 'omit' };
    if (controller) init.signal = controller.signal;
    if (options.form) {
      init.method = 'POST';
      init.body = new URLSearchParams(options.form);
    }
    return fetch(url, init).then(function (resp) {
      return resp.text().then(function (text) {
        var data = null;
        try { data = text ? JSON.parse(text) : null; } catch (e) { data = null; }
        return { status: resp.status, data: data };
      });
    }).finally(function () { if (timer) clearTimeout(timer); });
  }
  AudD.request = request;

  // twitch.php answers [ok, data-or-error-code, extra]. Returns
  // {ok, code, data, extra}; anything else counts as an internal error.
  function readAnswer(d) {
    // Anything but the [ok, ...] array (an HTML error page, a gateway
    // error) is AudD's problem, not the streamer's.
    if (!Array.isArray(d)) return { ok: false, code: 'internal_error', data: null, extra: null };
    var ok = d[0] === true || d[0] === 1 || d[0] === 'true';
    return {
      ok: ok,
      code: ok ? null : (str(d[1]) || 'internal_error'),
      data: ok && d[1] && typeof d[1] === 'object' ? d[1] : null,
      extra: d[2] && typeof d[2] === 'object' ? d[2] : null
    };
  }
  AudD.readAnswer = readAnswer;

  function twitchPhp(action, form) {
    return request(E.twitchPhp + '?' + action, { form: form, timeout: 20000 }).then(function (r) {
      return readAnswer(r.data);
    });
  }
  AudD.twitchPhp = twitchPhp;

  // The account fields the extension uses from twitch.php.
  function readAccount(obj) {
    obj = obj || {};
    var tillText = str(obj.streams_till_text);
    var till = tillText ? Date.parse(tillText) : NaN;
    return {
      tillText: tillText,
      till: isFinite(till) ? till : null,
      suggestRenewal: obj.suggest_renewal === true || obj.suggest_renewal === 1 || obj.suggest_renewal === '1' || obj.suggest_renewal === 'true',
      twitchId: str(obj.twitch_id),
      // The account is the channel's, but AudD couldn't set the channel up
      // for its recognition results this time.
      setupFailed: obj.channel_setup_failed === true || obj.channel_setup_failed === 1 || obj.channel_setup_failed === '1' || obj.channel_setup_failed === 'true',
      userId: str(obj.user_id),
      paymentsH: str(obj.payments_h)
    };
  }
  AudD.readAccount = readAccount;

  // The state of the account from a get_user/signup/save_user answer:
  // {state: 'active'|'expired'|'unpaid'|'none'|'error', account, code}
  AudD.accountState = function (res) {
    if (res.ok) {
      var a = readAccount(res.data);
      // A date that is clearly in the past means recognition has ended, even
      // if the answer was a success.
      if (a.till != null && a.till < Date.now() - 36 * 3600000) return { state: 'expired', account: a };
      return { state: 'active', account: a };
    }
    if (res.code === 'zero_limit') {
      var e = readAccount(res.extra);
      return { state: e.tillText ? 'expired' : 'unpaid', account: e };
    }
    if (res.code === 'no_user') return { state: 'none', account: null };
    return { state: 'error', code: res.code, account: null };
  };

  // payments.audd.io link for one month of one stream ($45).
  AudD.paymentLink = function (account) {
    if (!account || !account.userId || !account.paymentsH) return null;
    return E.payments + '?user_id=' + encodeURIComponent(account.userId) +
      '&h=' + encodeURIComponent(account.paymentsH) + '&action=make_streams_payment-1';
  };

  AudD.decodeJwt = function (token) {
    try {
      var part = String(token).split('.')[1].replace(/-/g, '+').replace(/_/g, '/');
      return JSON.parse(atob(part));
    } catch (e) {
      return {};
    }
  };

  // ------------------------------------------------------------- feed

  var RETRY_SECONDS = [5, 10, 20, 40, 60];

  // Songs for one channel: the history from getChannelById, then new songs
  // pushed through Twitch PubSub (push) or fetched by longpoll.
  //   phase: 'loading' | 'ready' | 'error'
  AudD.createFeed = function (options) {
    var channel = options.channel;
    var state = { phase: 'loading', songs: [], retryAt: 0, failures: 0 };
    var loadedAt = 0;
    var retryTimer = null;
    var stopped = false;

    function emit() { if (!stopped) options.onChange(state); }

    function load() {
      clearTimeout(retryTimer);
      if (stopped) return Promise.resolve();
      var url = E.lastSong + 'getChannelById/?ch_id=' + encodeURIComponent(channel);
      return request(url, { timeout: 12000 }).then(function (r) {
        if (stopped) return;
        if (r.status === 200 && r.data && typeof r.data === 'object') {
          var songs = songsFromChannel(r.data);
          state.songs.forEach(function (s) { songs = mergeSong(songs, s); });
          state.songs = songs;
        } else if (r.status !== 404 && r.status !== 400) {
          // 404: a channel with no songs yet. 400: an id the backend doesn't
          // take, which can't have songs either.
          throw new Error('HTTP ' + r.status);
        }
        loadedAt = Date.now();
        state.phase = 'ready';
        state.failures = 0;
        state.retryAt = 0;
        emit();
      }).catch(function () {
        if (stopped) return;
        state.failures++;
        var wait = RETRY_SECONDS[Math.min(state.failures - 1, RETRY_SECONDS.length - 1)];
        state.retryAt = Date.now() + wait * 1000;
        if (!state.songs.length) state.phase = 'error';
        emit();
        retryTimer = setTimeout(load, wait * 1000);
      });
    }

    function push(raw) {
      var song = parseSong(raw);
      if (!song) return;
      state.songs = mergeSong(state.songs, song);
      if (state.phase !== 'ready') state.phase = 'ready';
      emit();
    }

    // Longpoll (hosted widget): the same protocol as api.audd.io/longpoll.
    var since = Date.now();
    var pollFailures = 0;
    function poll() {
      if (stopped) return;
      var url = E.lastSong + 'longpoll/?timeout=30&category=' + encodeURIComponent(channel) + '&since_time=' + since;
      request(url, { timeout: 45000 }).then(function (r) {
        var d = r.data;
        // golongpoll answers its errors with 200 and {"error": ...}; anything
        // without events or a timeout is a failure and backs off.
        if (r.status !== 200 || !d || !(Array.isArray(d.events) || d.timeout)) throw new Error('HTTP ' + r.status);
        pollFailures = 0;
        if (Array.isArray(d.events)) {
          d.events.forEach(function (ev) {
            if (!ev) return;
            push(ev.data);
            var t = num(ev.timestamp);
            if (t) since = t;
          });
        } else if (d.timeout) {
          var t = num(d.timestamp);
          if (t) since = t;
        }
        setTimeout(poll, 10);
      }).catch(function () {
        pollFailures++;
        setTimeout(poll, Math.min(30, 3 * pollFailures) * 1000);
      });
    }

    // Catch up on songs missed while the page was hidden (PubSub only
    // delivers while connected).
    function onVisible() {
      if (document.visibilityState === 'visible' && state.phase === 'ready' && Date.now() - loadedAt > 120000) load();
    }

    // In case PubSub messages don't arrive: while the page is visible and
    // the latest song is more than 4 minutes old, reload every 3 to 4
    // minutes.
    var refreshTimer = null;
    function scheduleRefresh() {
      refreshTimer = setTimeout(function () {
        var top = state.songs[0];
        var old = !top || !top.playedAt || Date.now() - top.playedAt > 4 * 60000;
        if (!stopped && document.visibilityState === 'visible' && state.phase === 'ready' && old) load();
        if (!stopped) scheduleRefresh();
      }, (180 + Math.random() * 60) * 1000);
    }

    return {
      state: state,
      start: function () {
        load();
        if (options.longpoll) {
          poll();
        } else {
          document.addEventListener('visibilitychange', onVisible);
          scheduleRefresh();
        }
      },
      retryNow: function () { state.phase = state.songs.length ? state.phase : 'loading'; emit(); return load(); },
      push: push,
      pushMessage: function (message) {
        try { push(JSON.parse(message)); } catch (e) { /* not a song */ }
      },
      stop: function () {
        stopped = true;
        clearTimeout(retryTimer);
        clearTimeout(refreshTimer);
        document.removeEventListener('visibilitychange', onVisible);
      }
    };
  };

  // ------------------------------------------------------------ views

  function cover(song, cls) {
    var box = el('span', { class: 'cover' + (cls ? ' ' + cls : ''), 'aria-hidden': 'true' }, [icon('note', 'cover__icon')]);
    if (!song.cover) {
      box.classList.add('cover--missing');
      return box;
    }
    var img = el('img', { class: 'cover__img', alt: '', src: song.cover, decoding: 'async', referrerpolicy: 'no-referrer' });
    function fail() {
      if (img.parentNode) img.parentNode.removeChild(img);
      box.classList.add('cover--missing');
    }
    img.addEventListener('load', function () {
      if (img.naturalWidth < 2) fail();
      else box.classList.add('cover--loaded');
    });
    img.addEventListener('error', fail);
    box.appendChild(img);
    return box;
  }

  function songLabel(song) {
    return song.artist ? song.title + ' by ' + song.artist : song.title;
  }

  function linkOrBox(song, cls, label) {
    if (!song.link) return el('div', { class: cls });
    return el('a', {
      class: cls,
      href: song.link,
      target: '_blank',
      rel: 'noopener noreferrer',
      'aria-label': label
    });
  }

  function statusLine(status, song) {
    return el('span', { class: 'status-row' }, [
      el('span', { class: 'status status--' + status.kind }, [
        status.kind === 'now' ? equalizer() : null,
        el('span', { class: 'status__text', text: status.label })
      ]),
      song.link ? el('span', { class: 'listen', 'aria-hidden': 'true' }, [el('span', { class: 'listen__label', text: 'Listen' }), icon('open')]) : null
    ]);
  }

  function nowCard(song, status, extraClass) {
    var card = linkOrBox(song, 'now' + (extraClass ? ' ' + extraClass : ''),
      status.label + ': ' + songLabel(song) + (song.link ? '. Opens the song page in a new tab.' : ''));
    if (song.cover) {
      var wash = el('img', { class: 'now__wash', alt: '', src: song.cover, 'aria-hidden': 'true', referrerpolicy: 'no-referrer' });
      wash.addEventListener('error', function () { wash.remove(); });
      card.appendChild(wash);
    }
    card.appendChild(cover(song, 'now__cover'));
    card.appendChild(el('span', { class: 'now__text' }, [
      statusLine(status, song),
      el('span', { class: 'now__title', title: song.title, text: song.title }),
      song.artist ? el('span', { class: 'now__artist', title: song.artist, text: song.artist }) : null
    ]));
    return card;
  }

  // Earlier songs, newest first, under a heading for each day before today
  // ("Yesterday", "Thu, Nov 19").
  function historyList(songs, now, level) {
    var groups = el('div', { class: 'history__groups' });
    var list = null;
    var day = dayKey(now);
    songs.forEach(function (song) {
      if (song.playedAt && dayKey(song.playedAt) !== day) {
        day = dayKey(song.playedAt);
        groups.appendChild(el('h' + (level + 1), { class: 'history__day', text: dayHeading(song.playedAt, now) }));
        list = null;
      }
      if (!list) {
        list = el('ol', { class: 'history__list' });
        groups.appendChild(list);
      }
      var time = song.playedAt
        ? el('time', { class: 'row__time', datetime: new Date(song.playedAt).toISOString(), title: fullFormat.format(song.playedAt), text: clockFormat.format(song.playedAt) })
        : el('span', { class: 'row__time', text: '' });
      var row = linkOrBox(song, 'row', (song.playedAt ? fullFormat.format(song.playedAt) + ', ' : '') + songLabel(song) + (song.link ? '. Opens the song page in a new tab.' : ''));
      append(row, [
        time,
        el('span', { class: 'row__text' }, [
          el('span', { class: 'row__title', title: song.title, text: song.title }),
          song.artist ? el('span', { class: 'row__artist', title: song.artist, text: song.artist }) : null
        ])
      ]);
      list.appendChild(el('li', { 'data-key': song.key }, [row]));
    });
    return groups;
  }

  function brand() {
    return el('a', { class: 'brand', href: E.site, target: '_blank', rel: 'noopener noreferrer', title: 'Songs recognized by AudD' }, [
      el('img', { class: 'brand__logo', src: 'images/audd-logo.png', alt: 'AudD', width: 52, height: 14 })
    ]);
  }
  AudD.brand = brand;

  function skeleton() {
    return el('div', { class: 'skeleton', 'aria-hidden': 'true' }, [
      el('div', { class: 'skeleton__now' }, [el('span', { class: 'sk sk--cover' }), el('span', { class: 'skeleton__lines' }, [
        el('span', { class: 'sk sk--short' }), el('span', { class: 'sk sk--long' }), el('span', { class: 'sk sk--mid' })
      ])]),
      el('div', { class: 'skeleton__rows' }, [el('span', { class: 'sk sk--row' }), el('span', { class: 'sk sk--row' }), el('span', { class: 'sk sk--row' })])
    ]);
  }

  function secondsLeft(at) {
    return Math.max(1, Math.round((at - Date.now()) / 1000));
  }

  // A view renders a feed's state into root for one surface:
  //   'panel'     below the video, and in the configuration page preview
  //   'component' over the video
  //   'widget'    the hosted widget (OBS browser source)
  AudD.createView = function (root, surface, options) {
    options = options || {};
    var last = { phase: null, nowKey: null, nowKind: null, listKeys: null, note: null, example: null };
    var current = { phase: 'loading', songs: [] };
    var note = null;
    var announcer = el('p', { class: 'visually-hidden', 'aria-live': 'polite' });
    var announce = options.announce !== false && surface !== 'component';
    // Heading level of "Recently played" (3 inside the configuration page)
    var level = options.headingLevel || 2;
    var body = el('div', { class: 'view view--' + surface });
    root.textContent = '';
    root.appendChild(body);
    if (announce) root.appendChild(announcer);

    var slots = {};
    var retryLabel = null;

    function onRetry() { if (options.onRetry) options.onRetry(); }

    function renderNote() {
      if (!slots.note) return;
      slots.note.textContent = '';
      if (!note) { slots.note.hidden = true; return; }
      slots.note.hidden = false;
      append(slots.note, [
        el('p', { class: 'note__title' }, [icon('lock'), el('span', { text: 'Only you can see this' })]),
        el('p', { class: 'note__text', text: note.text }),
        note.href ? el('a', { class: 'note__action', href: note.href, target: '_blank', rel: 'noopener noreferrer' }, [note.action + ' ', icon('open')]) : null
      ]);
    }

    function messageBlock(kind, title, text, extra) {
      return el('div', { class: 'message message--' + kind }, [
        el('span', { class: 'message__icon', 'aria-hidden': 'true' }, [icon(kind === 'error' ? 'alert' : 'note')]),
        el('p', { class: 'message__title', text: title }),
        text ? el('p', { class: 'message__text', text: text }) : null,
        extra || null
      ]);
    }

    function renderPanelish(state, now) {
      var songs = state.songs;
      var phaseKey = state.phase === 'ready' && !songs.length ? 'empty' : (songs.length ? 'songs' : state.phase);
      var rebuild = phaseKey !== last.phase || options.example !== last.example;
      if (rebuild) {
        body.textContent = '';
        slots = {};
        last.nowKey = last.listKeys = last.nowKind = null;
        if (surface === 'panel') {
          slots.note = el('div', { class: 'note', role: 'status' });
          body.appendChild(slots.note);
          renderNote();
        }
        if (phaseKey === 'loading') {
          body.appendChild(skeleton());
          body.setAttribute('aria-busy', 'true');
        } else {
          body.removeAttribute('aria-busy');
        }
        if (phaseKey === 'error') {
          if (surface === 'widget') {
            body.appendChild(el('div', { class: 'widget-quiet' }));
          } else {
            retryLabel = el('span', { class: 'message__retry-text' });
            body.appendChild(messageBlock('error', 'Can’t load songs right now',
              'AudD isn’t responding.',
              el('p', { class: 'message__retry' }, [retryLabel, ' ',
                el('button', { class: 'button button--small', type: 'button', on: { click: onRetry } }, [icon('retry'), 'Try again'])])));
          }
        }
        if (phaseKey === 'empty') {
          if (surface === 'widget') {
            body.appendChild(el('p', { class: 'widget-empty', text: 'No song recognized yet' }));
          } else {
            body.appendChild(messageBlock('empty', 'No songs yet',
              'When AudD recognizes a song on this stream, it shows up here with a link to listen.'));
          }
        }
        if (phaseKey === 'nochannel') {
          body.appendChild(messageBlock('empty', 'This address needs your channel',
            'This page shows the song playing on a stream, as a Browser source in OBS. Copy your address from “Show the song in OBS” on the AudD extension’s Configure page in your Twitch Creator Dashboard.'));
        }
        if (phaseKey === 'songs') {
          slots.now = el('section', { class: 'now-slot', 'aria-label': 'Latest song' });
          body.appendChild(slots.now);
          if (surface === 'panel' || options.history) {
            slots.historyList = el('div', { class: 'history__scroll' });
            slots.history = el('section', { class: 'history', 'aria-labelledby': 'history-heading' }, [
              el('div', { class: 'history__head' }, [
                el('h' + level, { id: 'history-heading', class: 'history__heading', text: 'Recently played' }),
                surface === 'panel' ? brand() : null
              ]),
              slots.historyList
            ]);
            body.appendChild(slots.history);
          }
        }
        if (surface === 'panel' && phaseKey !== 'songs' && phaseKey !== 'loading') {
          body.appendChild(el('div', { class: 'footer' }, [brand()]));
        }
        if (options.example) {
          body.appendChild(el('p', { class: 'example-tag', text: 'Example' }));
        }
        last.phase = phaseKey;
        last.example = options.example;
      }

      if (phaseKey === 'error' && retryLabel) {
        retryLabel.textContent = state.retryAt ? 'Trying again in ' + secondsLeft(state.retryAt) + (secondsLeft(state.retryAt) === 1 ? ' second.' : ' seconds.') : '';
      }

      if (phaseKey !== 'songs') return;

      var top = songs[0];
      var status = songStatus(top, now);
      if (top.key !== last.nowKey || status.kind !== last.nowKind) {
        var isNew = last.nowKey !== null && top.key !== last.nowKey;
        slots.now.textContent = '';
        slots.now.appendChild(nowCard(top, status, isNew ? 'is-new' : ''));
        // Screen readers hear new songs, not the one already there on load.
        if (isNew) announcer.textContent = status.label + ': ' + songLabel(top);
        last.nowKey = top.key;
        last.nowKind = status.kind;
      } else {
        var label = slots.now.querySelector('.status__text');
        if (label) label.textContent = status.label;
      }

      if (slots.historyList) {
        var rest = songs.slice(1);
        // The date is part of the key: "Today" and "Yesterday" move at midnight.
        var keys = new Date(now).toDateString() + '\n' + rest.map(function (s) { return s.key; }).join('\n');
        if (keys !== last.listKeys) {
          var previous = last.listKeys == null ? null : last.listKeys.split('\n');
          slots.historyList.textContent = '';
          if (rest.length) {
            var list = historyList(rest, now, level);
            if (previous) {
              Array.prototype.forEach.call(list.querySelectorAll('li[data-key]'), function (li) {
                if (previous.indexOf(li.getAttribute('data-key')) < 0) li.classList.add('is-new');
              });
            }
            slots.historyList.appendChild(list);
          } else {
            slots.historyList.appendChild(el('p', { class: 'history__empty', text: 'Earlier songs will be listed here.' }));
          }
          last.listKeys = keys;
          if (surface === 'widget') fitRows();
        }
      }
    }

    function isOld(song, now) {
      return !!song.playedAt && now - (song.playedAt + song.playLength * 1000) > 60 * 60000;
    }

    // The widget can't scroll: show only the rows that fit whole.
    function fitRows() {
      var box = slots.historyList;
      if (!box) return;
      var nodes = box.querySelectorAll('li, .history__day');
      Array.prototype.forEach.call(nodes, function (n) { n.classList.remove('is-clipped'); });
      var bottom = box.getBoundingClientRect().bottom;
      var clipped = false;
      Array.prototype.forEach.call(nodes, function (n) {
        if (clipped || n.getBoundingClientRect().bottom > bottom + 0.5) {
          clipped = true;
          n.classList.add('is-clipped');
        }
      });
      // A day heading with no row under it goes too.
      Array.prototype.forEach.call(box.querySelectorAll('.history__day'), function (h) {
        var list = h.nextElementSibling;
        if (list && !list.querySelector('li:not(.is-clipped)')) h.classList.add('is-clipped');
      });
    }
    if (surface === 'widget') window.addEventListener('resize', function () { fitRows(); });

    function renderComponent(state, now) {
      var songs = state.songs;
      var top = songs[0];
      var status = top ? songStatus(top, now) : null;
      var key;
      if (!top) key = state.phase;
      else key = top.key + '|' + (status.kind !== 'past' ? 'now' : isOld(top, now) ? 'hidden' : 'past');
      if (key === last.nowKey) {
        var label = body.querySelector('.status__text');
        if (label && status) label.textContent = status.label;
        return;
      }
      var isNew = !!top && last.nowKey !== null && last.nowKey.indexOf(top.key) !== 0;
      last.nowKey = key;
      body.textContent = '';
      // Over live video, show nothing rather than a message the viewer can't
      // act on: no song yet, loading, the backend unreachable, or a song
      // from more than an hour ago.
      if (!top || isOld(top, now)) return;
      body.appendChild(nowCard(top, status, isNew ? 'is-new' : ''));
    }

    function render() {
      var now = Date.now();
      if (surface === 'component') renderComponent(current, now);
      else renderPanelish(current, now);
    }

    // Labels like "Played 3 min ago" and the retry countdown move with time:
    // every second while counting down to a retry, every 15 s otherwise.
    var ticker = null;
    function schedule() {
      var counting = current.phase === 'error' && !current.songs.length;
      ticker = setTimeout(function () { render(); schedule(); }, counting ? 1000 : 15000);
    }
    schedule();

    return {
      update: function (state) {
        current = state;
        render();
        clearTimeout(ticker);
        schedule();
      },
      setNote: function (n) { note = n; renderNote(); },
      setExample: function (on) { options.example = on; render(); },
      destroy: function () { clearTimeout(ticker); }
    };
  };

  // ------------------------------------------------------------ theme

  AudD.setTheme = function (theme) {
    document.documentElement.setAttribute('data-theme', theme === 'light' ? 'light' : 'dark');
  };

  AudD.isMobile = params.get('platform') === 'mobile';
  if (AudD.isMobile) document.documentElement.classList.add('is-mobile');
})();
