// The panel (below the video, and the mobile view), the video component,
// and the hosted widget (panel.html?ch=... used as an OBS browser source).
(function () {
  'use strict';
  var AudD = window.AudD;
  var params = AudD.params;
  var root = document.getElementById('app');
  var surface = document.body.getAttribute('data-surface') === 'component' ? 'component' : 'panel';

  // ------------------------------------------------ hosted widget mode
  // https://widget.audd.tech/?ch=<Twitch channel ID or -longpoll category>
  //   &history     list earlier songs
  //   &background  draw a card behind the songs (transparent otherwise)
  //   &shadow      shadow under the cover art
  //   &black-font  dark text, for light scenes
  var widgetChannel = params.get('ch');
  if (widgetChannel) {
    var html = document.documentElement;
    html.classList.add('widget');
    var options = {
      history: params.has('history'),
      background: params.has('background'),
      shadow: params.has('shadow'),
      darkText: params.has('black-font')
    };
    Object.keys(options).forEach(function (k) {
      if (options[k]) html.classList.add('widget--' + k.replace(/[A-Z]/g, function (c) { return '-' + c.toLowerCase(); }));
    });
    AudD.setTheme(options.darkText ? 'light' : 'dark');
    var widgetView = AudD.createView(root, 'widget', options);
    var widgetFeed = AudD.createFeed({ channel: widgetChannel, longpoll: true, onChange: widgetView.update });
    widgetView.update(widgetFeed.state);
    widgetFeed.start();
    return;
  }

  // ------------------------------------------------------- on Twitch
  AudD.setTheme('dark');
  var feed = null;
  var view = AudD.createView(root, surface, { onRetry: function () { if (feed) feed.retryNow(); } });
  view.update({ phase: 'loading', songs: [] });

  var ext = window.Twitch && window.Twitch.ext;
  if (!ext) {
    view.update({ phase: 'error', songs: [] });
    return;
  }

  ext.onContext(function (context) {
    if (context && context.theme) AudD.setTheme(context.theme);
  });

  var channel = null;
  var listening = false;
  var broadcasterChecked = false;

  ext.onAuthorized(function (auth) {
    if (!auth || !auth.channelId) return;
    if (auth.channelId !== channel) {
      channel = auth.channelId;
      if (feed) feed.stop();
      feed = AudD.createFeed({ channel: channel, onChange: view.update });
      view.update(feed.state);
      feed.start();
    }
    if (!listening) {
      listening = true;
      ext.listen('broadcast', function (target, contentType, message) {
        if (feed) feed.pushMessage(message);
      });
    }
    if (surface === 'panel' && !broadcasterChecked) {
      broadcasterChecked = true;
      noteForBroadcaster(auth.token);
    }
  });

  // The broadcaster looking at their own panel learns when recognition is
  // not running for their channel. Viewers never see this.
  function noteForBroadcaster(token) {
    var role = (ext.viewer && ext.viewer.role) || AudD.decodeJwt(token).role;
    if (role !== 'broadcaster') return;
    AudD.twitchPhp('get_user', { jwt: token }).then(function (res) {
      var s = AudD.accountState(res);
      var link = AudD.paymentLink(s.account);
      if (s.state === 'none') {
        view.setNote({ text: 'Songs aren’t being recognized: connect your AudD account on this extension’s Configure page in your Creator Dashboard.' });
      } else if (s.state === 'unpaid') {
        view.setNote({ text: 'Turn on music recognition to show viewers the songs on your stream ($45 per month).', action: 'Turn on ($45 per month)', href: link });
      } else if (s.state === 'expired') {
        view.setNote({ text: 'Music recognition ended' + (s.account.tillText ? ' on ' + s.account.tillText : '') + ', so new songs don’t appear here.', action: 'Renew ($45 per month)', href: link });
      } else if (s.state === 'active' && s.account.suggestRenewal) {
        view.setNote({ text: 'Music recognition ends' + (s.account.tillText ? ' on ' + s.account.tillText : ' soon') + '.', action: 'Renew ($45 per month)', href: link });
      }
    }).catch(function () { /* a note only; nothing to show when AudD can't be reached */ });
  }
})();
