// Stand-in for Twitch's extension helper (twitch-ext.min.js) in the local
// preview. It implements the parts the extension uses: onAuthorized (with a
// JWT signed by the preview server), onContext (theme), listen('broadcast')
// (new songs from the preview server), and viewer.role.
//
// URL parameters: theme, channel, role, account, every, reauth (seconds).
(function () {
  'use strict';
  var q = new URLSearchParams(window.location.search);
  var isConfig = /config\.html$/.test(window.location.pathname);
  var role = q.get('role') || (isConfig ? 'broadcaster' : 'viewer');
  var channel = q.get('channel') || 'typical';
  var account = q.get('account') || '';
  var theme = q.get('theme') || 'dark';
  var every = q.get('every') || '0';

  var authCallbacks = [];
  var contextCallbacks = [];
  var auth = null;

  function context() {
    return {
      theme: theme, mode: isConfig ? 'config' : 'viewer', language: 'en', isFullScreen: false, isMuted: false,
      isPaused: false, isTheatreMode: false, playbackMode: 'video', arePlayerControlsVisible: true,
      game: 'Music', bitrate: 6000, bufferSize: 2, displayResolution: '1280x720', videoResolution: '1920x1080',
      hlsLatencyBroadcaster: 4
    };
  }

  function authorize() {
    var url = '/dev/jwt?role=' + encodeURIComponent(role) + '&channel=' + encodeURIComponent(channel) +
      '&account=' + encodeURIComponent(account);
    fetch(url).then(function (r) { return r.json(); }).then(function (a) {
      auth = a;
      authCallbacks.forEach(function (cb) {
        try { cb(a); } catch (e) { console.error('onAuthorized callback failed:', e); }
      });
    });
  }

  var source = null;
  var broadcastListeners = [];

  window.Twitch = {
    ext: {
      version: 'preview',
      environment: 'preview',
      viewer: {
        get role() { return role; },
        opaqueId: 'U-preview',
        id: null,
        isLinked: false,
        onChanged: function () {}
      },
      configuration: {
        broadcaster: undefined, developer: undefined, global: undefined,
        onChanged: function (cb) { setTimeout(cb, 0); },
        set: function () {}
      },
      features: { isChatEnabled: true, isBitsEnabled: false, isSubscriptionStatusAvailable: false, onChanged: function () {} },
      actions: { followChannel: function () {}, minimize: function () {}, onFollow: function () {}, requestIdShare: function () {} },
      rig: { log: function () {} },
      onAuthorized: function (cb) {
        authCallbacks.push(cb);
        if (auth) setTimeout(function () { cb(auth); }, 0);
      },
      onContext: function (cb) {
        contextCallbacks.push(cb);
        setTimeout(function () { cb(context(), ['theme']); }, 0);
      },
      onError: function () {},
      onVisibilityChanged: function () {},
      onPositionChanged: function () {},
      listen: function (target, cb) {
        if (target !== 'broadcast') return;
        broadcastListeners.push(cb);
        if (source) return;
        source = new EventSource('/dev/pubsub?channel=' + encodeURIComponent(channel) + '&every=' + encodeURIComponent(every));
        source.onmessage = function (e) {
          broadcastListeners.forEach(function (fn) { fn('broadcast', 'application/json', e.data); });
        };
      },
      unlisten: function (target, cb) {
        broadcastListeners = broadcastListeners.filter(function (fn) { return fn !== cb; });
      },
      send: function () {}
    }
  };

  // The gallery can switch the theme live, the way Twitch calls onContext.
  window.addEventListener('message', function (e) {
    if (e.data && e.data.type === 'audd-preview-theme') {
      theme = e.data.theme;
      contextCallbacks.forEach(function (cb) { cb(context(), ['theme']); });
    }
  });

  // Twitch's helper authorizes after a handshake with the page around the
  // frame, which ends after the extension's document has loaded.
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', authorize);
  else authorize();
  // Twitch calls onAuthorized again when it refreshes the JWT.
  var reauth = Number(q.get('reauth'));
  if (reauth > 0) setInterval(authorize, reauth * 1000);
})();
