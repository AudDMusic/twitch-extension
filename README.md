# AudD Music Recognition Twitch extension

The repository contains the source code for the [AudD Music Recognition Twitch extension](https://dashboard.twitch.tv/extensions/ikcbah7wbue48v7doo4edulmxblt64-0.0.4).

The extension is based on [Music Recognition API](https://audd.io/).

AudD allows you to recognize music both in audio files (from TikTok UGC to microphone recordings) and live audio streams. With AudD, you can analyze what music is popular on social media or on radio stations you connect, build nice Now Playing things, make your own shazam app, or a screen connected to your vinyl player that shows the cover of the playing song.

## Frontend

Frontend is written in JS. There are two versions of widget layouts: with history & colorful background (for the panel below the video) and without history & with no background (for being placed on top of the video).

When the frontend is being opened, it connects to the backend to get the history of the songs (including the last played song). After that, it connects to the Twitch PubSub system to get new songs.

If the frontend can't connect to Twitch pubsub but opened with `?ch=[twitch username]` in the URL, it connects directly to the backend and uses longpoll to get new songs.

## Backend

Backend is written in Golang. It receives the callbacks from AudD API, understands what is the twitch channel for the received callback, and sends the result to the Twitch PubSub, to the chat, and to connected clients.

Note that originally the service was designed to sent results not only to Twitch but also to YouTube, Discord, etc. Let us know if you want the code for sending the results to YouTube and Discord.

### More info on the backend

AudD sends callbacks with results for all the streams to a single callback URL we set, so if we want to process the callbacks differently for different streams (e.g. send results to different Twitch extensions depending on what is the radio_id in the callbacks), we need to store the information which radio_ids corresponds to which Twitch channels
somewhere.

Surely, you can just have a DB or KV-store or whatever instead of doing everything this way, but here we use the callback URL stored on the AudD's side to store the radio_ids and corresponding channels. It's done by simply having a URL parameter "routes" in the callback URL which contains a JSON map with radio_ids and routes.

So if this service is available on `http://this-service.com/lastSong/`, and we want to store the information that the stream with radio_id=1 belongs to Twitch user username1 and stream #2 to username2, we simply use something like `http://this-service.com/lastSong/callbacks/?secret=[callbacksSecret]&routes={"1":"username1","2":"username2"}` as the callback URL that we send to AudD. So when AudD sends callbacks to this URL, this service gets the routes variable.

Then we use this routes variable we stored on AudD's side to understand that we need to send the result to username1 if we got a callback for stream #1 and to username2 if we got a callback for stream #2.

And we can also update this variable at any time by sending a new URL to the AudD API using the [setCallbackUrl](https://docs.audd.io/streams#2-set-the-url-for-callbacks) method.

We have an addition to this service that brings support of Discord and YouTube bots to send results to Discord and YouTube chats, let us know if you need this addition, we'll publish it then.

By the way, routes support comma-separated values, e.g. you can send the results from a stream to `"username1,username3,youtube:[video id]"` instead of just `"username1"`.

### Using with nginx

By default, the service listens on 127.0.0.1:8334. You can change this in the startServer function. Can be used with the following nginx configuration:

```
	location /lastSong/ {
		proxy_request_buffering off;
		proxy_pass http://127.0.0.1:8334;
	}
```

  To pass the client's IP to the service, add something like `proxy_set_header X-Real-IP $remote_addr;` inside the location{}

### Building backend for Windows

The package uses the [CloudFlare's tableflip](https://github.com/cloudflare/tableflip) to gradually upgrade when get SIGHUP without losing any requests. It doesn't support Windows and the build for Windows will fail. If you want to run this service on Windows, remove the tableflip stuff in the startServer function.
