// Every address the extension talks to or links to. The Twitch console must
// allowlist the hosts the extension fetches from (see README.md).
window.AUDD_ENDPOINTS = {
  // The extension backend: history, longpoll and the sign-in helper
  lastSong: 'https://api.audd.io/lastSong/',
  // Links a Twitch channel to an AudD account
  twitchPhp: 'https://dashboard.audd.io/api/twitch.php',
  // AudD's OAuth authorization server (device flow for "Sign in with AudD")
  oauthDevice: 'https://dashboard-api.audd.io/oauth2/device_authorization',
  oauthToken: 'https://dashboard-api.audd.io/oauth2/token',
  oauthRevoke: 'https://dashboard-api.audd.io/oauth2/revoke',
  oauthClientId: 'audd-twitch',
  oauthScope: 'token:read',
  oauthResource: 'https://mcp.audd.io',
  // Pages people open in a new tab
  payments: 'https://payments.audd.io/',
  dashboard: 'https://dashboard.audd.io/',
  site: 'https://audd.io/',
  widget: 'https://widget.audd.tech/',
  // Song pages and cover art
  songLinkBase: 'https://lis.tn/',
  youtubeThumbBase: 'https://i3.ytimg.com/vi/'
};
