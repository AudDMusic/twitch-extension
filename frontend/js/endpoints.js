// Every address the extension talks to or links to. The Twitch console must
// allowlist the hosts the extension fetches from (see README.md).
window.AUDD_ENDPOINTS = {
  // The extension backend: history, longpoll and Sign in with AudD
  lastSong: 'https://api.audd.io/lastSong/',
  // Links a Twitch channel to an AudD account
  twitchPhp: 'https://dashboard.audd.io/api/twitch.php',
  // Pages people open in a new tab
  payments: 'https://payments.audd.io/',
  dashboard: 'https://dashboard.audd.io/',
  site: 'https://audd.io/',
  widget: 'https://widget.audd.tech/',
  // Song pages and cover art
  songLinkBase: 'https://lis.tn/',
  youtubeThumbBase: 'https://i3.ytimg.com/vi/'
};
