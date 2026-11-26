(function () {
  'use strict';
  var live = document.getElementById('live');
  var reset = document.getElementById('reset');
  reset.addEventListener('click', function (e) {
    e.preventDefault();
    fetch('/dev/reset').then(function () { location.reload(); });
  });
  live.checked = /[?&]live\b/.test(location.search);
  live.addEventListener('change', function () { location.search = live.checked ? '?live' : ''; });
  var every = live.checked ? '&every=20' : '';

  var groups = [];
  function group(title, note, tiles) { groups.push({ title: title, note: note, tiles: tiles }); }
  function t(label, url, w, h, scale, desc, dark) { return { label: label, url: url, w: w, h: h, scale: scale || 1, desc: desc, dark: dark }; }

  var panelStates = [
    ['typical', 'Song playing now, 11 earlier songs'],
    ['just', 'AudD sends songs after they end: "Just played"'],
    ['recent', 'Last song 20 minutes ago'],
    ['stale', 'Last song 3 hours ago'],
    ['one', 'One song, nothing earlier'],
    ['full', '40 songs over 30 hours'],
    ['long', 'Long titles and artists, CJK, unbroken strings, HTML in names'],
    ['nocover', 'Broken cover, "no page" link, no link'],
    ['youtube', 'YouTube link: YouTube thumbnail'],
    ['legacy', 'Backend without played_at (song_length text only)'],
    ['empty', 'No songs yet (404 not_found)'],
    ['empty-prod', 'No songs yet (production’s empty ring)'],
    ['slow', 'Loading (the backend takes 8 s)'],
    ['down', 'Backend unreachable (503), retrying'],
    ['flaky', 'Backend fails twice, then answers']
  ];
  ['dark', 'light'].forEach(function (theme) {
    group('Panel, ' + theme + ' theme (318 × 300)', theme === 'dark' ? 'Below the video on desktop. Theme comes from Twitch.ext.onContext.' : null,
      panelStates.map(function (s) {
        return t(s[0], '/ext/panel.html?anchor=panel&platform=web&theme=' + theme + '&channel=' + s[0] + every, 318, 300, 1, s[1], theme === 'dark');
      }));
  });
  group('Panel as the broadcaster sees it', 'Only the channel owner sees the note; viewers never do.', [
    ['none', 'Not set up'], ['unpaid', 'Account connected, recognition never turned on'], ['expired', 'Recognition ended'], ['expiring', 'Recognition ends soon']
  ].map(function (s) {
    return t('account=' + s[0], '/ext/panel.html?anchor=panel&platform=web&theme=dark&role=broadcaster&account=' + s[0] + '&channel=typical' + every, 318, 300, 1, s[1], true);
  }));
  group('Twitch mobile app (375 wide)', null, [
    t('typical, dark', '/ext/panel.html?anchor=panel&platform=mobile&theme=dark&channel=typical' + every, 375, 640, 1, null, true),
    t('typical, light', '/ext/panel.html?anchor=panel&platform=mobile&theme=light&channel=typical' + every, 375, 640, 1),
    t('empty, dark', '/ext/panel.html?anchor=panel&platform=mobile&theme=dark&channel=empty', 375, 640, 1, null, true)
  ]);

  function stage(src, bg) { return '/dev/static/stage.html?bg=' + bg + '&src=' + encodeURIComponent(src); }
  var compStates = [['typical', 'Now playing'], ['just', 'Just played'], ['recent', 'Played 20 min ago'], ['stale', 'Played 3 h ago: nothing shown'], ['long', 'Long strings'], ['nocover', 'No cover art'], ['empty', 'No songs yet: nothing shown'], ['down', 'Backend unreachable: nothing shown']];
  group('Video component over the video (320 × 160, shown at half size on a 1138 × 640 player)', 'Always the dark overlay style, like Twitch’s player controls. Bright, dark and busy footage.', [].concat(
    compStates.map(function (s) { return t(s[0] + ' / bright', stage('/ext/video_component.html?anchor=component&platform=web&theme=light&channel=' + s[0] + every, 'bright'), 1138, 640, 0.5, s[1]); }),
    ['typical', 'recent'].map(function (c) { return t(c + ' / dark', stage('/ext/video_component.html?anchor=component&platform=web&theme=dark&channel=' + c, 'dark'), 1138, 640, 0.5); }),
    ['typical', 'recent'].map(function (c) { return t(c + ' / busy', stage('/ext/video_component.html?anchor=component&platform=web&theme=dark&channel=' + c, 'busy'), 1138, 640, 0.5); })
  ));

  function widget(q, bg, w, h) { return stage('/ext/panel.html?ch=' + q, bg) + '&w=' + w + '&h=' + h + '&pos=left'; }
  group('Hosted widget / OBS browser source (widget.audd.tech)', 'Transparent unless &background. Shown on a 1138 × 640 scene at half size.', [
    t('?ch=typical (600 × 160)', widget('typical', 'busy', 600, 160), 1138, 640, 0.5),
    t('&shadow', widget('typical&shadow', 'dark', 600, 160), 1138, 640, 0.5),
    t('&background', widget('typical&background', 'busy', 600, 160), 1138, 640, 0.5),
    t('&history&background (600 × 420)', widget('typical&history&background', 'busy', 600, 420), 1138, 640, 0.5),
    t('&history (no background)', widget('typical&history&shadow', 'dark', 600, 420), 1138, 640, 0.5),
    t('&black-font on a light scene', widget('typical&black-font', 'bright', 600, 160), 1138, 640, 0.5),
    t('&background&black-font', widget('typical&background&black-font&history', 'bright', 600, 420), 1138, 640, 0.5),
    t('tall source (400 × 400)', widget('long&background', 'dark', 400, 400), 1138, 640, 0.5),
    t('no songs yet', widget('empty', 'dark', 600, 160), 1138, 640, 0.5),
    t('live: a new song every 20 s (longpoll)', widget('live&history&background', 'busy', 600, 420), 1138, 640, 0.5)
  ]);

  var accounts = [
    ['none', 'Not linked'], ['active', 'Linked, recognition on'], ['expiring', 'Ends in 3 days'], ['expired', 'Recognition ended'],
    ['unpaid', 'Linked, never turned on'], ['error-internal_error', 'twitch.php error'], ['down', 'dashboard.audd.io unreachable'], ['slow', 'Loading (6 s)']
  ];
  group('Configuration page (960 wide, shown at 60%)', 'In the Creator Dashboard. Dark by default. Try "Sign in with AudD" in the first one: the tab it opens is a local stand-in for AudD’s page, where you pick the account and how the sign-in ends.', [].concat(
    accounts.map(function (a) { return t('account=' + a[0], '/ext/config.html?mode=config&theme=dark&channel=cfg-' + a[0] + '&account=' + a[0], 960, 1000, 0.6, a[1], true); }),
    [
      t('account=none, light', '/ext/config.html?mode=config&theme=light&channel=cfg-none-light&account=none', 960, 1000, 0.6),
      t('account=active, light', '/ext/config.html?mode=config&theme=light&channel=cfg-active-light&account=active', 960, 1000, 0.6),
      t('narrow (420)', '/ext/config.html?mode=config&theme=dark&channel=cfg-narrow&account=none', 420, 1200, 0.6, null, true),
      t('account=slowlink', '/ext/config.html?mode=config&theme=dark&channel=cfg-slowlink&account=slowlink', 960, 1000, 0.6, 'Sign in and approve: linking takes 8 s', true),
      t('signin=nobackend', '/ext/config.html?mode=config&theme=dark&channel=cfg-nobackend&account=none&signin=nobackend', 960, 1000, 0.6, 'Backend without the sign-in (404): sign-in unavailable', true),
      t('signin=busy', '/ext/config.html?mode=config&theme=dark&channel=cfg-busy&account=none&signin=busy', 960, 1000, 0.6, 'Too many sign-ins (429): sign-in unavailable', true),
      t('signin=noresult', '/ext/config.html?mode=config&theme=dark&channel=cfg-noresult&account=none&signin=noresult', 960, 1000, 0.6, 'Sign in, then close the tab: the result fails (502), so the page offers to paste a token', true),
      t('role=moderator', '/ext/config.html?mode=config&theme=dark&channel=cfg-moderator&account=none&role=moderator', 960, 1000, 0.6, 'Not the broadcaster (403)', true)
    ]
  ));

  group('Before: version 0.0.4 from the repository', 'Served from the -before folder when given.', [
    t('panel, dark', '/before/panel.html?anchor=panel&platform=web&theme=dark&channel=typical', 318, 300, 1, null, true),
    t('panel, long strings', '/before/panel.html?anchor=panel&platform=web&theme=dark&channel=long', 318, 300, 1, null, true),
    t('component', stage('/before/video_component.html?anchor=component&channel=typical', 'bright'), 1138, 640, 0.5),
    t('config', '/before/config.html?mode=config&channel=cfg-before&account=none', 960, 700, 0.6, null, true)
  ]);

  var root = document.getElementById('gallery');
  groups.forEach(function (g) {
    var h = document.createElement('h2');
    h.textContent = g.title;
    root.appendChild(h);
    if (g.note) {
      var p = document.createElement('p');
      p.className = 'group-note';
      p.textContent = g.note;
      root.appendChild(p);
    }
    var tiles = document.createElement('div');
    tiles.className = 'tiles';
    g.tiles.forEach(function (tile) {
      var box = document.createElement('div');
      box.className = 'tile';
      var a = document.createElement('a');
      a.className = 'title';
      a.href = tile.url;
      a.target = '_blank';
      a.textContent = tile.label;
      box.appendChild(a);
      var frame = document.createElement('div');
      frame.className = 'frame' + (tile.dark ? ' dark-backdrop' : '');
      frame.style.width = Math.round(tile.w * tile.scale) + 'px';
      frame.style.height = Math.round(tile.h * tile.scale) + 'px';
      var iframe = document.createElement('iframe');
      iframe.src = tile.url;
      iframe.width = tile.w;
      iframe.height = tile.h;
      iframe.loading = 'lazy';
      iframe.title = tile.label;
      if (tile.scale !== 1) iframe.style.transform = 'scale(' + tile.scale + ')';
      frame.appendChild(iframe);
      box.appendChild(frame);
      if (tile.desc) {
        var d = document.createElement('div');
        d.className = 'desc';
        d.textContent = tile.desc;
        box.appendChild(d);
      }
      tiles.appendChild(box);
    });
    root.appendChild(tiles);
  });
})();
