# AudD Music Recognition Twitch extension

The source code of the [AudD Music Recognition Twitch extension](https://dashboard.twitch.tv/extensions/ikcbah7wbue48v7doo4edulmxblt64). It shows viewers the song playing on a stream, with a link to open it on their music service, and posts each song in chat. The songs are recognized by the [AudD Music Recognition API](https://audd.io/) from the stream's audio.

## What's in the extension

- **Panel** (`frontend/panel.html`), below the video and in the Twitch mobile app: the song playing now, with cover art and a link to the song page, and the songs played before it. Follows Twitch's light and dark themes.
- **Video component** (`frontend/video_component.html`), over the video: the song playing now, in the dark overlay style of Twitch's player controls. A song that played a while ago shrinks to one line; with no song from the last hour, or while the backend can't be reached, nothing is drawn over the video.
- **Configuration page** (`frontend/config.html`), in the Creator Dashboard: the streamer connects an AudD account (Sign in with AudD, a new account, or a pasted API token), turns on recognition for the stream, and gets the address of the OBS widget. A preview shows the panel with the channel's real songs.
- **Hosted widget** (the same `panel.html` at [widget.audd.tech](https://widget.audd.tech/)), for OBS browser sources.

The label above the latest song only says what the timestamps support: *Now playing* for up to 6 minutes after a song started, *Just played* for a song AudD reported after it ended, and *Played 25 min ago* after that. A backend that doesn't send `played_at` can't say which of the two a song was, so its recent songs are the *Latest song*. Times are shown in the viewer's time zone.

Everything is plain JavaScript with no libraries: `js/endpoints.js` (every address the extension uses), `js/audd.js` (shared code: parsing, history, views), `js/viewer.js` (panel, component, widget) and `js/config.js` (configuration page).

### How songs reach the page

When a view opens, it loads the channel's last 40 songs from the backend (`getChannelById`). New songs then arrive through Twitch Extension PubSub, which the backend broadcasts to. The hosted widget, which runs outside Twitch, gets new songs from the backend's longpoll instead.

## Hosted widget

`https://widget.audd.tech/?ch=<channel>` shows the current song with a transparent background, for an OBS browser source. `<channel>` is either the numeric Twitch channel ID (the configuration page builds this address for the streamer) or `-` followed by an AudD stream's longpoll category, for streams that aren't on Twitch.

Add any of these to the address:

| Parameter | Effect |
|---|---|
| `&history` | List the songs played before the current one |
| `&background` | Draw a card behind the songs |
| `&shadow` | Add a shadow under the cover art |
| `&black-font` | Dark text, for light scenes |

The widget fits any source size; for example 600 × 160, or 600 × 420 with `&history`. To host it, serve the `frontend` folder with `panel.html` as the index page.

## Sign in with AudD

The configuration page links a channel to an AudD account without the streamer copying an API token:

1. The page starts an OAuth 2.0 device authorization ([RFC 8628](https://www.rfc-editor.org/rfc/rfc8628)) at AudD's authorization server, `https://dashboard-api.audd.io`, with the public client `audd-twitch` and the scope `token:read`, for the resource `https://mcp.audd.io`.
2. The streamer opens the AudD sign-in page in a new tab, checks the code, and approves. The page polls the token endpoint meanwhile.
3. The page sends the access token and its Twitch extension JWT to the backend's `/lastSong/auddApiToken/`. The backend accepts only a broadcaster JWT, reads the account's API token with the `get_api_token` tool of the AudD MCP server (which doesn't answer browsers on the extension's origin), and returns it.
4. The page links the account with `dashboard.audd.io/api/twitch.php?save_user`, the same request it makes for a pasted token, then revokes the OAuth tokens.

If the client or the backend endpoint isn't available, the page offers to paste an API token instead.

## Run it locally

`dev/preview` serves the extension with a stand-in for Twitch's extension helper and mocks of everything it talks to: the backend, `twitch.php`, the OAuth device flow and the MCP server. It needs only Go.

```sh
cd dev
go run ./preview
```

Then open http://127.0.0.1:8787/ for a gallery of every view in every state, light and dark. Each tile links to the page on its own; the parameters in its address select the state:

| Parameter | Values |
|---|---|
| `theme` | `dark`, `light` (what Twitch's `onContext` reports) |
| `channel` | the songs the mock backend has: `typical`, `just`, `recent`, `stale`, `one`, `full`, `long`, `nocover`, `youtube`, `legacy`, `empty`, `empty-prod`, `slow`, `down`, `flaky` |
| `every` | push a new song through PubSub every N seconds |
| `role` | `viewer`, `broadcaster` |
| `account` | what `twitch.php` reports: `none`, `active`, `expiring`, `expired`, `unpaid`, `error-<code>`, `down`, `slow` |
| `signin` | make Sign in with AudD fail: `unregistered` (unknown client), `nobackend` (backend without the endpoint), `nomcp` (MCP server unreachable) |
| `platform` | `mobile` for the Twitch mobile app |

On the configuration page, *Sign in with AudD* opens a local approval page; the mock accepts the API tokens `good-token`, `expired-token` and `unpaid-token`. The preview serves pages with a content security policy like Twitch's, so inline scripts or requests to other hosts fail the same way they would on Twitch.

`dev/screenshots.mjs` takes a screenshot of every state and reports any request outside the preview server, CSP violation or script error. It needs [playwright-core](https://www.npmjs.com/package/playwright-core) and a Chromium:

```sh
go run ./preview -before /path/to/an/older/frontend &   # -before is optional: serves it at /before/
node screenshots.mjs ./screenshots
```

## Backend

The backend (`backend/`, Go) receives the AudD stream callbacks, keeps the last 40 songs of each channel, and sends each new song to Twitch Extension PubSub, to the channel's chat, and to longpoll clients. It serves:

| Path | What |
|---|---|
| `/lastSong/callbacks/?secret=...&routes=...` | AudD stream callbacks |
| `/lastSong/getChannelById/?ch_id=<channel ID>` | A channel's songs: `{"NumericalId", "StringId", "History": {"OldestElement", "Elements": [...]}}` |
| `/lastSong/getChannel/?ch_id=<login>` | The same, by Twitch login |
| `/lastSong/longpoll/?category=<channel ID>&timeout=30&since_time=<ms>` | New songs, for clients outside Twitch |
| `/lastSong/auddApiToken/` | The Sign in with AudD step above (POST `jwt`, `access_token`) |

Each song is an AudD recognition result (`artist`, `title`, `album`, `release_date`, `label`, `song_link`, `score`) plus `played_at` (the AudD callback timestamp, Unix seconds) and `play_length` (seconds, when AudD sends it). `song_length` holds the same timestamp as text, as extension versions before 0.1.0 expect. PubSub messages carry only these fields, to stay under Twitch's 5 KB limit; the history and longpoll have the whole result.

### Routes: which stream goes to which channel

AudD sends the callbacks of every stream to one callback URL, so the backend needs to know which Twitch channel each stream (`radio_id`) belongs to. Instead of a database, it keeps that map in the callback URL itself, in a `routes` parameter:

```
https://this-service.example/lastSong/callbacks/?secret=<callbacksSecret>&routes={"1":"username1","2":"username2"}
```

Set it with the AudD [setCallbackUrl](https://docs.audd.io/streams#-2-set-the-url-for-callbacks) method, and update it the same way. A route is a Twitch login; to name a channel by its numeric Twitch ID, write `id:<ID>`, for example `{"1":"id:57165160"}`. Separate several routes for one stream with commas: `"username1,username3"`.

`callbacksSecret` must be set: the backend refuses every callback while it's empty.

### Build and run

```sh
cd backend
go build      # Go 1.21 or later
go test ./...
```

Fill in the constants at the top of `main.go` (callback secret, extension client ID and secrets, Twitch OAuth tokens, optional Telegram and Sentry settings). The service listens on `127.0.0.1:8334`; with nginx:

```
location /lastSong/ {
    proxy_request_buffering off;
    proxy_pass http://127.0.0.1:8334;
}
```

To pass the client's IP to the service, add `proxy_set_header X-Real-IP $remote_addr;` inside the `location`.

The service uses [Cloudflare's tableflip](https://github.com/cloudflare/tableflip) to restart on SIGHUP without dropping requests. tableflip doesn't support Windows: the service builds there, but stops at startup with "tableflip: platform does not support graceful restart". To run it on Windows, remove the tableflip code from `startServer`.

## Twitch console settings

The extension fetches from `api.audd.io`, `dashboard.audd.io` and `dashboard-api.audd.io`, and loads images from `lis.tn` and `i3.ytimg.com`. Allowlist them under *Allowlist for URL Fetching Domains* and *Allowlist for Image Domains*. Upload the contents of `frontend/` except `AudD music recognition.json`, which is a Twitch Developer Rig project file.
