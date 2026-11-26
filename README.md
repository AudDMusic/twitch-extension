# AudD Music Recognition Twitch extension

The source code of the [AudD Music Recognition Twitch extension](https://dashboard.twitch.tv/extensions/ikcbah7wbue48v7doo4edulmxblt64). It shows viewers the song playing on a stream, with a link to open it on their music service, and posts each song in chat. The songs are recognized by the [AudD Music Recognition API](https://audd.io/) from the stream's audio.

## What's in the extension

- **Panel** (`frontend/panel.html`), below the video and in the Twitch mobile app: the song playing now, with cover art and a link to the song page, and the songs played before it. Follows Twitch's light and dark themes.
- **Video component** (`frontend/video_component.html`), over the video: the latest song as a card that fills the whole area Twitch gives the component, at any size (cover next to the text in a wide area, above it in a square or tall one), in the dark overlay style of Twitch's player controls. With no song from the last hour, or while the backend can't be reached, nothing is drawn over the video.
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

The widget fits any source size; for example 600 × 160, or 600 × 420 with `&history`. To host it, run `node dev/build-widget.mjs`: it writes `dist/index.html`, one file with the styles, the scripts and the logo inside, to serve as the index page. Opened without `?ch=`, the page says where to copy the address from.

## Sign in with AudD

The configuration page links a channel to an AudD account without the streamer copying an API token. The backend runs the sign-in; the page starts it, opens it, and asks for the result:

1. When the page shows the sign-in, it makes a secret (32 random bytes as 64 lowercase hex digits) and its SHA-256 in lowercase hex, the session. It registers the session with the backend's `/lastSong/signIn/start/`, together with its Twitch extension JWT; the backend accepts only the broadcaster's.
2. *Sign in with AudD* is a plain link to `/lastSong/signIn/open/?session=<session>`, opened in a new tab. The backend sends that tab to AudD's approval page; after the streamer approves, the backend asks them to confirm the Twitch channel with *Link account*, then says they can go back to Twitch.
3. From the click, the page asks `/lastSong/signIn/result/` every 2 seconds, for up to 10 minutes, with its JWT and the secret. Only the page knows the secret: the link carries just its hash.
4. When the result has the account's API token, the page links the account with `dashboard.audd.io/api/twitch.php?save_user`, the same request it makes for a pasted token. The backend has already ended the sign-in at AudD by then.

Each session is used once: after a result, or after 10 minutes, the page starts a new one for the next try, and it replaces an unused one before its 10 minutes run out. If the backend can't start a sign-in or can't be reached, the page offers to paste an API token instead.

## Run it locally

`dev/preview` serves the extension with a stand-in for Twitch's extension helper and mocks of everything it talks to: the backend, including its sign-in, and `twitch.php`. It needs only Go.

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
| `role` | `viewer`, `broadcaster`, or another role such as `moderator` |
| `account` | what `twitch.php` reports: `none`, `active`, `expiring`, `expired`, `unpaid`, `error-<code>`, `down`, `slow`, `slowlink` (not linked, and linking takes 8 seconds) |
| `signin` | make Sign in with AudD unavailable: `nobackend` (backend without the sign-in, 404), `down` (502), `busy` (start answers 429), `noresult` (the tab works, but the result answers 502) |
| `platform` | `mobile` for the Twitch mobile app |

On the configuration page, *Sign in with AudD* opens a local stand-in for AudD's approval page, where you pick the account and how the sign-in ends, then for the backend's *Link account* page. With `role=moderator` the backend refuses to start the sign-in. When you paste a token instead, the mock accepts `good-token`, `expired-token` and `unpaid-token`. The preview serves pages with a content security policy like Twitch's, so inline scripts or requests to other hosts fail the same way they would on Twitch.

`dev/screenshots.mjs` takes a screenshot of every state and reports any request outside the preview server, CSP violation or script error. It needs [playwright-core](https://www.npmjs.com/package/playwright-core) 1.45 or later and a Chromium:

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
| `/lastSong/signIn/start/` | Starts a sign-in (POST `jwt`, `session`): `{"status": "pending"}`, or `{"error": ...}` with 400, 401, 403 or 429 |
| `/lastSong/signIn/open/?session=<session>` | The tab *Sign in with AudD* opens: AudD's approval, then *Link account* |
| `/lastSong/signIn/result/` | A sign-in's result (POST `jwt`, `secret`): `{"status": "unknown"}` or `"pending"` while it runs, then `"done"` with `api_token`, `"declined"`, or `"failed"` with `error` (`sign_in_expired`, `token_permission_missing`, `account_service_unavailable`) |

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

The extension fetches from `api.audd.io` and `dashboard.audd.io`, and loads images from `lis.tn` and `i3.ytimg.com`. Allowlist them under *Allowlist for URL Fetching Domains* and *Allowlist for Image Domains*. Upload the contents of `frontend/` except `AudD music recognition.json`, which is a Twitch Developer Rig project file.
