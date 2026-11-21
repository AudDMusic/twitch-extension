module github.com/AudDMusic/twitch-extension/dev

go 1.21

require github.com/AudDMusic/twitch-extension/backend v0.0.0

require github.com/golang-jwt/jwt v3.2.2+incompatible // indirect

replace github.com/AudDMusic/twitch-extension/backend => ../backend
